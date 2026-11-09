package secheaders

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/gorilla/handlers"
)

const (
	KeyXFrameOptions       = "security_x_frame_options"
	KeyHSTSMaxAge          = "security_hsts_max_age_seconds"
	KeyHSTSIncludeSubs     = "security_hsts_include_subdomains"
	KeyHSTSPreload         = "security_hsts_preload"
	KeyHSTSDisabled        = "security_hsts_disabled"
	KeyCSP                 = "security_csp"
	KeyCSPExtra            = "security_csp_extra"
	KeyXContentTypeOptions = "security_x_content_type_options"
	KeyReferrerPolicy      = "security_referrer_policy"
	KeyPermissionsPolicy   = "security_permissions_policy"
	KeyCORSOrigins         = "security_cors_allowed_origins"
)

type Settings func(key string) string

type Request struct {
	CSP              *CSP
	OmitFrameOptions bool
	CacheControl     string
	Headers          map[string]string
}

type Config struct {
	XFrameOptions         string
	HSTSMaxAgeSeconds     int
	HSTSIncludeSubDomains bool
	HSTSPreload           bool
	HSTSDisabled          bool
	CSP                   *CSP
	XContentTypeOptions   string
	ReferrerPolicy        string
	PermissionsPolicy     string
	CORSAllowedOrigins    []string
	CORSAllowedHeaders    []string
	StyleNonce            bool
	NoStoreCacheControl   string
	StaticPrefixes        []string
	StaticPaths           []string
	StaticCacheControl    string
	LegacyHeaders         bool
	PerRequest            func(r *http.Request, rq *Request)
}

func Defaults() *Config {
	return &Config{
		XFrameOptions:         "DENY",
		HSTSMaxAgeSeconds:     63072000,
		HSTSIncludeSubDomains: true,
		HSTSPreload:           true,
		CSP:                   NewCSP(),
		XContentTypeOptions:   "nosniff",
		ReferrerPolicy:        "no-referrer",
		PermissionsPolicy:     "camera=(), microphone=(), geolocation=(), payment=()",
		CORSAllowedHeaders:    []string{"Content-Type", "Authorization", "X-CSRF-Token"},
		NoStoreCacheControl:   "no-cache, no-store, must-revalidate",
		StaticCacheControl:    "public, max-age=3600",
		LegacyHeaders:         true,
	}
}

func (c *Config) ApplySettings(get Settings) {
	if get == nil {
		return
	}
	val := func(key string) string { return strings.TrimSpace(get(key)) }
	if v := val(KeyXFrameOptions); v != "" {
		c.XFrameOptions = v
	}
	if v := val(KeyHSTSMaxAge); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec >= 0 {
			c.HSTSMaxAgeSeconds = sec
			c.HSTSDisabled = false
		} else {
			logger.Warn("secheaders: ignoring invalid %s=%q", KeyHSTSMaxAge, v)
		}
	}
	if v := val(KeyHSTSIncludeSubs); v != "" {
		c.HSTSIncludeSubDomains = strings.EqualFold(v, "true")
	}
	if v := val(KeyHSTSPreload); v != "" {
		c.HSTSPreload = strings.EqualFold(v, "true")
	}
	if v := val(KeyHSTSDisabled); v != "" {
		c.HSTSDisabled = strings.EqualFold(v, "true")
	}
	if v := val(KeyCSP); v != "" {
		c.CSP = ParseCSP(v)
		logger.Warn("secheaders: Content-Security-Policy fully overridden by %s", KeyCSP)
		if strings.Contains(v, "'unsafe-inline'") {
			logger.Warn("secheaders: %s still contains 'unsafe-inline'", KeyCSP)
		}
	}
	if v := val(KeyCSPExtra); v != "" {
		extra := map[string][]string{}
		if err := json.Unmarshal([]byte(v), &extra); err != nil {
			logger.Error("secheaders: %s is not valid JSON, extra CSP sources ignored: %v", KeyCSPExtra, err)
		} else {
			for directive, sources := range extra {
				c.CSP.Add(directive, sources...)
			}
		}
	}
	if v := val(KeyXContentTypeOptions); v != "" {
		c.XContentTypeOptions = v
	}
	if v := val(KeyReferrerPolicy); v != "" {
		c.ReferrerPolicy = v
	}
	if v := val(KeyPermissionsPolicy); v != "" {
		c.PermissionsPolicy = v
	}
	if v := val(KeyCORSOrigins); v != "" {
		c.CORSAllowedOrigins = MergeOrigins(c.CORSAllowedOrigins, strings.Split(v, ","))
	}
}

