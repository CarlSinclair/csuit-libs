package apikit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/CarlSinclair/csuit-libs/mcpkit"
)

type testBody struct {
	Note string `json:"note"`
}

func TestWithPathParams(t *testing.T) {
	op := Op{Path: "/api/v1/bots/{id}/contacts/{cid}", Params: []Param{{Name: "id", In: "path", Description: "bot"}}}
	got := withPathParams(op)
	if len(got) != 2 {
		t.Fatalf("got %d params, want 2: %+v", len(got), got)
	}
	if got[0].Description != "bot" {
		t.Error("declared param was overwritten")
	}
	if got[1].Name != "cid" || got[1].In != "path" || !got[1].Required {
		t.Errorf("inferred param wrong: %+v", got[1])
	}
	if len(withPathParams(Op{Path: "/api/v1/things"})) != 0 {
		t.Error("params inferred for a path without variables")
	}
}

func TestPresetsFor(t *testing.T) {
	presets := PresetsFor([]string{"orders:read", "orders:write", "customers:read", "orders:cancel"})
	byID := map[string][]string{}
	for _, p := range presets {
		byID[p.ID] = p.Scopes
	}
	if !reflect.DeepEqual(byID["read_only"], []string{"customers:read", "orders:read"}) {
		t.Errorf("read_only = %v", byID["read_only"])
	}
	if !reflect.DeepEqual(byID["read_write"], []string{"customers:read", "orders:read", "orders:write"}) {
		t.Errorf("read_write = %v", byID["read_write"])
	}
	if len(byID["full"]) != 4 {
		t.Errorf("full = %v", byID["full"])
	}
	for _, id := range []string{"read_only", "read_write"} {
		for _, s := range byID[id] {
			if s == "orders:cancel" {
				t.Errorf("%s preset grants the independent verb %s", id, s)
			}
		}
	}
	for _, p := range presets {
		if !sort.StringsAreSorted(p.Scopes) {
			t.Errorf("%s scopes not sorted: %v", p.ID, p.Scopes)
		}
	}
}

func TestToolNames(t *testing.T) {
	if got := toolName(Op{Summary: "List your bots!"}); got != "list_your_bots" {
		t.Errorf("slug = %q", got)
	}
	if got := toolName(Op{Summary: "x", Tool: "custom_name"}); got != "custom_name" {
		t.Errorf("explicit tool = %q", got)
	}
}

func TestToolNameIsCapped(t *testing.T) {
	long := Op{Summary: "Update selected bot configuration fields (display name, language, donation text, cart recovery); payment settings are not changeable here"}
	got := toolName(long)
	if len(got) > maxToolNameLen || got == "" || strings.HasSuffix(got, "_") {
		t.Errorf("tool name %q (%d) is not a valid capped slug", got, len(got))
	}
	if !strings.HasPrefix(got, "update_selected_bot_configuration") {
		t.Errorf("tool name lost its meaning: %q", got)
	}
}

func TestDuplicateToolNamesStayUnique(t *testing.T) {
	auth, _ := newAuth(nil, true)
	reg := auth.NewRegistry(func(string, string, http.Handler) {})
	noop := func(http.ResponseWriter, *http.Request, *Key) {}
	for i := 0; i < 3; i++ {
		reg.Handle(Op{Method: http.MethodGet, Path: "/a" + strings.Repeat("x", i), Scope: "a:read", Summary: "Same summary"}, noop)
	}
	seen := map[string]bool{}
	for _, tool := range reg.MCPTools() {
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d tools, want 3", len(seen))
	}
}

