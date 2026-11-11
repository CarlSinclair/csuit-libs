package idempotency

import (
	"net/http"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	HeaderName = "Idempotency-Key"
	FormField  = "idempotency_token"

	CodeKeyRequired = "idempotency_key_required"
	CodeDuplicate   = "duplicate_submission"
	CodeServerError = "server_error"

	minTokenLen = 16
	maxTokenLen = 128
	defaultTTL  = 24 * time.Hour
)

type Guard struct {
	Claim            func(scope, key string, window time.Duration) (bool, error)
	Release          func(scope, key string)
	Actor            func(r *http.Request) string
	Refuse           func(w http.ResponseWriter, r *http.Request, status int, code string)
	ErrorFlashQueued func(h http.Header) bool
}

type statusWriter struct {
	http.ResponseWriter
	guard  *Guard
	status int
	failed bool
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
		s.failed = code >= 400 || (s.guard.ErrorFlashQueued != nil && s.guard.ErrorFlashQueued(s.Header()))
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func validToken(t string) bool {
	if len(t) < minTokenLen || len(t) > maxTokenLen {
		return false
	}
	for _, c := range t {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':'
		if !ok {
			return false
		}
	}
	return true
}

func Token(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get(HeaderName)); t != "" {
		return t
	}
	return strings.TrimSpace(r.FormValue(FormField))
}

func (g *Guard) Require(scope string, next http.HandlerFunc) http.HandlerFunc {
	claimScope := "idem_" + scope
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		token := Token(r)
		if !validToken(token) {
			logger.Warn("Idempotency[%s]: missing or malformed key path=%s remote=%s len=%d", scope, r.URL.Path, r.RemoteAddr, len(token))
			g.Refuse(w, r, http.StatusBadRequest, CodeKeyRequired)
			return
		}
		key := g.Actor(r) + ":" + token
		claimed, err := g.Claim(claimScope, key, defaultTTL)
		if err != nil {
			logger.Error("Idempotency[%s]: claim failed, refusing the request path=%s err=%v", scope, r.URL.Path, err)
			g.Refuse(w, r, http.StatusInternalServerError, CodeServerError)
			return
		}
		if !claimed {
			logger.Warn("Idempotency[%s]: duplicate submission refused path=%s remote=%s", scope, r.URL.Path, r.RemoteAddr)
			g.Refuse(w, r, http.StatusConflict, CodeDuplicate)
			return
		}
		sw := &statusWriter{ResponseWriter: w, guard: g}
		defer func() {
			if rec := recover(); rec != nil {
				g.Release(claimScope, key)
				panic(rec)
			}
			if sw.status == 0 || sw.failed {
				g.Release(claimScope, key)
			}
		}()
		next(sw, r)
	}
}
