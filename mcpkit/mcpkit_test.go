package mcpkit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func echoTool() Tool {
	return Tool{
		Name:   "echo",
		Scope:  "echo:read",
		Method: http.MethodPost,
		Schema: map[string]any{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}, "required": []string{"msg"}},
		Path:   func(map[string]any) (string, error) { return "/echo", nil },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			if r.Header.Get("X-Fail") != "" {
				w.WriteHeader(http.StatusConflict)
			}
			_, _ = w.Write(append([]byte(r.Header.Get("Authorization")+"|"), b...))
		}),
	}
}

func post(t *testing.T, s *Server, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Host = "example.com"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

func TestInitializeCreatesSession(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: []Tool{echoTool()}}
	rec := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, nil)
	id := rec.Header().Get("Mcp-Session-Id")
	if id == "" {
		t.Fatal("no session id issued")
	}
	res := decode(t, rec)["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", res["protocolVersion"])
	}
	if rec := post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{"Mcp-Session-Id": id}); rec.Code != http.StatusOK {
		t.Errorf("valid session rejected: %d", rec.Code)
	}
	if rec := post(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{"Mcp-Session-Id": "unknown"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown session status = %d", rec.Code)
	}
}

func TestSessionTableIsCapped(t *testing.T) {
	s := &Server{Name: "t", Version: "1"}
	for i := 0; i < maxSessions; i++ {
		if _, err := s.createSession(); err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
	}
	rec := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("full table status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on a full session table")
	}
	s.mu.Lock()
	for k := range s.sessions {
		s.sessions[k] = time.Now().Add(-time.Second)
	}
	s.mu.Unlock()
	if _, err := s.createSession(); err != nil {
		t.Errorf("expired sessions were not reclaimed: %v", err)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	s := &Server{Name: "t", Version: "1"}
	id, err := s.createSession()
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sessions[id] = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if s.touchSession(id) {
		t.Error("expired session accepted")
	}
}

func TestOriginCheck(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: []Tool{echoTool()}}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if rec := post(t, s, body, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign origin status = %d", rec.Code)
	}
	if rec := post(t, s, body, map[string]string{"Origin": "https://example.com"}); rec.Code != http.StatusOK {
		t.Errorf("same origin status = %d", rec.Code)
	}
	if rec := post(t, s, body, nil); rec.Code != http.StatusOK {
		t.Errorf("no origin status = %d", rec.Code)
	}
	if rec := post(t, s, body, map[string]string{"Origin": "://bad"}); rec.Code != http.StatusForbidden {
		t.Errorf("malformed origin status = %d", rec.Code)
	}
}

func TestMethodsAndParseErrors(t *testing.T) {
	s := &Server{Name: "t", Version: "1"}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d", rec.Code)
	}
	if e := decode(t, post(t, s, `{not json`, nil))["error"]; e == nil {
		t.Error("parse error not reported")
	}
	if e := decode(t, post(t, s, `{"jsonrpc":"2.0","id":1,"method":"nope\nforged log line"}`, nil))["error"]; e == nil {
		t.Error("unknown method accepted")
	}
	req = httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d", rec.Code)
	}
}

func TestVisibilityFilterRunsOncePerRequest(t *testing.T) {
	calls := 0
	s := &Server{Name: "t", Version: "1", Tools: []Tool{echoTool(), echoTool(), echoTool()}, Visible: func(r *http.Request) func(Tool) bool {
		calls++
		return func(Tool) bool { return false }
	}}
	post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if calls != 1 {
		t.Errorf("filter factory ran %d times for one tools/list, want 1", calls)
	}
	res := decode(t, post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil))["result"].(map[string]any)
	if n := len(res["tools"].([]any)); n != 0 {
		t.Errorf("hidden tools listed: %d", n)
	}
}

func TestCallForwardsHeadersAndBody(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: []Tool{echoTool()}}
	rec := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`,
		map[string]string{"Authorization": "Bearer k1"})
	res := decode(t, rec)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "Bearer k1|") || !strings.Contains(text, `"msg":"hi"`) {
		t.Errorf("handler saw %q", text)
	}
	if res["isError"] != false {
		t.Errorf("isError = %v", res["isError"])
	}
	rec = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`,
		map[string]string{"X-Fail": "1"})
	if decode(t, rec)["result"].(map[string]any)["isError"] != true {
		t.Error("HTTP 409 not reported as isError")
	}
}

func TestCallValidation(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: []Tool{echoTool()}}
	cases := map[string]string{
		"unknown tool":     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		"missing required": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		"blank required":   `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"  "}}}`,
		"bad arguments":    `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":[1]}}`,
		"bad params":       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"x"}`,
	}
	for name, body := range cases {
		if decode(t, post(t, s, body, nil))["error"] == nil {
			t.Errorf("%s: no error returned", name)
		}
	}
}

func TestSSEResponse(t *testing.T) {
	s := &Server{Name: "t", Version: "1"}
	rec := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, map[string]string{"Accept": "application/json, text/event-stream"})
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type = %q", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "event: message\ndata: {") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
