package authkit

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "0123456789abcdef0123456789abcdef"

func TestHashAndMagicToken(t *testing.T) {
	if HashToken("x") != HashToken("x") || HashToken("x") == HashToken("y") || len(HashToken("x")) != 64 {
		t.Error("HashToken wrong")
	}
	tok, exp, err := NewMagicToken(5 * time.Minute)
	if err != nil || len(tok) != 64 || time.Until(exp) > 5*time.Minute || time.Until(exp) < 4*time.Minute {
		t.Errorf("magic token: %q %v %v", tok, exp, err)
	}
}

func TestRecoveryCode(t *testing.T) {
	code, hash, err := NewRecoveryCode()
	if err != nil || len(code) != 17 || hash != HashToken(code) {
		t.Errorf("recovery code: %q %q %v", code, hash, err)
	}
}

func TestTwoFAPendingToken(t *testing.T) {
	tok, err := New2FAPendingToken(secret, 42, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := Parse2FAPendingToken(secret, tok)
	if err != nil || uid != 42 {
		t.Errorf("uid=%d err=%v", uid, err)
	}
	if _, err := Parse2FAPendingToken("another-secret-another-secret-0000", tok); err == nil {
		t.Error("wrong secret accepted")
	}
	other, _ := SignHS256(secret, jwt.MapClaims{"user_id": 1, "exp": time.Now().Add(time.Minute).Unix(), "scope": "access"})
	if _, err := Parse2FAPendingToken(secret, other); err == nil {
		t.Error("wrong scope accepted")
	}
	expired, _ := New2FAPendingToken(secret, 1, -time.Minute)
	if _, err := Parse2FAPendingToken(secret, expired); err == nil {
		t.Error("expired token accepted")
	}
}

func TestParseHS256PinsAlgorithm(t *testing.T) {
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{"a": 1}).SignedString([]byte(secret))
	if _, err := ParseHS256(secret, tok); err == nil {
		t.Error("HS512 accepted")
	}
}

func TestPayloadAndSecret(t *testing.T) {
	sig := SignPayload(secret, "body")
	if !VerifyPayload(secret, "body", sig) || VerifyPayload(secret, "bodyx", sig) {
		t.Error("payload signature wrong")
	}
	if CheckSecret("short") == nil || CheckSecret(secret) != nil {
		t.Error("CheckSecret wrong")
	}
}
