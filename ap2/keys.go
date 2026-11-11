package ap2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// --- Per-tenant AP2 signing keys ---
//
// Every UCP checkout response this store returns carries an
// ap2.merchant_authorization: a detached JWS (ES256) over the JCS-canonical
// checkout, proving to the buyer's agent that this exact cart/price came
// from this merchant. That needs a stable private key per tenant.
//
// Keys are generated on first use, kept as a JSON array in a single
// business_config row ("ap2_signing_keys", listed in
// secretStoreConfigKeys so the whole blob - private scalars included - is
// AES-256-GCM encrypted at rest by SetStoreConfig). Only the public half
// is ever exposed: /.well-known/jwks.json and the discovery profile's
// signing_keys. Rotation mints a new active key and keeps the previous
// one verifiable for an overlap window so mandates already signed against
// it still check out.

const (
	ap2KeysConfigKey      = "ap2_signing_keys"
	ap2PublicSummaryKey   = "ap2_signing_keys_public"
	ap2RotateRequestedKey = "ap2_rotate_requested"
	ap2KeyRotationWindow  = 72 * time.Hour
	ap2KeyAlg             = "ES256"
	ap2KeyCurve           = "P-256"
)

var ap2KeysMu sync.Mutex

type ap2Key struct {
	Kid       string     `json:"kid"`
	Alg       string     `json:"alg"`
	Crv       string     `json:"crv"`
	X         string     `json:"x"`
	Y         string     `json:"y"`
	D         string     `json:"d,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

func (k ap2Key) verifiable(now time.Time) bool {
	return k.RetiredAt == nil || now.Before(k.RetiredAt.Add(ap2KeyRotationWindow))
}

func (k ap2Key) publicJWK() map[string]interface{} {
	return map[string]interface{}{
		"kty": "EC",
		"crv": k.Crv,
		"x":   k.X,
		"y":   k.Y,
		"kid": k.Kid,
		"use": "sig",
		"alg": k.Alg,
	}
}

func (k ap2Key) toECDSAPublic() (*ecdsa.PublicKey, error) {
	xb, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("ap2 key %s: bad x: %w", k.Kid, err)
	}
	yb, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("ap2 key %s: bad y: %w", k.Kid, err)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}, nil
}

func (k ap2Key) toECDSAPrivate() (*ecdsa.PrivateKey, error) {
	pub, err := k.toECDSAPublic()
	if err != nil {
		return nil, err
	}
	db, err := base64.RawURLEncoding.DecodeString(k.D)
	if err != nil {
		return nil, fmt.Errorf("ap2 key %s: bad d: %w", k.Kid, err)
	}
	return &ecdsa.PrivateKey{PublicKey: *pub, D: new(big.Int).SetBytes(db)}, nil
}

func loadAP2Keys() ([]ap2Key, error) {
	raw := strings.TrimSpace(storeGet(ap2KeysConfigKey))
	if raw == "" {
		return nil, nil
	}
	var keys []ap2Key
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("ap2 keys: corrupt config value: %w", err)
	}
	return keys, nil
}

func saveAP2Keys(keys []ap2Key) error {
	b, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := storeSet(ap2KeysConfigKey, string(b)); err != nil {
		return err
	}
	// A plaintext, public-only mirror so the controller can show key
	// status (kid / created / active) by a plain business_config read,
	// with no access to this tenant's encryption key. Never contains d.
	if pb, perr := json.Marshal(ap2SummariesFrom(keys)); perr == nil {
		if serr := storeSet(ap2PublicSummaryKey, string(pb)); serr != nil {
			logger.Error("AP2_KEY public summary write failed err=%v", serr)
		}
	}
	return nil
}

func ap2SummariesFrom(keys []ap2Key) []AP2KeySummary {
	var newestActive time.Time
	for _, k := range keys {
		if k.RetiredAt == nil && k.CreatedAt.After(newestActive) {
			newestActive = k.CreatedAt
		}
	}
	out := make([]AP2KeySummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, AP2KeySummary{
			Kid:       k.Kid,
			CreatedAt: k.CreatedAt,
			RetiredAt: k.RetiredAt,
			Active:    k.RetiredAt == nil && k.CreatedAt.Equal(newestActive),
		})
	}
	return out
}

// rotateKeysLocked retires every currently-active key and appends a fresh
// one. Caller must hold ap2KeysMu. Returns the new key list and new kid.
func rotateKeysLocked(keys []ap2Key) ([]ap2Key, string, error) {
	now := time.Now().UTC()
	for i := range keys {
		if keys[i].RetiredAt == nil {
			retired := now
			keys[i].RetiredAt = &retired
		}
	}
	nk, err := mintAP2Key()
	if err != nil {
		return keys, "", err
	}
	return append(keys, nk), nk.Kid, nil
}

func mintAP2Key() (ap2Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return ap2Key{}, err
	}
	x := base64.RawURLEncoding.EncodeToString(fixedLen(priv.X.Bytes(), 32))
	y := base64.RawURLEncoding.EncodeToString(fixedLen(priv.Y.Bytes(), 32))
	d := base64.RawURLEncoding.EncodeToString(fixedLen(priv.D.Bytes(), 32))
	return ap2Key{
		Kid:       jwkThumbprintEC(x, y),
		Alg:       ap2KeyAlg,
		Crv:       ap2KeyCurve,
		X:         x,
		Y:         y,
		D:         d,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func fixedLen(b []byte, n int) []byte {
	if len(b) == n {
		return b
	}
	out := make([]byte, n)
	if len(b) > n {
		copy(out, b[len(b)-n:])
		return out
	}
	copy(out[n-len(b):], b)
	return out
}

// jwkThumbprintEC is the RFC 7638 thumbprint of an EC P-256 public JWK,
// used as the stable key id.
func jwkThumbprintEC(x, y string) string {
	canon := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":%q,"y":%q}`, x, y)
	sum := sha256.Sum256([]byte(canon))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ActiveAP2SigningKey returns the current signing key, generating the very
