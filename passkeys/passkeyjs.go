package passkeys

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed passkey.js
var passkeyJS []byte

const JSPath = "/static/passkeys/passkey.js"

var passkeyJSTag = func() string {
	sum := sha256.Sum256(passkeyJS)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}()

// JSHandler serves the add and sign-in passkey script every product loads
// from JSPath. It binds by event delegation, so it survives AJAX page swaps.
func JSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", passkeyJSTag)
		if r.Header.Get("If-None-Match") == passkeyJSTag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(passkeyJS); err != nil {
			logger.Warn("passkeys: serving the script failed: %v", err)
		}
	})
}
