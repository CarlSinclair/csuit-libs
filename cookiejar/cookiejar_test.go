package cookiejar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Short: []string{"session_token", "mfa_pending", "csrf_token", "flash_message"},
		Long:  []string{"refresh_token", "lang"},
	}
}

func setCookies(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

func collect(dst *map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*dst = map[string]string{}
		for _, c := range r.Cookies() {
			(*dst)[c.Name] = c.Value
		}
	}
}

func TestFoldsLegacyCookiesIntoTwo(t *testing.T) {
	h := Middleware(testConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "aaa.bbb.ccc", Path: "/", MaxAge: 900, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh123", Path: "/", MaxAge: 604800, HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "lang", Value: "pt-br", Path: "/", MaxAge: 31536000})
		w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	got := setCookies(rec)
	if len(got) != 2 || got["__Host-session"] == nil || got["__Host-persistent"] == nil {
		t.Fatalf("want exactly session+prefs cookies, got %v", got)
	}
	if !got["__Host-session"].HttpOnly || !got["__Host-session"].Secure || !got["__Host-persistent"].HttpOnly {
		t.Fatalf("cookies must be HttpOnly and Secure")
	}
}

func TestRoundTripInjectsLegacyNames(t *testing.T) {
	var seen map[string]string
	mk := func(next http.HandlerFunc) http.Handler { return Middleware(testConfig())(next) }
	rec := httptest.NewRecorder()
	mk(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "tok1", Path: "/", MaxAge: 900})
		http.SetCookie(w, &http.Cookie{Name: "lang", Value: "es", Path: "/", MaxAge: 1000})
	}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	req.AddCookie(&http.Cookie{Name: "__cf_bm", Value: "cloudflare"})
	rec2 := httptest.NewRecorder()
	mk(collect(&seen)).ServeHTTP(rec2, req)
	if seen["session_token"] != "tok1" || seen["lang"] != "es" || seen["__cf_bm"] != "cloudflare" {
		t.Fatalf("handler did not see legacy names, got %v", seen)
	}
	if _, ok := seen["__Host-session"]; ok {
		t.Fatalf("composite cookie must not leak to the handler")
	}
	if len(rec2.Result().Cookies()) != 0 {
		t.Fatalf("unchanged state must not re-emit cookies, got %v", rec2.Result().Cookies())
	}
}

func TestDeleteRemovesKey(t *testing.T) {
	mk := func(next http.HandlerFunc) http.Handler { return Middleware(testConfig())(next) }
	rec := httptest.NewRecorder()
	mk(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "tok1", Path: "/", MaxAge: 900})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "ref1", Path: "/", MaxAge: 900})
	}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	req := httptest.NewRequest("GET", "/logout", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec2 := httptest.NewRecorder()
	mk(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Path: "/", MaxAge: -1})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Path: "/", MaxAge: -1})
	}).ServeHTTP(rec2, req)
	got := setCookies(rec2)
	if c := got["__Host-session"]; c == nil || c.MaxAge >= 0 {
		t.Fatalf("emptied session jar must be expired, got %v", c)
	}
}

func TestLegacyCookiesAreMigratedAndExpired(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "old"})
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	var seen map[string]string
	rec := httptest.NewRecorder()
	Middleware(testConfig())(collect(&seen)).ServeHTTP(rec, req)
	if seen["session_token"] != "old" || seen["lang"] != "en" {
		t.Fatalf("legacy cookies must still be visible to the handler, got %v", seen)
	}
	got := setCookies(rec)
	if got["__Host-session"] == nil || got["__Host-persistent"] == nil {
		t.Fatalf("legacy cookies must be folded into composites, got %v", got)
	}
	if c := got["session_token"]; c == nil || c.MaxAge >= 0 {
		t.Fatalf("legacy session_token must be expired, got %v", c)
	}
	if c := got["lang"]; c == nil || c.MaxAge >= 0 {
		t.Fatalf("legacy lang must be expired, got %v", c)
	}
}

func TestExpiredEntryIsIgnored(t *testing.T) {
	m := &middleware{cfg: testConfig(), long: map[string]bool{"lang": true}, known: map[string]bool{}}
	raw := encode(jar{"mfa_pending": {V: "x", E: time.Now().Add(-time.Minute).Unix()}, "session_token": {V: "ok"}})
	got := decode(raw, time.Now(), func(n string) bool { return !m.isLong(n) })
	if _, ok := got["mfa_pending"]; ok {
		t.Fatalf("expired entry must be dropped")
	}
	if got["session_token"].V != "ok" {
		t.Fatalf("live entry must survive")
	}
}

