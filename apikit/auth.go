package apikit

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

type HandlerFunc func(w http.ResponseWriter, r *http.Request, k *Key)

type Config struct {
	Store            Store
	Enabled          func() bool
	ClientIP         func(*http.Request) string
	OnWrite          func(k *Key, r *http.Request, status int)
	DefaultRateLimit int
	RateWindow       time.Duration
	FailLimit        int
	FailWindow       time.Duration
}

type Authenticator struct {
	cfg Config
}

type ctxKey struct{}

func New(cfg Config) *Authenticator {
	if cfg.DefaultRateLimit <= 0 {
		cfg.DefaultRateLimit = 120
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = time.Minute
	}
	if cfg.FailLimit <= 0 {
		cfg.FailLimit = 20
	}
	if cfg.FailWindow <= 0 {
		cfg.FailWindow = time.Minute
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = func(r *http.Request) string { return r.RemoteAddr }
	}
	return &Authenticator{cfg: cfg}
}

func FromContext(ctx context.Context) (*Key, bool) {
	k, ok := ctx.Value(ctxKey{}).(*Key)
	return k, ok
}

func BearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func (a *Authenticator) authFailed(w http.ResponseWriter, r *http.Request, code, message string) {
	ip := a.cfg.ClientIP(r)
	ok, retry, err := redis.Allow("apikit:authfail:"+ip, a.cfg.FailLimit, a.cfg.FailWindow)
	if err != nil {
		logger.Warn("apikit: auth failure limiter error ip=%s err=%v", ip, err)
	}
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many failed authentication attempts")
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	WriteError(w, http.StatusUnauthorized, code, message)
}

func (a *Authenticator) Wrap(scope string, h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Enabled != nil && !a.cfg.Enabled() {
			WriteError(w, http.StatusForbidden, "api_not_enabled", "API access is not enabled for this account")
			return
		}
		raw := BearerToken(r)
		if raw == "" {
			a.authFailed(w, r, "unauthorized", "missing bearer token")
			return
		}
		k, err := a.cfg.Store.Lookup(r.Context(), HashToken(raw))
		if err != nil {
			WriteInternal(w, "key lookup", err)
			return
		}
		now := time.Now()
		switch {
		case k == nil:
			a.authFailed(w, r, "invalid_key", "the API key is not valid")
			return
		case k.Revoked():
			a.authFailed(w, r, "key_revoked", "the API key was revoked")
			return
		case k.Expired(now):
			a.authFailed(w, r, "key_expired", "the API key has expired")
			return
		}
		if !k.HasScope(scope) {
			WriteError(w, http.StatusForbidden, "insufficient_scope", "this key is missing the scope "+scope)
			return
		}
		limit := k.RateLimit
		if limit <= 0 {
			limit = a.cfg.DefaultRateLimit
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
		ok, retry, rerr := redis.Allow("apikit:key:"+strconv.FormatInt(k.ID, 10), limit, a.cfg.RateWindow)
		if rerr != nil {
			logger.Warn("apikit: rate limiter error key_id=%d err=%v", k.ID, rerr)
		}
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded for this key")
			return
		}
		if terr := a.cfg.Store.Touch(r.Context(), k.ID, a.cfg.ClientIP(r), now); terr != nil {
			logger.Warn("apikit: touch failed key_id=%d err=%v", k.ID, terr)
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, k))
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r, k)
		if a.cfg.OnWrite != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			a.cfg.OnWrite(k, r, status)
		}
	})
}

func (a *Authenticator) failureBudgetLeft(r *http.Request) bool {
	ip := a.cfg.ClientIP(r)
	ok, _, err := redis.Allow("apikit:authfail:"+ip, a.cfg.FailLimit, a.cfg.FailWindow)
	if err != nil {
		logger.Warn("apikit: auth failure limiter error ip=%s err=%v", ip, err)
	}
	return ok
}

func (a *Authenticator) Principal(r *http.Request) *Key {
	if a.cfg.Enabled != nil && !a.cfg.Enabled() {
		return nil
	}
	raw := BearerToken(r)
	if raw == "" {
		return nil
	}
	k, err := a.cfg.Store.Lookup(r.Context(), HashToken(raw))
	if err != nil {
		logger.Warn("apikit: principal lookup failed err=%v", err)
		return nil
	}
	if k == nil || k.Revoked() || k.Expired(time.Now()) {
		a.failureBudgetLeft(r)
		return nil
	}
	return k
}
