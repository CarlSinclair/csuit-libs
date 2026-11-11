package clienterr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageOnlyDropsQueryAndFragment(t *testing.T) {
	got := pageOnly("example.com/booking/manage?token=secret&x=1#frag")
	if got != "example.com/booking/manage" {
		t.Fatalf("got %q", got)
	}
	if got := pageOnly("https://a.test/p?q=1"); got != "a.test/p" {
		t.Fatalf("got %q", got)
	}
}

func TestHandlerAcceptsReportAndRejectsGet(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, ReportPath, strings.NewReader(`{"context":"c","message":"m","url":"a.test/p?token=x"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("post code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, ReportPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, ReportPath, strings.NewReader(`not json`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("bad body code=%d", rec.Code)
	}
}

func TestScriptHandlerServesJS(t *testing.T) {
	rec := httptest.NewRecorder()
	ScriptHandler()(rec, httptest.NewRequest(http.MethodGet, ScriptPath, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || !strings.Contains(rec.Body.String(), "reportClientError") {
		t.Fatalf("code=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
