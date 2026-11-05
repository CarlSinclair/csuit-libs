package twofa

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSecretQRAndVerify(t *testing.T) {
	secret, err := NewSecret("Test", "a@b.co")
	if err != nil || secret == "" {
		t.Fatalf("secret: %q %v", secret, err)
	}
	url, err := QRDataURL("Test", secret, "a@b.co")
	if err != nil || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("qr: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(secret, code) || Verify(secret, "000000") && code != "000000" {
		t.Error("verify wrong")
	}
	if _, err := QRDataURL("Test", "not base32!!", "a@b.co"); err == nil {
		t.Error("bad secret should fail")
	}
}

func TestVerifyStep(t *testing.T) {
	secret, err := NewSecret("Test", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := VerifyStep(secret, " "+code[:3]+" "+code[3:], now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("current code rejected or wrong step: %d %v", step, ok)
	}
	prev, _ := totp.GenerateCode(secret, now.Add(-30*time.Second))
	if s2, ok := VerifyStep(secret, prev, now); !ok || s2 != now.Unix()/30-1 {
		t.Fatalf("previous-window code must match its own step: %d %v", s2, ok)
	}
	if _, ok := VerifyStep(secret, "000000", now); ok && code != "000000" && prev != "000000" {
		t.Fatal("wrong code accepted")
	}
	old, _ := totp.GenerateCode(secret, now.Add(-5*time.Minute))
	if _, ok := VerifyStep(secret, old, now); ok && old != code && old != prev {
		t.Fatal("an old code must not be accepted")
	}
}
