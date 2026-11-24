package qrscan

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed qrscan.js
var scanJS []byte

//go:embed jsQR.min.js
var decoderJS []byte

const (
	JSPath      = "/static/qrscan/qrscan.js"
	DecoderPath = "/static/qrscan/jsQR.min.js"
)

func tag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

var (
	scanTag    = tag(scanJS)
	decoderTag = tag(decoderJS)
)

func serve(body []byte, etag string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if strings.Contains(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(body); err != nil {
			logger.Warn("qrscan: serving %s failed: %v", r.URL.Path, err)
		}
	})
}

func JSHandler() http.Handler      { return serve(scanJS, scanTag) }
func DecoderHandler() http.Handler { return serve(decoderJS, decoderTag) }
