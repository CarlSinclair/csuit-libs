package twofa

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed confirm.js
var confirmJS []byte

//go:embed telegram.js
var telegramJS []byte

//go:embed passkeystep.js
var passkeyStepJS []byte

const (
	ConfirmJSPath     = "/static/twofa/confirm.js"
	TelegramJSPath    = "/static/twofa/telegram.js"
	PasskeyStepJSPath = "/static/twofa/passkey.js"
)

func scriptTag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// ConfirmJSHandler serves the confirm-dialog script every product loads from
// ConfirmJSPath. A page can set data-cancel-class on its script tag to style
// the cancel button.
func ConfirmJSHandler() http.Handler { return scriptHandler(confirmJS) }

// TelegramJSHandler serves the sign-in Telegram approval script every product
// loads from TelegramJSPath.
func TelegramJSHandler() http.Handler { return scriptHandler(telegramJS) }

// PasskeyStepJSHandler serves the script that asks for a passkey as a factor
// on the second sign-in step.
func PasskeyStepJSHandler() http.Handler { return scriptHandler(passkeyStepJS) }

func scriptHandler(body []byte) http.Handler {
	tag := scriptTag(body)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", tag)
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(body); err != nil {
			logger.Warn("twofa: serving a script failed: %v", err)
		}
	})
}
