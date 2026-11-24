package sharedjs

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"path"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed *.js
var files embed.FS

const Prefix = "/static/shared/"

type entry struct {
	body []byte
	etag string
}

var table = func() map[string]entry {
	out := map[string]entry{}
	list, err := files.ReadDir(".")
	if err != nil {
		logger.Error("sharedjs: reading embedded scripts failed: %v", err)
		return out
	}
	for _, f := range list {
		b, err := files.ReadFile(f.Name())
		if err != nil {
			logger.Error("sharedjs: reading %s failed: %v", f.Name(), err)
			continue
		}
		sum := sha256.Sum256(b)
		out[f.Name()] = entry{body: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	return out
}()

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := path.Base(strings.TrimPrefix(r.URL.Path, Prefix))
		e, ok := table[name]
		if !ok || !strings.HasPrefix(r.URL.Path, Prefix) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", e.etag)
		if strings.Contains(r.Header.Get("If-None-Match"), e.etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(e.body); err != nil {
			logger.Warn("sharedjs: serving %s failed: %v", name, err)
		}
	})
}

func File(name string) http.Handler {
	e, ok := table[name]
	if !ok {
		logger.Error("sharedjs: no embedded script named %s", name)
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", e.etag)
		if strings.Contains(r.Header.Get("If-None-Match"), e.etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(e.body); err != nil {
			logger.Warn("sharedjs: serving %s failed: %v", name, err)
		}
	})
}
