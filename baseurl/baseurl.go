// Package baseurl works out the public address an app is served from and
// remembers it once a signed-in admin has reached the app there. Mail links,
// payment returns and webhooks need that address, and the Host header of an
// anonymous request is never trusted for them: only an admin's own request
// can change the saved value.
package baseurl

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Detect returns the origin (scheme and host) the request was made to, or ""
// for localhost and bare IP hosts. With trustProxy it honours X-Forwarded-Host
// and X-Forwarded-Proto, which is only correct behind a reverse proxy that
// overwrites them.
func Detect(r *http.Request, trustProxy bool) string {
	host := strings.TrimSpace(r.Host)
	if trustProxy {
		if v := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); v != "" {
			host = v
		}
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	if name == "" || name == "localhost" || net.ParseIP(strings.Trim(name, "[]")) != nil {
		return ""
	}
	proto := "https"
	if trustProxy {
		if v := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); v == "http" || v == "https" {
			proto = v
		}
	} else if r.TLS == nil {
		proto = "http"
	}
	return proto + "://" + host
}

// Learner saves the address an admin reaches the app at.
type Learner struct {
	Name       string                  // app name for log lines
	TrustProxy bool                    // see Detect
	Current    func() string           // the saved address ("" when unset)
	Save       func(base string) error // persist and apply it
	OnChange   func(base string) bool  // optional: re-register webhooks; false aborts the save
	mu         sync.Mutex
	last       string
}

// Observe must only be called for a request that is already authenticated as
// an admin. It saves the request's address when it differs from the saved one.
func (l *Learner) Observe(r *http.Request) {
	b := Detect(r, l.TrustProxy)
	if b == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == b {
		return
	}
	cur := strings.TrimRight(l.Current(), "/")
	if cur == b {
		l.last = b
		return
	}
	if l.OnChange != nil && !l.OnChange(b) {
		logger.Warn("%s: public address %s not saved, the change hook refused it", l.Name, b)
		return
	}
	if err := l.Save(b); err != nil {
		logger.Error("%s: could not save public address %s (was %q): %v", l.Name, b, cur, err)
		return
	}
	l.last = b
	logger.Info("%s: public address learned from an admin session: %s (was %q)", l.Name, b, cur)
}
