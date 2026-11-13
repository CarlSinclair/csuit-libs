package pwa

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Icon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type Manifest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ShortName       string   `json:"short_name"`
	Description     string   `json:"description,omitempty"`
	StartURL        string   `json:"start_url"`
	Scope           string   `json:"scope"`
	Display         string   `json:"display"`
	BackgroundColor string   `json:"background_color,omitempty"`
	ThemeColor      string   `json:"theme_color,omitempty"`
	Categories      []string `json:"categories,omitempty"`
	Icons           []Icon   `json:"icons"`
}

func StandardIcons(prefix string) []Icon {
	prefix = strings.TrimRight(prefix, "/")
	return []Icon{
		{Src: prefix + "/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
		{Src: prefix + "/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
		{Src: prefix + "/icon-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
	}
}

func (m Manifest) withDefaults() Manifest {
	if m.StartURL == "" {
		m.StartURL = "/"
	}
	if m.Scope == "" {
		m.Scope = "/"
	}
	if m.ID == "" {
		m.ID = m.StartURL
	}
	if m.Display == "" {
		m.Display = "standalone"
	}
	if m.ShortName == "" {
		short := []rune(m.Name)
		if len(short) > 12 {
			short = short[:12]
		}
		m.ShortName = string(short)
	}
	return m
}

func Handler(build func(r *http.Request) Manifest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := build(r).withDefaults()
		if strings.TrimSpace(m.Name) == "" || len(m.Icons) == 0 {
			logger.Error("pwa: manifest for %s has no name or icons", r.Host)
			http.Error(w, "manifest unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
		if err := json.NewEncoder(w).Encode(m); err != nil {
			logger.Warn("pwa: manifest write failed host=%s err=%v", r.Host, err)
		}
	})
}
