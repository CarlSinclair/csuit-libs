package errorpage

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Config struct {
	Bypass        func(r *http.Request) bool
	Raw           func(r *http.Request) bool
	KeysForStatus func(code int) (titleKey, bodyKey string)
	Capture       func(r *http.Request) (*http.Request, func(*http.Request) *http.Request)
	PrepareRender func(w http.ResponseWriter, r *http.Request) *http.Request
	Render        func(w http.ResponseWriter, r *http.Request, data map[string]interface{})
	OnPanic       func(rec interface{})
}

type BufRW struct {
	W      http.ResponseWriter
	R      *http.Request
	Status int
	buf    bytes.Buffer
	wrote  bool
}

func (b *BufRW) Header() http.Header { return b.W.Header() }

func (b *BufRW) WriteHeader(code int) {
	if b.wrote {
		return
	}
	b.Status = code
	b.wrote = true
}

func (b *BufRW) Write(p []byte) (int, error) {
	if !b.wrote {
		b.WriteHeader(http.StatusOK)
	}
	return b.buf.Write(p)
}

func (b *BufRW) Body() string { return b.buf.String() }

func (b *BufRW) FlushToReal() {
	if b.Status == 0 {
		b.Status = http.StatusOK
	}
	b.W.WriteHeader(b.Status)
	if _, err := b.W.Write(b.buf.Bytes()); err != nil {
		logger.WriteFailed("buffered", err, b.R, fmt.Sprintf("status=%d bytes=%d", b.Status, b.buf.Len()))
	}
}

func IsAssetPath(p string) bool {
	if strings.HasPrefix(p, "/static/") {
		return true
	}
	switch p {
	case "/favicon.ico", "/robots.txt", "/sitemap.xml", "/.well-known/security.txt":
		return true
	}
	return false
}

func WantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return false
	}
	return accept == "" || strings.Contains(accept, "text/html")
}

func DefaultKeysForStatus(code int) (titleKey, bodyKey string) {
	switch {
	case code == http.StatusNotFound:
		return "page_not_found", "page_not_found_body"
	case code == http.StatusForbidden:
		return "forbidden_error", "forbidden_error_body"
	case code == http.StatusBadRequest:
		return "bad_request_error", "bad_request_error_body"
	case code >= 400 && code < 500:
		return "client_error", "client_error_body"
	default:
		return "server_error", "server_error_body"
	}
}

func (c Config) keys(code int) (string, string) {
	if c.KeysForStatus != nil {
		return c.KeysForStatus(code)
	}
	return DefaultKeysForStatus(code)
}

func (c Config) raw(r *http.Request) bool {
	return !WantsHTML(r) || (c.Raw != nil && c.Raw(r))
}

func (c Config) capture(r *http.Request) (*http.Request, func(*http.Request) *http.Request) {
	if c.Capture == nil {
		return r, func(req *http.Request) *http.Request { return req }
	}
	return c.Capture(r)
}

func (c Config) prepare(w http.ResponseWriter, r *http.Request) *http.Request {
	if c.PrepareRender == nil {
		return r
	}
	return c.PrepareRender(w, r)
}

func (c Config) ErrorPage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsAssetPath(r.URL.Path) || (c.Bypass != nil && c.Bypass(r)) {
			next.ServeHTTP(w, r)
			return
		}

		r, restore := c.capture(r)
		brw := &BufRW{W: w, R: r}
		next.ServeHTTP(brw, r)
		r = restore(r)

		if c.raw(r) {
			brw.FlushToReal()
			return
		}
		code := brw.Status
		if code == 0 {
			code = http.StatusOK
		}
		if code < 400 {
			brw.FlushToReal()
			return
		}

		titleKey, bodyKey := c.keys(code)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		if code >= 500 {
			logger.Warn("HTML error page shown: code=%d path=%s method=%s original_body=%q", code, r.URL.Path, r.Method, brw.Body())
		}
		c.Render(w, c.prepare(w, r), map[string]interface{}{
			"Code":         code,
			"Title":        titleKey,
			"Body":         bodyKey,
			"PageTitleKey": titleKey,
		})
	})
}

func (c Config) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, restore := c.capture(r)
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("PANIC: path=%s method=%s err=%v", r.URL.Path, r.Method, rec)
				r = restore(r)
				if c.OnPanic != nil {
					c.OnPanic(rec)
				}
				if c.raw(r) || IsAssetPath(r.URL.Path) {
					http.Error(w, "system_error", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				c.Render(w, c.prepare(w, r), map[string]interface{}{
					"Code":         http.StatusInternalServerError,
					"Title":        "server_error",
					"Body":         "server_error_body",
					"PageTitleKey": "server_error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
