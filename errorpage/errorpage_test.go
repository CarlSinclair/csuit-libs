package errorpage

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlushToRealDoesNotDuplicateHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	b := &BufRW{W: rec}
	b.Header().Set("Location", "/api/auth")
	b.Header().Add("Set-Cookie", "a=1")
	b.WriteHeader(http.StatusSeeOther)
	b.FlushToReal()
	if got := rec.Header().Values("Location"); len(got) != 1 {
		t.Fatalf("Location sent %d times: %v", len(got), got)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 1 {
		t.Fatalf("Set-Cookie sent %d times: %v", len(got), got)
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestErrorPageRendersHTMLFor404(t *testing.T) {
	var gotCode interface{}
	c := Config{Render: func(w http.ResponseWriter, r *http.Request, d map[string]interface{}) { gotCode = d["Code"] }}
	h := c.ErrorPage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if gotCode != 404 {
		t.Fatalf("render code = %v", gotCode)
	}
}

func TestRecoverReturnsPlain500ForJSON(t *testing.T) {
	c := Config{Render: func(w http.ResponseWriter, r *http.Request, d map[string]interface{}) { t.Fatal("rendered") }}
	h := c.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("code = %d", rec.Code)
	}
}
