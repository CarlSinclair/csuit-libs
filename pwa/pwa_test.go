package pwa

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerFillsDefaults(t *testing.T) {
	h := Handler(func(*http.Request) Manifest {
		return Manifest{Name: "A Very Long Application Name", Icons: StandardIcons("/static/")}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.StartURL != "/" || m.Scope != "/" || m.ID != "/" || m.Display != "standalone" || m.ShortName != "A Very Long " {
		t.Fatalf("defaults not applied: %+v", m)
	}
	if len(m.Icons) != 3 || m.Icons[0].Src != "/static/icon-192.png" || m.Icons[2].Purpose != "maskable" {
		t.Fatalf("icons: %+v", m.Icons)
	}
}

func TestHandlerRefusesIncompleteManifest(t *testing.T) {
	h := Handler(func(*http.Request) Manifest { return Manifest{Name: "x"} })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a manifest without icons must not be served, got %d", rec.Code)
	}
}
