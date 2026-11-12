package mcpkit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/agentnotice"
	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	protocolVersion = "2025-06-18"
	sessionTTL      = time.Hour
	maxSessions     = 5000
	sweepEvery      = time.Minute
)

type Tool struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Schema      map[string]any
	Scope       string
	Method      string
	Path        func(args map[string]any) (string, error)
	Body        func(args map[string]any) ([]byte, error)
	Handler     http.Handler
}

type Server struct {
	Name    string
	Version string
	Tools   []Tool

	Visible func(r *http.Request) func(t Tool) bool

	mu        sync.Mutex
	sessions  map[string]time.Time
	lastSweep time.Time
}

func (t Tool) required() []string {
	if t.Schema == nil {
		return t.Required
	}
	switch v := t.Schema["required"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if n, ok := e.(string); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

func (s *Server) filter(r *http.Request) func(t Tool) bool {
	if s.Visible == nil {
		return func(Tool) bool { return true }
	}
	return s.Visible(r)
}

func (s *Server) toolDefs(r *http.Request) []map[string]any {
	out := make([]map[string]any, 0, len(s.Tools))
	visible := s.filter(r)
	for _, t := range s.Tools {
		if !visible(t) {
			continue
		}
		if t.Schema != nil {
			out = append(out, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
			continue
		}
		props := t.Properties
		if props == nil {
			props = map[string]any{}
		}
		req := t.Required
		if req == nil {
			req = []string{}
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": req},
		})
	}
	return out
}

var errSessionsFull = errors.New("mcpkit: session table is full")

func (s *Server) sweepLocked(now time.Time, force bool) {
	if !force && now.Sub(s.lastSweep) < sweepEvery {
		return
	}
	s.lastSweep = now
	for k, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, k)
		}
	}
}

func (s *Server) createSession() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]time.Time{}
	}
	now := time.Now()
	s.sweepLocked(now, len(s.sessions) >= maxSessions)
	if len(s.sessions) >= maxSessions {
		return "", errSessionsFull
	}
	s.sessions[id] = now.Add(sessionTTL)
	return id, nil
}

func (s *Server) touchSession(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.sweepLocked(now, false)
	exp, ok := s.sessions[id]
	if !ok || now.After(exp) {
		return false
	}
	s.sessions[id] = now.Add(sessionTTL)
	return true
}

func validOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validOrigin(r) {
		logger.Warn("MCP %s: rejected origin=%q host=%q", s.Name, r.Header.Get("Origin"), r.Host)
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if id := strings.TrimSpace(r.Header.Get("Mcp-Session-Id")); id != "" {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := ""
	if id := strings.TrimSpace(r.Header.Get("Mcp-Session-Id")); id != "" {
		if !s.touchSession(id) {
			http.Error(w, "unknown or expired session", http.StatusNotFound)
			return
		}
		sessionID = id
	}

	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeError(w, r, "", nil, -32700, "parse error")
		return
	}

	switch req.Method {
	case "initialize":
		newID, err := s.createSession()
		if err != nil {
			if errors.Is(err, errSessionsFull) {
				logger.Warn("MCP %s: session table full, rejecting initialize", s.Name)
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many sessions", http.StatusServiceUnavailable)
				return
			}
			logger.Error("MCP %s: session id generation failed err=%v", s.Name, err)
			s.writeError(w, r, "", req.ID, -32603, "internal error")
			return
		}
		s.writeResult(w, r, newID, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})
	case "tools/list":
		s.writeResult(w, r, sessionID, req.ID, map[string]any{"tools": s.toolDefs(r)})
	case "tools/call":
		s.call(w, r, sessionID, req.ID, req.Params)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	default:
		s.writeError(w, r, sessionID, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) call(w http.ResponseWriter, r *http.Request, sessionID string, id, params json.RawMessage) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		s.writeError(w, r, sessionID, id, -32602, "invalid params")
		return
	}
	var tool *Tool
	visible := s.filter(r)
	for i := range s.Tools {
		if s.Tools[i].Name == call.Name && visible(s.Tools[i]) {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		s.writeError(w, r, sessionID, id, -32601, "unknown tool: "+call.Name)
		return
	}
	args := map[string]any{}
	if len(strings.TrimSpace(string(call.Arguments))) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			logger.Warn("MCP %s: invalid tool arguments tool=%q err=%v", s.Name, call.Name, err)
			s.writeError(w, r, sessionID, id, -32602, "invalid params")
			return
		}
	}
	for _, name := range tool.required() {
		if v, ok := args[name]; !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
			s.writeError(w, r, sessionID, id, -32602, name+" is required")
			return
		}
	}
	path, err := tool.Path(args)
	if err != nil {
		s.writeError(w, r, sessionID, id, -32602, err.Error())
		return
	}
	rawBody := []byte(call.Arguments)
	if tool.Body != nil {
		built, berr := tool.Body(args)
		if berr != nil {
			s.writeError(w, r, sessionID, id, -32602, berr.Error())
			return
		}
		rawBody = built
	}
	body := strings.NewReader(string(rawBody))
	r2, err := http.NewRequestWithContext(r.Context(), tool.Method, path, body)
	if err != nil {
		logger.Error("MCP %s: could not build request tool=%q err=%v", s.Name, call.Name, err)
		s.writeError(w, r, sessionID, id, -32603, "internal error")
		return
	}
	r2.Host = r.Host
	r2.RemoteAddr = r.RemoteAddr
	r2.TLS = r.TLS
	r2.Header = r.Header.Clone()
	for _, h := range []string{"Accept", "Accept-Encoding", "Mcp-Session-Id", "Content-Length", "Content-Encoding", "Origin"} {
		r2.Header.Del(h)
	}
	r2.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tool.Handler.ServeHTTP(rec, r2)
	s.writeResult(w, r, sessionID, id, map[string]any{
		"isError": rec.Code >= 400,
		"content": []map[string]any{
			{"type": "text", "text": rec.Body.String()},
			agentnotice.MCPContent(),
		},
	})
}

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, sessionID string, id json.RawMessage, result any) {
	s.writeEnvelope(w, r, sessionID, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, sessionID string, id json.RawMessage, code int, msg string) {
	logger.Warn("MCP %s: error session=%q code=%d msg=%q", s.Name, sessionID, code, msg)
	s.writeEnvelope(w, r, sessionID, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (s *Server) writeEnvelope(w http.ResponseWriter, r *http.Request, sessionID string, envelope map[string]any) {
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		logger.Error("MCP %s: response marshal failed err=%v", s.Name, err)
		http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"}}`, http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if _, werr := w.Write([]byte("event: message\ndata: " + string(body) + "\n\n")); werr != nil {
			logger.Warn("MCP %s: SSE write failed path=%s err=%v", s.Name, r.URL.Path, werr)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, werr := w.Write(body); werr != nil {
		logger.Warn("MCP %s: response write failed path=%s err=%v", s.Name, r.URL.Path, werr)
	}
}
