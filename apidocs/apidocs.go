package apidocs

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/apikit"
	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed body.html
var bodyHTML string

//go:embed docs.css
var docsCSS []byte

type Config struct {
	Product   string
	IntroKey  string
	BaseURL   string
	Events    []string
	MCPPath   string
	RateLimit int
}

type Field struct {
	Name     string
	Type     string
	Required bool
}

type Param struct {
	Name     string
	In       string
	Required bool
}

type Operation struct {
	Anchor     string
	Method     string
	Path       string
	Scope      string
	Summary    string
	Status     int
	Paginated  bool
	Idempotent bool
	Params     []Param
	Request    []Field
	Response   []Field
	Curl       string
}

type Group struct {
	Tag    string
	Anchor string
	Ops    []Operation
}

type Doc struct {
	Config
	Groups []Group
	Scopes []string
}

func Build(ops []apikit.Op, cfg Config) Doc {
	if cfg.MCPPath == "" {
		cfg.MCPPath = "/mcp/manage"
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 120
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	byTag := map[string][]Operation{}
	var order []string
	scopes := map[string]bool{}
	for _, op := range ops {
		tag := op.Tag
		if tag == "" {
			tag = "General"
		}
		if _, ok := byTag[tag]; !ok {
			order = append(order, tag)
		}
		if op.Scope != "" {
			scopes[op.Scope] = true
		}
		o := Operation{
			Anchor:     anchor(op.Method + "-" + op.Path),
			Method:     op.Method,
			Path:       op.Path,
			Scope:      op.Scope,
			Summary:    op.Summary,
			Status:     op.Status,
			Paginated:  op.List,
			Idempotent: op.Idempotent,
		}
		for _, p := range op.Params {
			o.Params = append(o.Params, Param{Name: p.Name, In: p.In, Required: p.Required || p.In == "path"})
		}
		if op.List {
			o.Params = append(o.Params,
				Param{Name: "limit", In: "query"},
				Param{Name: "cursor", In: "query"})
		}
		var body any
		if op.Request != nil {
			schema := apikit.SchemaOf(op.Request)
			o.Request = flatten("", schema, nil)
			body = sample(schema)
		}
		if op.Response != nil {
			o.Response = flatten("", apikit.SchemaOf(op.Response), nil)
		}
		o.Curl = curl(cfg.BaseURL, op, body)
		byTag[tag] = append(byTag[tag], o)
	}
	d := Doc{Config: cfg}
	for _, t := range order {
		d.Groups = append(d.Groups, Group{Tag: t, Anchor: anchor(t), Ops: byTag[t]})
	}
	for s := range scopes {
		d.Scopes = append(d.Scopes, s)
	}
	sort.Strings(d.Scopes)
	return d
}

func anchor(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func typeName(s map[string]any) string {
	t, _ := s["type"].(string)
	if f, _ := s["format"].(string); f != "" {
		return f
	}
	if t == "array" {
		if items, ok := s["items"].(map[string]any); ok {
			return "array of " + typeName(items)
		}
	}
	if t == "" {
		return "object"
	}
	return t
}

func flatten(prefix string, s map[string]any, required map[string]bool) []Field {
	var out []Field
	props, _ := s["properties"].(map[string]any)
	if props == nil {
		if items, ok := s["items"].(map[string]any); ok {
			return flatten(prefix+"[]", items, nil)
		}
		return nil
	}
	req := map[string]bool{}
	if r, ok := s["required"].([]string); ok {
		for _, n := range r {
			req[n] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		child, _ := props[n].(map[string]any)
		full := n
		if prefix != "" {
			full = prefix + "." + n
		}
		out = append(out, Field{Name: full, Type: typeName(child), Required: req[n]})
		t, _ := child["type"].(string)
		switch t {
		case "object":
			out = append(out, flatten(full, child, nil)...)
		case "array":
			if items, ok := child["items"].(map[string]any); ok {
				if it, _ := items["type"].(string); it == "object" {
					out = append(out, flatten(full+"[]", items, nil)...)
				}
			}
		}
	}
	return out
}

func sample(s map[string]any) any {
	switch t, _ := s["type"].(string); t {
	case "string":
		if f, _ := s["format"].(string); f == "date-time" {
			return "2026-01-31T15:04:05Z"
		}
		return "string"
	case "integer":
		return 0
	case "number":
		return 0.0
	case "boolean":
		return false
	case "array":
		if items, ok := s["items"].(map[string]any); ok {
			return []any{sample(items)}
		}
		return []any{}
	case "object":
		props, _ := s["properties"].(map[string]any)
		if props == nil {
			return map[string]any{}
		}
		out := make(map[string]any, len(props))
		for n, v := range props {
			child, _ := v.(map[string]any)
			out[n] = sample(child)
		}
		return out
	}
	return nil
}

func curl(base string, op apikit.Op, body any) string {
	var b strings.Builder
	url := base + op.Path
	b.WriteString("curl -X " + op.Method + " \"" + url + "\" \\\n")
	b.WriteString("  -H \"Authorization: Bearer $API_KEY\"")
	if body != nil {
		raw, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			logger.Warn("apidocs: marshal curl sample method=%s path=%s err=%v", op.Method, op.Path, err)
			return b.String()
		}
		b.WriteString(" \\\n  -H \"Content-Type: application/json\"")
		if op.Idempotent {
			b.WriteString(" \\\n  -H \"Idempotency-Key: $(uuidgen)\"")
		}
		b.WriteString(" \\\n  -d '" + string(raw) + "'")
	}
	return b.String()
}

type view struct {
	Doc
	T             func(string) string
	WebhookSample string
	SpecPath      string
	RetryMinutes  string
}

const webhookSample = `import crypto from "node:crypto";

export function verify(secret, headers, rawBody) {
  const ts = headers["x-webhook-timestamp"];
  const given = headers["x-webhook-signature"];
  const expected = "v1=" + crypto
    .createHmac("sha256", secret)
    .update(ts + "." + rawBody)
    .digest("hex");
  const fresh = Math.abs(Date.now() / 1000 - Number(ts)) < 300;
  return fresh && given.length === expected.length &&
    crypto.timingSafeEqual(Buffer.from(given), Buffer.from(expected));
}`

func RenderBody(d Doc, T func(string) string) (template.HTML, error) {
	tpl, err := template.New("apidocs").Funcs(template.FuncMap{
		"t":     T,
		"lower": strings.ToLower,
		"itoa":  strconv.Itoa,
	}).Parse(bodyHTML)
	if err != nil {
		return "", fmt.Errorf("apidocs: parse body template: %w", err)
	}
	v := view{Doc: d, T: T, WebhookSample: webhookSample, SpecPath: "/api/v1/openapi.json", RetryMinutes: "1, 5, 30, 120, 720"}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, v); err != nil {
		return "", fmt.Errorf("apidocs: render body: %w", err)
	}
	return template.HTML(buf.String()), nil
}

func CSSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if _, err := w.Write(docsCSS); err != nil {
			logger.Warn("apidocs: write css failed err=%v", err)
		}
	})
}
