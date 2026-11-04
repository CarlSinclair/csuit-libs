package secretbox

import (
	"errors"
	"strings"
	"testing"
)

func testRing(t *testing.T, id string, keys map[string]byte) *Keyring {
	t.Helper()
	m := map[string][]byte{}
	for k, b := range keys {
		m[k] = []byte(strings.Repeat(string(rune(b)), 32))
	}
	r, err := New(id, m)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoundTripAndLabelBinding(t *testing.T) {
	r := testRing(t, "a", map[string]byte{"a": 'x'})
	s, err := r.Seal("totp", "secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Open("totp", s)
	if err != nil || got != "secret" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := r.Open("apikey", s); err == nil {
		t.Fatal("opened under the wrong label")
	}
}

func TestRotation(t *testing.T) {
	old := testRing(t, "a", map[string]byte{"a": 'x'})
	s, _ := old.Seal("l", "v")
	r := testRing(t, "b", map[string]byte{"a": 'x', "b": 'y'})
	if !r.NeedsReseal(s) {
		t.Fatal("expected reseal")
	}
	out, changed, err := r.Reseal("l", s)
	if err != nil || !changed || !strings.HasPrefix(out, PrefixV2+"b:") {
		t.Fatalf("reseal: %q %v %v", out, changed, err)
	}
	if got, err := r.Open("l", out); err != nil || got != "v" {
		t.Fatalf("open after reseal: %q %v", got, err)
	}
}

func TestUnsealedRejectedByDefault(t *testing.T) {
	r := testRing(t, "a", map[string]byte{"a": 'x'})
	if _, err := r.Open("l", "plain"); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("want ErrNotSealed, got %v", err)
	}
	r.AllowPlaintext = true
	if got, err := r.Open("l", "plain"); err != nil || got != "plain" {
		t.Fatalf("plaintext migration: %q %v", got, err)
	}
}
