package apikit

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Param struct {
	Name        string
	In          string
	Description string
	Required    bool
}

type Op struct {
	Method     string
	Path       string
	Scope      string
	Summary    string
	Tag        string
	Params     []Param
	Request    any
	Response   any
	Status     int
	List       bool
	Idempotent bool
}

type Registry struct {
	auth  *Authenticator
	mount func(method, path string, h http.Handler)
	ops   []Op
}

func (a *Authenticator) NewRegistry(mount func(method, path string, h http.Handler)) *Registry {
	return &Registry{auth: a, mount: mount}
}

func (g *Registry) Handle(op Op, h HandlerFunc) {
	if op.Status == 0 {
		op.Status = http.StatusOK
	}
	if op.Idempotent && op.Method != http.MethodGet {
		h = Idempotent(h)
	}
	g.ops = append(g.ops, op)
	g.mount(op.Method, op.Path, g.auth.Wrap(op.Scope, h))
}

func (g *Registry) Scopes() []string {
	set := map[string]bool{}
	for _, op := range g.ops {
		if op.Scope != "" {
			set[op.Scope] = true
			resource, action, _ := strings.Cut(op.Scope, ":")
			if action == "read" {
				set[resource+":write"] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (g *Registry) Spec(title, version string) ([]byte, error) {
	paths := map[string]map[string]any{}
	errRef := map[string]any{"$ref": "#/components/schemas/Error"}
	errResp := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": errRef}}}
	}
	for _, op := range g.ops {
		item := paths[op.Path]
		if item == nil {
			item = map[string]any{}
			paths[op.Path] = item
		}
		params := make([]map[string]any, 0, len(op.Params))
		for _, p := range op.Params {
			params = append(params, map[string]any{
				"name": p.Name, "in": p.In, "description": p.Description,
				"required": p.Required || p.In == "path", "schema": map[string]any{"type": "string"},
			})
		}
		if op.List {
			params = append(params,
				map[string]any{"name": "limit", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1}},
				map[string]any{"name": "cursor", "in": "query", "required": false, "schema": map[string]any{"type": "string"}})
		}
		responses := map[string]any{
			"400": errResp("Invalid request"),
			"401": errResp("Missing, invalid, revoked or expired key"),
			"403": errResp("Missing scope or API not enabled"),
			"429": errResp("Rate limit exceeded"),
		}
		if op.Response != nil {
			schema := schemaOf(reflect.TypeOf(op.Response), 0)
			if op.List {
				schema = map[string]any{"type": "object", "properties": map[string]any{
					"data":        map[string]any{"type": "array", "items": schema},
					"next_cursor": map[string]any{"type": "string"},
				}}
			}
			responses[statusKey(op.Status)] = map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		} else {
			responses[statusKey(op.Status)] = map[string]any{"description": "Success"}
		}
		o := map[string]any{
			"summary":          op.Summary,
			"tags":             []string{op.Tag},
			"security":         []map[string][]string{{"bearerAuth": {}}},
			"x-required-scope": op.Scope,
			"responses":        responses,
		}
		if len(params) > 0 {
			o["parameters"] = params
		}
		if op.Request != nil {
			o["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schemaOf(reflect.TypeOf(op.Request), 0)}}}
		}
		item[strings.ToLower(op.Method)] = o
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": title, "version": version},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"}},
			"schemas": map[string]any{"Error": map[string]any{"type": "object", "properties": map[string]any{
				"error": map[string]any{"type": "object", "properties": map[string]any{
					"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"},
				}},
			}}},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func (g *Registry) SpecHandler(title, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, err := g.Spec(title, version)
		if err != nil {
			WriteInternal(w, "build openapi spec", err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if _, werr := w.Write(buf); werr != nil {
			logger.Warn("apikit: write openapi spec failed err=%v", werr)
		}
	})
}

func statusKey(code int) string { return strconv.Itoa(code) }

var timeType = reflect.TypeOf(time.Time{})

func schemaOf(t reflect.Type, depth int) map[string]any {
	if t == nil || depth > 6 {
		return map[string]any{}
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), depth+1)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), depth+1)}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
			props[name] = schemaOf(f.Type, depth+1)
			if !strings.Contains(opts, "omitempty") && f.Type.Kind() != reflect.Ptr {
				required = append(required, name)
			}
		}
		out := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	default:
		return map[string]any{}
	}
}
