package apikit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/CarlSinclair/csuit-libs/mcpkit"
)

type Preset struct {
	ID     string   `json:"id"`
	Scopes []string `json:"scopes"`
}

func (g *Registry) Presets() []Preset { return PresetsFor(g.Scopes()) }

func PresetsFor(scopes []string) []Preset {
	reads := map[string]bool{}
	writes := map[string]bool{}
	all := map[string]bool{}
	for _, s := range scopes {
		all[s] = true
		_, action, _ := strings.Cut(s, ":")
		switch action {
		case "read":
			reads[s] = true
		case "write":
			writes[s] = true
		}
	}
	keys := func(m ...map[string]bool) []string {
		set := map[string]bool{}
		for _, x := range m {
			for k := range x {
				set[k] = true
			}
		}
		out := make([]string, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return []Preset{
		{ID: "read_only", Scopes: keys(reads)},
		{ID: "read_write", Scopes: keys(reads, writes)},
		{ID: "full", Scopes: keys(all)},
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

const maxToolNameLen = 64

func toolName(op Op) string {
	if op.Tool != "" {
		return op.Tool
	}
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(op.Summary), "_"), "_")
	if len(slug) > maxToolNameLen {
		slug = slug[:maxToolNameLen]
		if i := strings.LastIndex(slug, "_"); i > 16 {
			slug = slug[:i]
		}
		slug = strings.Trim(slug, "_")
	}
	return slug
}

func opSchema(op Op) map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range op.Params {
		props[p.Name] = map[string]any{"type": "string", "description": p.Description}
		if p.Required || p.In == "path" {
			required = append(required, p.Name)
		}
	}
	if op.List {
		props["limit"] = map[string]any{"type": "integer", "minimum": 1, "description": "page size"}
		props["cursor"] = map[string]any{"type": "string", "description": "next_cursor of the previous page"}
	}
	if op.Request != nil {
		body := schemaOf(reflect.TypeOf(op.Request), 0)
		if bp, ok := body["properties"].(map[string]any); ok {
			for k, v := range bp {
				props[k] = v
			}
		}
		if br, ok := body["required"].([]string); ok {
			required = append(required, br...)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func opPath(op Op) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		path := op.Path
		for _, p := range op.Params {
			if p.In != "path" {
				continue
			}
			v, ok := args[p.Name]
			if !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
				return "", fmt.Errorf("%s is required", p.Name)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(fmt.Sprint(v)))
		}
		q := url.Values{}
		for _, p := range op.Params {
			if p.In == "query" {
				if v, ok := args[p.Name]; ok {
					q.Set(p.Name, fmt.Sprint(v))
				}
			}
		}
		if op.List {
			for _, name := range []string{"limit", "cursor"} {
				if v, ok := args[name]; ok {
					q.Set(name, fmt.Sprint(v))
				}
			}
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return path, nil
	}
}

func opBody(op Op) func(args map[string]any) ([]byte, error) {
	if op.Request == nil {
		return nil
	}
	drop := map[string]bool{}
	for _, p := range op.Params {
		drop[p.Name] = true
	}
	return func(args map[string]any) ([]byte, error) {
		body := map[string]any{}
		for k, v := range args {
			if !drop[k] {
				body[k] = v
			}
		}
		return json.Marshal(body)
	}
}

func (g *Registry) MCPTools() []mcpkit.Tool {
	tools := make([]mcpkit.Tool, 0, len(g.ops))
	seen := map[string]int{}
	for i, op := range g.ops {
		name := toolName(op)
		if n := seen[name]; n > 0 {
			suffix := fmt.Sprintf("_%d", n+1)
			if len(name)+len(suffix) > maxToolNameLen {
				name = name[:maxToolNameLen-len(suffix)]
			}
			name += suffix
		}
		seen[toolName(op)]++
		tools = append(tools, mcpkit.Tool{
			Name:        name,
			Description: op.Summary + " (requires scope " + op.Scope + ")",
			Schema:      opSchema(op),
			Scope:       op.Scope,
			Method:      op.Method,
			Path:        opPath(op),
			Body:        opBody(op),
			Handler:     routed(op, g.hands[i]),
		})
	}
	return tools
}

func routed(op Op, h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(op.Method+" "+op.Path, h)
	return mux
}

func (a *Authenticator) MCPVisible() func(r *http.Request) func(t mcpkit.Tool) bool {
	return func(r *http.Request) func(t mcpkit.Tool) bool {
		k := a.Principal(r)
		return func(t mcpkit.Tool) bool {
			if t.Scope == "" {
				return true
			}
			return k != nil && k.HasScope(t.Scope)
		}
	}
}
