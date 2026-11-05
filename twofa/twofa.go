// Package twofa generates and verifies TOTP secrets for the CSuit apps.
package twofa

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"

	"github.com/pquerna/otp/totp"
)

// NewSecret returns a fresh base32 TOTP secret for the account.
func NewSecret(issuer, account string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account})
	if err != nil {
		return "", err
	}
	return key.Secret(), nil
}

// QRDataURL renders the setup QR code for an existing base32 secret as a
// data:image/png URL. The secret is base32-decoded first so the code encodes
// the same secret that Verify checks against.
func QRDataURL(issuer, secret, account string) (string, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(secret, "="))
	if err != nil {
		return "", fmt.Errorf("failed to decode TOTP secret: %w", err)
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Secret: raw})
	if err != nil {
		return "", fmt.Errorf("failed to create TOTP key for QR: %w", err)
	}
	img, err := key.Image(200, 200)
	if err != nil {
		return "", fmt.Errorf("failed to generate QR code image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("failed to encode QR code image to PNG: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Verify checks a code against a plaintext base32 secret.
func Verify(secret, code string) bool { return totp.Validate(code, secret) }
