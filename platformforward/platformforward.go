package platformforward

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Config struct {
	Socket   string
	Exact    []string
	Prefixes []string
}

func (c Config) IsPublicPath(path string) bool {
	for _, p := range c.Exact {
		if path == p {
			return true
		}
	}
	for _, p := range c.Prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (c Config) Handler() http.Handler {
	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "controller"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", c.Socket)
			},
			ResponseHeaderTimeout: 100 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("platform forward: controller unreachable path=%s method=%s err=%v", r.URL.Path, r.Method, err)
			http.Error(w, "service unavailable", http.StatusBadGateway)
		},
	}
}

func (c Config) Register(handle func(pattern string, prefix bool, h http.Handler)) {
	h := c.Handler()
	for _, p := range c.Exact {
		handle(p, false, h)
	}
	for _, p := range c.Prefixes {
		handle(p, true, h)
	}
}
