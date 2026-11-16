package notifykit

import (
	"embed"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed assets/notifykit.js assets/notifykit.css
var assetFS embed.FS

func AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var ctype string
		switch name {
		case "notifykit.js":
			ctype = "application/javascript; charset=utf-8"
		case "notifykit.css":
			ctype = "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := assetFS.ReadFile("assets/" + name)
		if err != nil {
			logger.Error("notifykit: embedded asset %s unreadable: %v", name, err)
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if _, err := w.Write(data); err != nil {
			logger.Error("notifykit: write asset %s failed: %v", name, err)
		}
	})
}

func JS() ([]byte, error)  { return assetFS.ReadFile("assets/notifykit.js") }
func CSS() ([]byte, error) { return assetFS.ReadFile("assets/notifykit.css") }
