package secheaders

import (
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"net/http"
	"sync/atomic"
)

type Live struct {
	build func() *Config
	cur   atomic.Pointer[Config]
}

func NewLive(build func() *Config) *Live {
	l := &Live{build: build}
	l.Reload()
	return l
}

func (l *Live) Reload() *Config {
	c := l.build()
	l.cur.Store(c)
	return c
}

func (l *Live) Config() *Config { return l.cur.Load() }

type compiled struct {
	cfg *Config
	h   http.Handler
}

func (l *Live) wrap(next http.Handler, wrapper func(*Config, http.Handler) http.Handler) http.Handler {
	var cached atomic.Pointer[compiled]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := l.cur.Load()
		c := cached.Load()
		if c == nil || c.cfg != cfg {
			c = &compiled{cfg: cfg, h: wrapper(cfg, next)}
			cached.Store(c)
		}
		c.h.ServeHTTP(w, r)
	})
}

func (l *Live) Middleware(next http.Handler) http.Handler {
	return l.wrap(next, func(c *Config, n http.Handler) http.Handler { return c.Middleware(n) })
}

func (l *Live) CORS(next http.Handler) http.Handler {
	return l.wrap(next, func(c *Config, n http.Handler) http.Handler { return c.CORS(n) })
}

func (l *Live) Watch(interval time.Duration, fingerprint func() string) {
	last := fingerprint()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			cur := fingerprint()
			if cur == last {
				continue
			}
			last = cur
			l.Reload()
			logger.Info("secheaders: settings changed, security headers reloaded")
		}
	}()
}

func Fingerprint(get Settings) string {
	keys := []string{KeyXFrameOptions, KeyHSTSMaxAge, KeyHSTSIncludeSubs, KeyHSTSPreload, KeyHSTSDisabled, KeyCSP, KeyCSPExtra, KeyXContentTypeOptions, KeyReferrerPolicy, KeyPermissionsPolicy, KeyCORSOrigins}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(get(k))
		b.WriteByte('\n')
	}
	return b.String()
}
