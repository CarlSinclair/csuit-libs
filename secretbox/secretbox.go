package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	PrefixV1 = "enc:v1:"
	PrefixV2 = "enc:v2:"
)

var (
	ErrNotSealed  = errors.New("secretbox: value is not sealed")
	ErrUnknownKey = errors.New("secretbox: unknown key id")
	ErrNoKey      = errors.New("secretbox: no key configured")
)

type Keyring struct {
	current string
	keys    map[string][]byte
	legacy  []byte
	// AllowPlaintext lets Open return unsealed values unchanged. It exists only for migrating old rows; turn it off once every row is sealed.
	AllowPlaintext bool
}

func ParseKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(key) != 32 {
		return nil, errors.New("secretbox: key must be a base64-encoded 32-byte value")
	}
	return key, nil
}

func New(currentID string, keys map[string][]byte) (*Keyring, error) {
	if currentID == "" || strings.Contains(currentID, ":") {
		return nil, errors.New("secretbox: invalid current key id")
	}
	if _, ok := keys[currentID]; !ok {
		return nil, fmt.Errorf("secretbox: current key id %q has no key: %w", currentID, ErrNoKey)
	}
	copied := make(map[string][]byte, len(keys))
	for id, k := range keys {
		if id == "" || strings.Contains(id, ":") {
			return nil, fmt.Errorf("secretbox: invalid key id %q", id)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("secretbox: key %q is not 32 bytes", id)
		}
		copied[id] = append([]byte(nil), k...)
	}
	return &Keyring{current: currentID, keys: copied}, nil
}

func (k *Keyring) SetLegacyV1Key(key []byte) error {
	if len(key) != 32 {
		return errors.New("secretbox: legacy key is not 32 bytes")
	}
	k.legacy = append([]byte(nil), key...)
	return nil
}

func (k *Keyring) CurrentID() string { return k.current }

func derive(master []byte, label string) ([]byte, error) {
	r := hkdf.New(sha256.New, master, nil, []byte("csuit-secretbox/v2/"+label))
	out := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("secretbox: key derivation: %w", err)
	}
	return out, nil
}

func aad(kid, label string) []byte { return []byte(kid + "|" + label) }

func (k *Keyring) Seal(label, plaintext string) (string, error) {
	master, ok := k.keys[k.current]
	if !ok {
		return "", ErrNoKey
	}
	dk, err := derive(master, label)
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(dk)
	if err != nil {
		return "", fmt.Errorf("secretbox: cipher: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), aad(k.current, label))
	return PrefixV2 + k.current + ":" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (k *Keyring) Open(label, stored string) (string, error) {
	switch {
	case strings.HasPrefix(stored, PrefixV2):
		return k.openV2(label, stored)
	case strings.HasPrefix(stored, PrefixV1):
		return k.openV1(stored)
	case k.AllowPlaintext:
		return stored, nil
	}
	return "", ErrNotSealed
}

func (k *Keyring) openV2(label, stored string) (string, error) {
	rest := strings.TrimPrefix(stored, PrefixV2)
	i := strings.IndexByte(rest, ':')
	if i <= 0 {
		return "", errors.New("secretbox: malformed v2 value")
	}
	kid := rest[:i]
	master, ok := k.keys[kid]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownKey, kid)
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest[i+1:])
	if err != nil {
		return "", fmt.Errorf("secretbox: decode: %w", err)
	}
	dk, err := derive(master, label)
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(dk)
	if err != nil {
		return "", fmt.Errorf("secretbox: cipher: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("secretbox: ciphertext too short")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], aad(kid, label))
	if err != nil {
		return "", fmt.Errorf("secretbox: open: %w", err)
	}
	return string(plain), nil
}

func (k *Keyring) openV1(stored string) (string, error) {
	key := k.legacy
	if key == nil {
		key = k.keys[k.current]
	}
	if key == nil {
		return "", ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, PrefixV1))
	if err != nil {
		return "", fmt.Errorf("secretbox: decode v1: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secretbox: v1 cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secretbox: v1 gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("secretbox: v1 ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("secretbox: v1 open: %w", err)
	}
	return string(plain), nil
}

func (k *Keyring) NeedsReseal(stored string) bool {
	return !strings.HasPrefix(stored, PrefixV2+k.current+":")
}

func (k *Keyring) Reseal(label, stored string) (string, bool, error) {
	if !k.NeedsReseal(stored) {
		return stored, false, nil
	}
	plain, err := k.Open(label, stored)
	if err != nil {
		return "", false, err
	}
	out, err := k.Seal(label, plain)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}
