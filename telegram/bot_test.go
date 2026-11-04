package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signed(token string, fields map[string]string) url.Values {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fields[k])
	}
	secret := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(strings.Join(parts, "\n")))
	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v
}

func TestVerifyLogin(t *testing.T) {
	b := New("123456:ABC-token", "tvt_bot", "s3cret")
	fresh := strconv.FormatInt(time.Now().Unix(), 10)
	v := signed(b.Token, map[string]string{"id": "42", "first_name": "Ann", "username": "ann", "auth_date": fresh})
	got, err := b.VerifyLogin(v)
	if err != nil || got.ID != 42 || got.Username != "ann" {
		t.Fatalf("valid payload rejected: %v %+v", err, got)
	}
	if _, err := b.VerifyLogin(v); err == nil {
		t.Fatalf("replay of the same payload was accepted")
	}

	tampered := signed(b.Token, map[string]string{"id": "42", "auth_date": strconv.FormatInt(time.Now().Unix()+1, 10)})
	tampered.Set("id", "43")
	if _, err := b.VerifyLogin(tampered); err == nil {
		t.Fatalf("tampered payload accepted")
	}

	old := signed(b.Token, map[string]string{"id": "42", "auth_date": strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)})
	if _, err := b.VerifyLogin(old); err == nil {
		t.Fatalf("stale payload accepted")
	}

	wrongKey := signed("999:other", map[string]string{"id": "42", "auth_date": fresh + ""})
	if _, err := b.VerifyLogin(wrongKey); err == nil {
		t.Fatalf("payload signed with another token accepted")
	}
}

func TestParseCommand(t *testing.T) {
	cases := map[string][2]string{
		"/start abc":       {"/start", "abc"},
		"/start@TVT_bot x": {"/start", "x"},
		"/START":           {"/start", ""},
		"hello":            {"", ""},
		"":                 {"", ""},
	}
	for in, want := range cases {
		c, a := ParseCommand(in)
		if c != want[0] || a != want[1] {
			t.Errorf("ParseCommand(%q) = %q,%q want %q,%q", in, c, a, want[0], want[1])
		}
	}
}
