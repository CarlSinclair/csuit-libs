package apikit

import (
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
		{[]string{"*"}, "anything:write", true},
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