func TestOpPath(t *testing.T) {
	get := Op{Method: http.MethodGet, Path: "/api/v1/bots/{id}/contacts", List: true,
		Params: []Param{{Name: "id", In: "path"}, {Name: "q", In: "query"}}}
	path, err := opPath(get)(map[string]any{"id": "a/b?c", "q": "x y", "limit": float64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, "/api/v1/bots/a%2Fb%3Fc/contacts?") {
		t.Errorf("path segment not escaped: %s", path)
	}
	if !strings.Contains(path, "limit=5") || !strings.Contains(path, "q=x+y") {
		t.Errorf("query missing: %s", path)
	}
	if _, err := opPath(get)(map[string]any{}); err == nil {
		t.Error("missing path param accepted")
	}
	if _, err := opPath(get)(map[string]any{"id": "  "}); err == nil {
		t.Error("blank path param accepted")
	}
	post := Op{Method: http.MethodPost, Path: "/api/v1/orders/{id}/cancel", Params: []Param{{Name: "id", In: "path"}}}
	path, err = opPath(post)(map[string]any{"id": "7", "extra": "1"})
	if err != nil || path != "/api/v1/orders/7/cancel" {
		t.Errorf("post path = %q err=%v", path, err)
	}
	postQ := Op{Method: http.MethodPost, Path: "/api/v1/orders/{id}/refund", Request: testBody{},
		Params: []Param{{Name: "id", In: "path"}, {Name: "dry_run", In: "query"}}}
	path, err = opPath(postQ)(map[string]any{"id": "7", "dry_run": "true", "note": "x"})
	if err != nil || path != "/api/v1/orders/7/refund?dry_run=true" {
		t.Errorf("post query path = %q err=%v", path, err)
	}
	raw, err := opBody(postQ)(map[string]any{"id": "7", "dry_run": "true", "note": "x"})
	if err != nil || string(raw) != `{"note":"x"}` {
		t.Errorf("post body = %s err=%v", raw, err)
	}
}

func TestOpBodyDropsPathParams(t *testing.T) {
	op := Op{Path: "/x/{id}", Request: testBody{}, Params: []Param{{Name: "id", In: "path"}}}
	raw, err := opBody(op)(map[string]any{"id": "9", "note": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, has := got["id"]; has || got["note"] != "hi" {
		t.Errorf("body = %v", got)
	}
	if opBody(Op{}) != nil {
		t.Error("body builder created for an op without a request type")
	}
}

func TestOpSchema(t *testing.T) {
	op := Op{Path: "/x/{id}", Request: testBody{}, List: true, Params: withPathParams(Op{Path: "/x/{id}"})}
	sc := opSchema(op)
	props := sc["properties"].(map[string]any)
	for _, k := range []string{"id", "note", "limit", "cursor"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema missing %s: %v", k, props)
		}
	}
	req, _ := sc["required"].([]string)
	found := false
	for _, r := range req {
		if r == "id" {
			found = true
		}
	}
	if !found {
		t.Errorf("path param not required: %v", req)
	}
}

func buildServer(t *testing.T) (*mcpkit.Server, *Authenticator) {
	t.Helper()
	auth, _ := newAuth(map[string]Key{
		"reader":   {ID: 1, Scopes: []string{"orders:read"}},
		"canceler": {ID: 2, Scopes: []string{"orders:read", "orders:cancel"}},
	}, true)
	mux := http.NewServeMux()
	reg := auth.NewRegistry(func(method, path string, h http.Handler) { mux.Handle(method+" "+path, h) })
	reg.Handle(Op{Method: http.MethodGet, Path: "/api/v1/orders/{id}", Scope: "orders:read", Summary: "Get an order", Tag: "Orders"},
		func(w http.ResponseWriter, r *http.Request, k *Key) {
			WriteJSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id")})
		})
	reg.Handle(Op{Method: http.MethodPost, Path: "/api/v1/orders/{id}/cancel", Scope: "orders:cancel", Summary: "Cancel an order", Tag: "Orders"},
		func(w http.ResponseWriter, r *http.Request, k *Key) {
			WriteJSON(w, http.StatusOK, map[string]string{"cancelled": r.PathValue("id")})
		})
	if got := reg.Scopes(); !reflect.DeepEqual(got, []string{"orders:cancel", "orders:read", "orders:write"}) {
		t.Fatalf("registry scopes = %v", got)
	}
	return &mcpkit.Server{Name: "t", Version: "1", Tools: reg.MCPTools(), Visible: auth.MCPVisible()}, auth
}

func rpc(t *testing.T, srv http.Handler, token, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp/manage", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

func toolNamesOf(resp map[string]any) []string {
	var names []string
	res, _ := resp["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	for _, x := range tools {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	return names
}

func TestMCPToolsFilteredByScope(t *testing.T) {
	srv, _ := buildServer(t)
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if got := toolNamesOf(rpc(t, srv, "reader", list)); !reflect.DeepEqual(got, []string{"get_an_order"}) {
		t.Errorf("reader sees %v", got)
	}
	if got := toolNamesOf(rpc(t, srv, "canceler", list)); !reflect.DeepEqual(got, []string{"cancel_an_order", "get_an_order"}) {
		t.Errorf("canceler sees %v", got)
	}
	if got := toolNamesOf(rpc(t, srv, "", list)); len(got) != 0 {
		t.Errorf("anonymous sees %v", got)
	}
	if got := toolNamesOf(rpc(t, srv, "bogus", list)); len(got) != 0 {
		t.Errorf("invalid key sees %v", got)
	}
}

func TestMCPCallEnforcesScope(t *testing.T) {
	srv, _ := buildServer(t)
	cancel := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cancel_an_order","arguments":{"id":"42"}}}`
	if resp := rpc(t, srv, "reader", cancel); resp["error"] == nil {
		t.Errorf("reader could call a tool outside its scope: %v", resp)
	}
	resp := rpc(t, srv, "canceler", cancel)
	res, _ := resp["result"].(map[string]any)
	if res == nil || res["isError"] == true {
		t.Fatalf("canceler call failed: %v", resp)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"cancelled":"42"`) {
		t.Errorf("handler output = %s", text)
	}
	get := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_an_order","arguments":{"id":"1/cancel"}}}`
	resp = rpc(t, srv, "canceler", get)
	text = resp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"id":"1/cancel"`) {
		t.Errorf("path value was not kept inside one segment: %s", text)
	}
	missing := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_an_order","arguments":{}}}`
	if resp := rpc(t, srv, "reader", missing); resp["error"] == nil {
		t.Errorf("missing id accepted: %v", resp)
	}
}

func TestMCPCallRevokedKeyIsRejected(t *testing.T) {
	srv, _ := buildServer(t)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_an_order","arguments":{"id":"1"}}}`
	if resp := rpc(t, srv, "nobody", call); resp["error"] == nil {
		t.Errorf("unknown key could call: %v", resp)
	}
}

func TestMCPToolsCheckScopesDoesNotPanic(t *testing.T) {
	auth, _ := newAuth(nil, true)
	reg := auth.NewRegistry(func(string, string, http.Handler) {})
	reg.Handle(Op{Method: http.MethodGet, Path: "/a", Scope: "a:read", Summary: "A"}, func(http.ResponseWriter, *http.Request, *Key) {})
	reg.CheckScopes([]string{"a:read"})
	reg.CheckScopes(nil)
}
