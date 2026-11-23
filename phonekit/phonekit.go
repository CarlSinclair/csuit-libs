// Package phonekit serves the shared phone-number input script: a country
// code field with a flag that confirms the detected country, a national
// number field and one hidden E.164 field. Products mount Handler at JSPath
// and vendor the flag SVGs (flag-icons, 4x3) into static/vendor/flags/.
package phonekit

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed phone-input.js
var js []byte

const JSPath = "/static/phonekit/phone-input.js"

// FlagsVendorDir is where each product keeps the flag SVGs (listed in its vendor.json).
const FlagsVendorDir = "static/vendor/flags/"

func Handler() http.Handler {
	sum := sha256.Sum256(js)
	tag := `"` + hex.EncodeToString(sum[:8]) + `"`
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
		if _, err := w.Write(js); err != nil {
			logger.Warn("phonekit: serving script failed: %v", err)
		}
	})
}
