package logger

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	persistMaxKeys        = 500
	clientGoneAlertHits   = 10
	clientGoneAlertWindow = 5 * time.Minute
)

type persistEntry struct {
	hits      []time.Time
	alertedAt time.Time
}

var (
	persistMu   sync.Mutex
	persistSeen = map[string]*persistEntry{}
)

func IsClientGone(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "i/o timeout")
}

func Persistent(key string, threshold int, window time.Duration, format string, v ...interface{}) {
	now := time.Now()
	persistMu.Lock()
	e := persistSeen[key]
	if e == nil {
		if len(persistSeen) >= persistMaxKeys {
			persistSeen = map[string]*persistEntry{}
		}
		e = &persistEntry{}
		persistSeen[key] = e
	}
	cut := now.Add(-window)
	kept := e.hits[:0]
	for _, t := range e.hits {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	e.hits = append(kept, now)
	n := len(e.hits)
	escalate := n >= threshold && now.Sub(e.alertedAt) >= window
	if escalate {
		e.alertedAt = now
	}
	persistMu.Unlock()

	msg := fmt.Sprintf(format, v...)
	if escalate {
		Error("PERSISTENT %s: %d occurrences within %s, latest: %s", key, n, window, msg)
		return
	}
	Warn("%s", msg)
}

func RenderError(tmpl string, err error, r *http.Request, detail string) {
	if !IsClientGone(err) {
		Error("Template Execute Error [%s]: %v %s", tmpl, err, detail)
		return
	}
	if strings.HasPrefix(tmpl, "error") {
		Debug("client gone while writing [%s]: %v %s", tmpl, err, detail)
		return
	}
	if r != nil {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			Error("Response lost to disconnected client after %s [%s]: %v path=%s %s", r.Method, tmpl, err, r.URL.Path, detail)
			return
		}
	}
	Persistent("client-write:"+tmpl, clientGoneAlertHits, clientGoneAlertWindow,
		"client gone while writing [%s]: %v %s", tmpl, err, detail)
}

func MissingKey(key string) {
	if len(key) <= 80 && key != "" && strings.IndexFunc(key, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-')
	}) < 0 {
		Error("Missing Translation Key: '%s'", key)
		return
	}
	Warn("Lookup of non-key text ignored (len=%d)", len(key))
}
