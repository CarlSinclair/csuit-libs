package securitytxt

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

var securityTxtHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func securityTxtHost(r *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 || !securityTxtHostRe.MatchString(host) {
		return ""
	}
	return host
}

func Handler(owner, languages string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, owner, languages)
	}
}

func handle(w http.ResponseWriter, r *http.Request, owner, languages string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host := securityTxtHost(r)
	if host == "" {
		logger.Warn("security.txt: rejected request with unusable Host header %q from %s", r.Host, r.RemoteAddr)
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}
	expires := time.Now().UTC().AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z")
	body := fmt.Sprintf("# Security contact for %[1]s - %[3]s\nContact: mailto:sec@%[1]s\nExpires: %[2]s\nPreferred-Languages: %[4]s\nCanonical: https://%[1]s/.well-known/security.txt\n", host, expires, owner, languages)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write([]byte(body)); err != nil {
		logger.Warn("security.txt: write failed host=%s: %v", host, err)
	}
}