// first one on demand. The returned kid is what goes in the JWS header.
func ActiveAP2SigningKey() (kid string, priv *ecdsa.PrivateKey, err error) {
	ap2KeysMu.Lock()
	defer ap2KeysMu.Unlock()

	keys, err := loadAP2Keys()
	if err != nil {
		return "", nil, err
	}
	findActive := func() *ap2Key {
		var a *ap2Key
		for i := range keys {
			if keys[i].RetiredAt == nil {
				if a == nil || keys[i].CreatedAt.After(a.CreatedAt) {
					a = &keys[i]
				}
			}
		}
		return a
	}
	active := findActive()

	// Controller-requested rotation: the controller writes an RFC3339
	// timestamp to ap2_rotate_requested in this tenant's business_config.
	// If it is newer than the active key, rotate here (the controller has
	// no access to this tenant's encryption key, so it cannot mint one).
	if req := strings.TrimSpace(storeGet(ap2RotateRequestedKey)); req != "" {
		if reqAt, perr := time.Parse(time.RFC3339, req); perr == nil {
			if active == nil || reqAt.After(active.CreatedAt) {
				rotated, newKid, rErr := rotateKeysLocked(keys)
				if rErr != nil {
					return "", nil, rErr
				}
				keys = rotated
				if sErr := saveAP2Keys(keys); sErr != nil {
					return "", nil, sErr
				}
				if cErr := storeSet(ap2RotateRequestedKey, ""); cErr != nil {
					logger.Error("AP2_KEY could not clear rotate request after rotation new_kid=%s: %v", newKid, cErr)
				}
				logger.Warn("AP2_KEY rotated on controller request new_kid=%s", newKid)
				active = findActive()
			}
		}
	}

	if active == nil {
		nk, mErr := mintAP2Key()
		if mErr != nil {
			return "", nil, mErr
		}
		keys = append(keys, nk)
		if sErr := saveAP2Keys(keys); sErr != nil {
			return "", nil, sErr
		}
		logger.Warn("AP2_KEY minted first signing key kid=%s", nk.Kid)
		active = findActive()
	}
	pk, pErr := active.toECDSAPrivate()
	if pErr != nil {
		return "", nil, pErr
	}
	return active.Kid, pk, nil
}

// AP2VerificationKey resolves one of this tenant's own public keys by kid,
// including a recently-retired one still inside the rotation overlap. Used
// to check a merchant_authorization this store produced.
func AP2VerificationKey(kid string) (*ecdsa.PublicKey, bool) {
	kid = strings.TrimSpace(kid)
	if kid == "" {
		return nil, false
	}
	keys, err := loadAP2Keys()
	if err != nil {
		logger.Error("AP2_KEY verification key load failed kid=%s err=%v", kid, err)
		return nil, false
	}
	now := time.Now().UTC()
	for _, k := range keys {
		if k.Kid == kid && k.verifiable(now) {
			pub, perr := k.toECDSAPublic()
			if perr != nil {
				logger.Error("AP2_KEY parse failed kid=%s err=%v", kid, perr)
				return nil, false
			}
			return pub, true
		}
	}
	return nil, false
}

// AP2PublicJWKS returns every currently-usable public key for JWKS output.
func AP2PublicJWKS() ([]map[string]interface{}, error) {
	keys, err := loadAP2Keys()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		if _, _, gErr := ActiveAP2SigningKey(); gErr != nil {
			return nil, gErr
		}
		keys, err = loadAP2Keys()
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	out := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		if k.verifiable(now) {
			out = append(out, k.publicJWK())
		}
	}
	return out, nil
}

// AP2KeySummary is the read-only view the controller dashboard shows.
type AP2KeySummary struct {
	Kid       string     `json:"kid"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	Active    bool       `json:"active"`
}

func AP2KeySummaries() ([]AP2KeySummary, error) {
	keys, err := loadAP2Keys()
	if err != nil {
		return nil, err
	}
	return ap2SummariesFrom(keys), nil
}

// RotateAP2SigningKey retires every currently-active key (keeping it
// verifiable for the overlap window) and mints a fresh active one.
func RotateAP2SigningKey() (newKid string, err error) {
	ap2KeysMu.Lock()
	defer ap2KeysMu.Unlock()

	keys, err := loadAP2Keys()
	if err != nil {
		return "", err
	}
	rotated, newKid, err := rotateKeysLocked(keys)
	if err != nil {
		return "", err
	}
	if err := saveAP2Keys(rotated); err != nil {
		return "", err
	}
	if cErr := storeSet(ap2RotateRequestedKey, ""); cErr != nil {
		logger.Error("AP2_KEY could not clear rotate request after rotation new_kid=%s: %v", newKid, cErr)
	}
	logger.Warn("AP2_KEY rotated new_kid=%s total_keys=%d", newKid, len(rotated))
	return newKid, nil
}

var (
	storeGetFn func(key string) string
	storeSetFn func(key, value string) error
)

func SetStore(get func(key string) string, set func(key, value string) error) {
	storeGetFn = get
	storeSetFn = set
}

func storeGet(key string) string {
	if storeGetFn == nil {
		logger.Error("ap2: key store not configured, cannot read key=%s", key)
		return ""
	}
	return storeGetFn(key)
}

func storeSet(key, value string) error {
	if storeSetFn == nil {
		return fmt.Errorf("ap2: key store not configured, cannot write key=%s", key)
	}
	return storeSetFn(key, value)
}
