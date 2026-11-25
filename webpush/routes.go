package webpush

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Schema returns the push_subscriptions table for a dialect ("sqlite" or
// "mysql", which also needs Config.MySQL set). Products with the table already
// keep theirs.
func Schema(dialect string) string {
	if dialect == "mysql" {
		return `CREATE TABLE IF NOT EXISTS push_subscriptions (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  endpoint VARCHAR(500) NOT NULL,
  endpoint_hash CHAR(64) NOT NULL,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  user_agent TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_used_at DATETIME,
  UNIQUE KEY idx_push_endpoint_hash (endpoint_hash),
  KEY idx_push_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`
	}
	return `CREATE TABLE IF NOT EXISTS push_subscriptions(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	endpoint TEXT NOT NULL UNIQUE,
	p256dh TEXT NOT NULL,
	auth TEXT NOT NULL,
	user_agent TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	last_used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_push_user ON push_subscriptions(user_id);`
}

type subscribeBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Warn("webpush: reply not delivered: %v", err)
	}
}

// Routes mounts the browser subscription endpoints under prefix
// ("/push"): GET key, POST subscribe, POST unsubscribe, GET status. JSON is
// required on the writes, which keeps cross-site forms from reaching them.
func Routes(mux *http.ServeMux, prefix string, userID func(r *http.Request) (int64, bool)) {
	prefix = strings.TrimRight(prefix, "/")
	mux.HandleFunc("GET "+prefix+"/key", func(w http.ResponseWriter, r *http.Request) {
		if uid, ok := userID(r); !ok || uid <= 0 {
			jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
			return
		}
		key, err := GetVAPIDPublicKey()
		if err != nil {
			logger.Error("webpush: VAPID public key unavailable: %v", err)
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "system_error"})
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"key": key})
	})
	mux.HandleFunc("GET "+prefix+"/status", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := userID(r)
		if !ok || uid <= 0 {
			jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
			return
		}
		jsonReply(w, http.StatusOK, map[string]bool{"subscribed": UserHasPushSubscription(uid)})
	})
	write := func(handle func(uid int64, b subscribeBody, ua string) error, what string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			uid, ok := userID(r)
			if !ok || uid <= 0 {
				jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
				return
			}
			if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				jsonReply(w, http.StatusUnsupportedMediaType, map[string]string{"error": "bad_request"})
				return
			}
			var b subscribeBody
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&b); err != nil {
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
				return
			}
			ua := r.UserAgent()
			if len(ua) > 250 {
				ua = ua[:250]
			}
			if err := handle(uid, b, ua); err != nil {
				logger.Error("webpush: %s failed for user %d: %v", what, uid, err)
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": "push_failed"})
				return
			}
			jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
		}
	}
	mux.HandleFunc("POST "+prefix+"/subscribe", write(func(uid int64, b subscribeBody, ua string) error {
		return SavePushSubscription(uid, b.Endpoint, b.Keys.P256dh, b.Keys.Auth, ua)
	}, "subscribe"))
	mux.HandleFunc("POST "+prefix+"/unsubscribe", write(func(uid int64, b subscribeBody, _ string) error {
		return DeletePushSubscription(uid, b.Endpoint)
	}, "unsubscribe"))
}

const serviceWorkerJS = `self.addEventListener("install", function () { self.skipWaiting(); });
self.addEventListener("activate", function (event) { event.waitUntil(self.clients.claim()); });
self.addEventListener("push", function (event) {
  var data = {};
  if (event.data) {
    try { data = event.data.json(); } catch (err) { data = { body: event.data.text() }; }
  }
  var title = data.title || self.registration.scope;
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (list) {
      list.forEach(function (c) { c.postMessage({ type: "notifykit-badge", unreadCount: data.unread_count }); });
      if (list.some(function (c) { return c.visibilityState === "visible"; })) return;
      return self.registration.showNotification(title, {
        body: data.body || "",
        icon: "/static/icon.png",
        badge: "/static/icon.png",
        data: { link: data.link || "/" }
      });
    })
  );
});
self.addEventListener("notificationclick", function (event) {
  event.notification.close();
  var link = (event.notification.data && event.notification.data.link) || "/";
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (list) {
      for (var i = 0; i < list.length; i++) {
        if ("focus" in list[i]) { list[i].navigate(link); return list[i].focus(); }
      }
      return self.clients.openWindow(link);
    })
  );
});
`

// ServiceWorker serves a minimal worker that shows pushes and opens their
// link. Mount it at /sw.js so its scope is the whole site.
func ServiceWorker() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		if _, err := w.Write([]byte(serviceWorkerJS)); err != nil {
			logger.Warn("webpush: service worker not delivered: %v", err)
		}
	})
}