func TestTamperedCompositeIsIgnored(t *testing.T) {
	var seen map[string]string
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: "not-base64!!"})
	req.AddCookie(&http.Cookie{Name: "__Host-persistent", Value: encode(jar{"bad;name": {V: "x"}, "lang": {V: "a;b"}, "refresh_token": {V: "fine"}})})
	Middleware(testConfig())(collect(&seen)).ServeHTTP(httptest.NewRecorder(), req)
	if len(seen) != 1 || seen["refresh_token"] != "fine" {
		t.Fatalf("only the valid entry may survive, got %v", seen)
	}
}

func TestWrongJarKeysAreRejected(t *testing.T) {
	var seen map[string]string
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: encode(jar{"refresh_token": {V: "evil"}, "session_token": {V: "ok"}})})
	Middleware(testConfig())(collect(&seen)).ServeHTTP(httptest.NewRecorder(), req)
	if _, ok := seen["refresh_token"]; ok {
		t.Fatalf("a long-lived key inside the session cookie must be dropped")
	}
	if seen["session_token"] != "ok" {
		t.Fatalf("session key must survive")
	}
}

func TestCookiesWithOtherPathsPassThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	Middleware(testConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "scoped", Value: "v", Path: "/admin"})
		http.SetCookie(w, &http.Cookie{Name: "shared", Value: "v", Path: "/", Domain: "example.com"})
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	got := setCookies(rec)
	if got["scoped"] == nil || got["shared"] == nil {
		t.Fatalf("cookies with a custom Path or Domain must pass through untouched, got %v", got)
	}
}

func TestWriterKeepsOptionalInterfaces(t *testing.T) {
	var w http.ResponseWriter = &writer{ResponseWriter: httptest.NewRecorder()}
	if _, ok := w.(http.Flusher); !ok {
		t.Fatalf("writer must implement http.Flusher")
	}
	if _, ok := w.(http.Hijacker); !ok {
		t.Fatalf("writer must implement http.Hijacker")
	}
	if !strings.Contains(expired("x"), "Max-Age=0") {
		t.Fatalf("expired() must emit Max-Age=0")
	}
}

func TestIdenticalValueIsNotReEmitted(t *testing.T) {
	mk := func(next http.HandlerFunc) http.Handler { return Middleware(testConfig())(next) }
	setCSRF := func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: "same", Path: "/", MaxAge: 31536000})
	}
	rec := httptest.NewRecorder()
	mk(setCSRF).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec2 := httptest.NewRecorder()
	mk(setCSRF).ServeHTTP(rec2, req)
	if n := len(rec2.Result().Cookies()); n != 0 {
		t.Fatalf("re-setting an identical value must not emit cookies, got %d", n)
	}
}

func TestOnlyChangedCompositeIsEmitted(t *testing.T) {
	mk := func(next http.HandlerFunc) http.Handler { return Middleware(testConfig())(next) }
	rec := httptest.NewRecorder()
	mk(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "a1", Path: "/", MaxAge: 900})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "r1", Path: "/", MaxAge: 604800})
	}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec2 := httptest.NewRecorder()
	mk(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "a2", Path: "/", MaxAge: 900})
	}).ServeHTTP(rec2, req)
	got := setCookies(rec2)
	if got["__Host-session"] == nil {
		t.Fatalf("changed session composite must be emitted")
	}
	if got["__Host-persistent"] != nil {
		t.Fatalf("untouched persistent composite must not be re-emitted, it would overwrite a concurrent refresh-token rotation")
	}
}

func TestShortCookieIsBrowserSessionAndLongIsPersistent(t *testing.T) {
	rec := httptest.NewRecorder()
	Middleware(testConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "a1", Path: "/", MaxAge: 900})
		http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: "c1", Path: "/", MaxAge: 31536000})
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "r1", Path: "/", MaxAge: 604800})
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	got := setCookies(rec)
	if c := got["__Host-session"]; c == nil || c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("session composite must be a browser-session cookie, got %v", c)
	}
	if c := got["__Host-persistent"]; c == nil || c.MaxAge <= 0 {
		t.Fatalf("persistent composite must carry an expiry, got %v", c)
	}
}

func TestNoCookiesRoutesNeverSetCookies(t *testing.T) {
	cfg := testConfig()
	cfg.NoCookies = func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/widget/") }
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: "x", Path: "/", MaxAge: 100})
		http.SetCookie(w, &http.Cookie{Name: "stray", Value: "y", Path: "/other"})
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/widget/config", nil))
	if n := len(rec.Result().Cookies()); n != 0 {
		t.Fatalf("widget routes must never set cookies, got %d", n)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/dashboard", nil))
	if len(rec2.Result().Cookies()) == 0 {
		t.Fatalf("other routes must still get cookies")
	}
}
