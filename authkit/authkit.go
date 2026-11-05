// Package authkit holds the stateless auth helpers shared by the CSuit apps:
// one-time token generation and hashing, recovery codes, HS256 JWT signing and
// verification, the 2FA-pending token, and HMAC payload signatures. Anything
// that touches a database (refresh rotation, sessions) stays in each app.
package authkit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const MinSecretLen = 32

const scope2FAPending = "2fa_pending"

// CheckSecret reports whether secret is long enough to sign tokens with.
func CheckSecret(secret string) error {
	secret = strings.TrimSpace(secret)
	if len(secret) < MinSecretLen {
		return fmt.Errorf("JWT secret is missing or too short (%d chars, need >= %d)", len(secret), MinSecretLen)
	}
	return nil
}

// HashToken is the stored form of an emailed one-time token: its SHA-256 in
// hex. The link carries the raw token; the database only holds this.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewMagicToken returns 32 random bytes in hex and the time it expires. It
// retries a few times on a crypto/rand failure and returns an error rather
// than a predictable token.
func NewMagicToken(ttl time.Duration) (string, time.Time, error) {
	b := make([]byte, 32)
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err = rand.Read(b); err == nil {
			return hex.EncodeToString(b), time.Now().UTC().Add(ttl), nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", time.Time{}, fmt.Errorf("crypto/rand failed after retries: %w", err)
}

// NewRecoveryCode returns a code like "A1B2C3D4-E5F60718" and the hash to store.
func NewRecoveryCode() (code, hash string, err error) {
	b := make([]byte, 8)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	code = strings.ToUpper(hex.EncodeToString(b[0:4])) + "-" + strings.ToUpper(hex.EncodeToString(b[4:8]))
	return code, HashToken(code), nil
}

// SignHS256 signs claims with secret.
func SignHS256(secret string, claims jwt.MapClaims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseHS256 verifies a token signed with exactly HS256 and returns its claims.
func ParseHS256(secret, tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	return claims, nil
}

// UnverifiedClaims reads claims without checking the signature. Use it only to
// pick out ids from a token that is being discarded anyway (logout).
func UnverifiedClaims(tokenString string) (jwt.MapClaims, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	return claims, nil
}

// ClaimInt64 reads a numeric claim that may arrive as float64, int or a string.
func ClaimInt64(v any) (int64, error) {
	switch t := v.(type) {
	case float64:
		return int64(t), nil
	case int64:
		return t, nil
	case int:
		return int64(t), nil
	case json.Number:
		return t.Int64()
	case string:
		return json.Number(t).Int64()
	}
	return 0, fmt.Errorf("unexpected type %T", v)
}

// New2FAPendingToken is the short-lived token issued between password and
// second factor.
func New2FAPendingToken(secret string, userID int64, ttl time.Duration) (string, error) {
	now := time.Now()
	return SignHS256(secret, jwt.MapClaims{
		"user_id": userID,
		"exp":     now.Add(ttl).Unix(),
		"iat":     now.Unix(),
		"scope":   scope2FAPending,
	})
}

// Parse2FAPendingToken verifies the token and returns the user id.
func Parse2FAPendingToken(secret, tokenString string) (int64, error) {
	claims, err := ParseHS256(secret, tokenString)
	if err != nil {
		return 0, err
	}
	if scope, ok := claims["scope"].(string); !ok || scope != scope2FAPending {
		return 0, errors.New("invalid token scope")
	}
	uid, err := ClaimInt64(claims["user_id"])
	if err != nil {
		return 0, errors.New("invalid user_id")
	}
	return uid, nil
}

// SignPayload returns the hex HMAC-SHA256 of body under secret.
func SignPayload(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyPayload compares a hex signature with the expected one in constant time.
func VerifyPayload(secret, body, signature string) bool {
	return subtle.ConstantTimeCompare([]byte(SignPayload(secret, body)), []byte(signature)) == 1
}
