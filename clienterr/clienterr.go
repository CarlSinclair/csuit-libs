package clienterr

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	ReportPath = "/api/client-error"
	ScriptPath = "/client-error.js"

	maxBody       = 16 * 1024
	maxPerMinute  = 60
	maxKeyMessage = 160
)

//go:embed report.js
var script []byte

type report struct {
	Context   string      `json:"context"`
	Message   string      `json:"message"`
	Stack     string      `json:"stack"`
	Extra     interface{} `json:"extra"`
	URL       string      `json:"url"`
	UserAgent string      `json:"user_agent"`
}

var (
	limitMu      sync.Mutex
	limitWindow  time.Time
	limitCount   int
	limitDropped int
)

func allow() bool {
	limitMu.Lock()
	defer limitMu.Unlock()
	now := time.Now()
	if now.Sub(limitWindow) >= time.Minute {
		if limitDropped > 0 {
			logger.Warn("clienterr: dropped %d reports in the last minute (limit %d per minute)", limitDropped, maxPerMinute)
		}
		limitWindow = now
		limitCount = 0
		limitDropped = 0
	}
	if limitCount >= maxPerMinute {
		limitDropped++
		return false
	}
	limitCount++
	return true
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func pageOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return truncate(u.Host+u.Path, 300)
}

func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		defer r.Body.Close()

		var req report
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn("CLIENT_ERROR: unreadable report body err=%v", err)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !allow() {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		extra := ""
		if req.Extra != nil {
			if b, err := json.Marshal(req.Extra); err == nil {
				extra = truncate(string(b), 1000)
			}
		}
		page := pageOnly(req.URL)
		alertKey := truncate(req.Context, 80) + "|" + truncate(req.Message, maxKeyMessage) + "|" + page
		if strings.TrimSpace(req.Message) == "" {
			if m := extraMessage(req.Extra); m != "" {
				alertKey = truncate(req.Context, 80) + "|" + truncate(m, maxKeyMessage) + "|" + page
			}
		}
		logger.ErrorKeyed(alertKey, "CLIENT_ERROR context=%q message=%q page=%q ua=%q extra=%s stack=%s",
			truncate(req.Context, 200),
			truncate(req.Message, 500),
			page,
			truncate(req.UserAgent, 300),
			extra,
			truncate(req.Stack, 1500),
		)
		w.WriteHeader(http.StatusNoContent)
	}
}

func extraMessage(extra interface{}) string {
	m, ok := extra.(map[string]interface{})
	if !ok {
		return ""
	}
	s, _ := m["message"].(string)
	return s
}

func ScriptHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodHead {
			return
		}
		if _, err := w.Write(script); err != nil {
			logger.Warn("clienterr: script write failed err=%v", err)
		}
	}
}
