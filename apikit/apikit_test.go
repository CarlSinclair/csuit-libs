package apikit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScopesInclude(t *testing.T) {
	cases := []struct {
		granted []string
		want    string
		ok      bool
	}{
		{[]string{"products:read"}, "products:read", true},
		{[]string{"products:write"}, "products:read", true},
		{[]string{"products:read"}, "products:write", false},
		{[]string{"orders:read"}, "products:read", false},
		{[]string{"*"}, "anything:write", false},
		{[]string{"*"}, "orders:cancel", false},
		{[]string{"orders:cancel"}, "orders:read", false},
		{[]string{"ORDERS:READ "}, "orders:read", true},
		{nil, "products:read", false},
	}
	for _, c := range cases {
		if got := ScopesInclude(c.granted, c.want); got != c.ok {
			t.Errorf("ScopesInclude(%v,%q)=%v want %v", c.granted, c.want, got, c.ok)
		}
	}
}

func TestNormalizeScopes(t *testing.T) {
	out, err := NormalizeScopes([]string{" Products:Read ", "products:read", "orders:read"}, []string{"products:read", "orders:read"})
	if err != nil || len(out) != 2 {
		t.Fatalf("got %v err %v", out, err)
	}
	if _, err := NormalizeScopes([]string{"nope:read"}, []string{"products:read"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, err := NormalizeScopes(nil, []string{"products:read"}); err == nil {
		t.Fatal("empty scopes accepted")
	}
}

func TestTokenAndHash(t *testing.T) {
	raw, hash, display, err := NewToken("tst_")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "tst_") || HashToken(raw) != hash || !strings.HasPrefix(raw, display) {
		t.Fatalf("bad token %q hash %q display %q", raw, hash, display)
	}
	raw2, _, _, _ := NewToken("tst_")
	if raw == raw2 {
		t.Fatal("tokens not unique")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id, err := DecodeCursor(EncodeCursor(12345))
	if err != nil || id != 12345 {
		t.Fatalf("got %d err %v", id, err)
	}
	if _, err := DecodeCursor("!!!"); err == nil {
		t.Fatal("garbage cursor accepted")
	}
}

func TestSignVerify(t *testing.T) {
	body := []byte(`{"a":1}`)
	now := time.Now()
	sig := Sign("sec", now.Unix(), body)
	if !VerifySignature("sec", sig, now.Unix(), body, 5*time.Minute, now) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature("other", sig, now.Unix(), body, 5*time.Minute, now) {
		t.Fatal("wrong secret accepted")
	}
	if VerifySignature("sec", sig, now.Add(-time.Hour).Unix(), body, 5*time.Minute, now) {
		t.Fatal("stale timestamp accepted")
	}
}

func TestValidateWebhookURL(t *testing.T) {
	bad := []string{"", "http://example.com/x", "https://localhost/x", "https://127.0.0.1/x", "https://10.0.0.5/x",
		"https://169.254.169.254/x", "https://user:pw@example.com/x", "https://svc.internal/x", "https://100.64.1.1/x", "ftp://example.com"}
	for _, u := range bad {
		if ValidateWebhookURL(u) == nil {
			t.Errorf("accepted %q", u)
		}
	}
	if err := ValidateWebhookURL("https://hooks.example.com/in"); err != nil {
		t.Errorf("rejected valid url: %v", err)
	}
}

func TestParseDBTime(t *testing.T) {
	for _, v := range []any{"2026-01-02 03:04:05", []byte("2026-01-02 03:04:05"), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} {
		got, err := parseDBTime(v)
		if err != nil || got == nil || got.Year() != 2026 {
			t.Errorf("parseDBTime(%T)=%v err %v", v, got, err)
		}
	}
	if got, _ := parseDBTime(nil); got != nil {
		t.Error("nil not nil")
	}
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "0.1.2.3", "198.18.0.1", "240.0.0.1", "::1", "fc00::1", "fe80::1", "64:ff9b::7f00:1", "2002:7f00:1::1", "::ffff:127.0.0.1"}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil || !blockedIP(ip) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestValidateWebhookURLRejectsTricks(t *testing.T) {
	for _, u := range []string{
		"http://example.com/h", "https://user:pw@example.com/h", "https://localhost/h", "https://a.localhost/h",
		"https://x.internal/h", "https://x.local/h", "https://127.0.0.1/h", "https://[::1]/h", "https://169.254.169.254/",
		"https://example.com/h#frag", "", "ftp://example.com", "https:///nohost",
	} {
		if ValidateWebhookURL(u) == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if err := ValidateWebhookURL("https://example.com/hook"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
}

func TestSafeClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	client := NewSafeClient(nil)
	if _, err := client.Get(srv.URL); err == nil {
		t.Error("safe client connected to a loopback server")
	}
}
