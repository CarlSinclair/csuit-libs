package demoegress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// ErrExternalDisabled is what every blocked outbound connection from the demo
// instance fails with, so a connector's own error handling logs and surfaces it
// like any other connection failure.
var ErrExternalDisabled = errors.New("external features are disabled in the demo")

type Guard struct {
	IsDemo         func() bool
	FixedHosts     []string
	ConfiguredKeys []string
	Config         func(key string) string
}

func hostnameOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (g *Guard) HostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	allowed := append([]string{}, g.FixedHosts...)
	if g.Config != nil {
		for _, key := range g.ConfiguredKeys {
			if h := hostnameOf(g.Config(key)); h != "" {
				allowed = append(allowed, h)
			}
		}
	}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// Install makes this process's default HTTP transport refuse every outbound
// connection except the allowlist. It is a no-op on any instance that is not the
// demo. Call it once at startup, after the database is initialised and before
// any worker starts.
func (g *Guard) Install() {
	if g.IsDemo == nil || !g.IsDemo() {
		return
	}
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		logger.Error("Demo egress guard NOT installed: default transport is %T, not *http.Transport", http.DefaultTransport)
		return
	}
	base := t.DialContext
	if base == nil {
		base = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if g.HostAllowed(host) {
			return base(ctx, network, addr)
		}
		logger.Warn("Demo egress blocked host=%q", host)
		return nil, ErrExternalDisabled
	}
	logger.Info("Demo egress guard installed")
}

// DialUnlessDemo is the dialer for clients that build their own transport: it
// refuses every connection on the demo and dials normally elsewhere.
func (g *Guard) DialUnlessDemo(ctx context.Context, network, addr string) (net.Conn, error) {
	if g.IsDemo != nil && g.IsDemo() {
		logger.Warn("Demo egress blocked addr=%q", addr)
		return nil, ErrExternalDisabled
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}