func MergeOrigins(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, o := range list {
			o = strings.TrimSpace(o)
			if o == "" || seen[o] {
				continue
			}
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

type nonceKey struct{}

func Nonce(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(nonceKey{}).(string); ok {
		return v
	}
	return ""
}

func newNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func (c *Config) isStatic(path string) bool {
	for _, p := range c.StaticPaths {
		if path == p {
			return true
		}
	}
	for _, p := range c.StaticPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (c *Config) hsts() string {
	if c.HSTSDisabled || c.HSTSMaxAgeSeconds <= 0 {
		return ""
	}
	v := fmt.Sprintf("max-age=%d", c.HSTSMaxAgeSeconds)
	if c.HSTSIncludeSubDomains {
		v += "; includeSubDomains"
	}
	if c.HSTSPreload {
		v += "; preload"
	}
	return v
}

func (c *Config) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rq := &Request{CSP: c.CSP.Clone()}
		if c.isStatic(r.URL.Path) {
			rq.CacheControl = c.StaticCacheControl
		} else {
			rq.CacheControl = c.NoStoreCacheControl
		}
		if c.PerRequest != nil {
			c.PerRequest(r, rq)
		}
		h := w.Header()
		if c.XFrameOptions != "" && !rq.OmitFrameOptions {
			h.Set("X-Frame-Options", c.XFrameOptions)
		}
		if v := c.hsts(); v != "" {
			h.Set("Strict-Transport-Security", v)
		}
		if csp := rq.CSP; csp != nil && len(csp.order) > 0 {
			if c.StyleNonce && csp.Has("style-src") {
				nonce, err := newNonce()
				if err != nil {
					logger.Error("secheaders: could not generate CSP style nonce for %s %s: %v", r.Method, r.URL.Path, err)
				} else {
					csp.Add("style-src", "'nonce-"+nonce+"'")
					r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
				}
			}
			h.Set("Content-Security-Policy", csp.String())
		}
		if c.XContentTypeOptions != "" {
			h.Set("X-Content-Type-Options", c.XContentTypeOptions)
		}
		if c.ReferrerPolicy != "" {
			h.Set("Referrer-Policy", c.ReferrerPolicy)
		}
		if c.PermissionsPolicy != "" {
			h.Set("Permissions-Policy", c.PermissionsPolicy)
		}
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		if c.LegacyHeaders {
			h.Set("X-XSS-Protection", "1; mode=block")
		}
		if rq.CacheControl != "" {
			h.Set("Cache-Control", rq.CacheControl)
			if rq.CacheControl == c.NoStoreCacheControl {
				h.Set("Pragma", "no-cache")
				h.Set("Expires", "0")
			}
		}
		for k, v := range rq.Headers {
			h.Set(k, v)
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Config) CORS(next http.Handler) http.Handler {
	if len(c.CORSAllowedOrigins) == 0 {
		return next
	}
	return handlers.CORS(
		handlers.AllowedOrigins(c.CORSAllowedOrigins),
		handlers.AllowedMethods([]string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"}),
		handlers.AllowedHeaders(c.CORSAllowedHeaders),
		handlers.AllowCredentials(),
		handlers.MaxAge(300),
	)(next)
}

func RequestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Forwarded"))), "proto=https")
}
