package ap2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/CarlSinclair/csuit-libs/jcs"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// --- AP2 mandate signing and verification ---
//
// The host app is the merchant. It SIGNS one thing (the ap2.merchant_authorization
// on every checkout response) and VERIFIES two things a buyer's agent /
// wallet sends back on complete_checkout: the checkout mandate (an
// SD-JWT+kb proving a human approved this exact cart) and, when present,
// the payment mandate (bound to the checkout). Verification is deliberately
// strict - ES256 only, no alg:none / HMAC key-confusion, mandatory exp,
// bounded iat, single-use nonce, exact audience, holder key-binding, and a
// terms match against the server's own recomputed checkout.

// UCP / AP2 error codes, surfaced verbatim (the Error string IS the code).
var (
	ErrMandateRequired              = errors.New("mandate_required")
	ErrMandateInvalidSignature      = errors.New("mandate_invalid_signature")
	ErrMandateExpired               = errors.New("mandate_expired")
	ErrMandateScopeMismatch         = errors.New("mandate_scope_mismatch")
	ErrMerchantAuthorizationInvalid = errors.New("merchant_authorization_invalid")
	ErrAgentMissingKey              = errors.New("agent_missing_key")
	ErrMandateMalformed             = errors.New("mandate_malformed")
)

const (
	ap2MaxDisclosures    = 64
	ap2MaxDisclosuresize = 4096
	ap2IatSkew           = 15 * time.Minute
)

var jwtCompactRe = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+$`)

// --- ES256 primitives -------------------------------------------------------

func es256Sign(signingInput []byte, priv *ecdsa.PrivateKey) (string, error) {
	h := sha256.Sum256(signingInput)
	r, s, err := ecdsa.Sign(rand.Reader, priv, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

func es256Verify(signingInput []byte, sigB64 string, pub *ecdsa.PublicKey) bool {
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || len(sig) != 64 {
		return false
	}
	h := sha256.Sum256(signingInput)
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, h[:], r, s)
}

func b64uJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- merchant_authorization (we sign) -------------------------------------

// MerchantAuthorizationJWS returns the detached-JWS ap2.merchant_authorization
// for a checkout object: "<b64url header>..<b64url signature>", the JCS
// canonical form of the checkout WITH ITS ap2 FIELD REMOVED transmitted
// separately as the response body itself (RFC 7515 Appendix F).
func MerchantAuthorizationJWS(checkout map[string]interface{}) (string, error) {
	kid, priv, err := ActiveAP2SigningKey()
	if err != nil {
		return "", err
	}
	clone := make(map[string]interface{}, len(checkout))
	for k, v := range checkout {
		if k == "ap2" {
			continue
		}
		clone[k] = v
	}
	payload, err := jcs.Marshal(clone)
	if err != nil {
		return "", err
	}
	headerB64, err := b64uJSON(map[string]string{"alg": "ES256", "kid": kid, "typ": "JWS"})
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig, err := es256Sign([]byte(headerB64+"."+payloadB64), priv)
	if err != nil {
		return "", err
	}
	return headerB64 + ".." + sig, nil
}

// VerifyMerchantAuthorizationJWS checks a detached merchant_authorization
// against this tenant's own JWKS over the JCS form of the given checkout
// (ap2 field excluded). Used to re-check the authorization a checkout
// mandate embeds, and by tools/ap2verify.
func VerifyMerchantAuthorizationJWS(checkout map[string]interface{}, detached string) error {
	parts := strings.Split(strings.TrimSpace(detached), ".")
	if len(parts) != 3 || parts[1] != "" {
		return ErrMerchantAuthorizationInvalid
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrMerchantAuthorizationInvalid
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil || hdr.Alg != "ES256" {
		return ErrMerchantAuthorizationInvalid
	}
	pub, ok := AP2VerificationKey(hdr.Kid)
	if !ok {
		return ErrMerchantAuthorizationInvalid
	}
	clone := make(map[string]interface{}, len(checkout))
	for k, v := range checkout {
		if k == "ap2" {
			continue
		}
		clone[k] = v
	}
	payload, err := jcs.Marshal(clone)
	if err != nil {
		return ErrMerchantAuthorizationInvalid
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	if !es256Verify([]byte(parts[0]+"."+payloadB64), parts[2], pub) {
		return ErrMerchantAuthorizationInvalid
	}
	return nil
}

// --- checkout / payment mandate (agent signs, we verify) ------------------

type MandateVerifyOpts struct {
	ExpectedAudience string
	PlatformKeys     []map[string]interface{}
	AllowUnpinnedKey bool
	Now              time.Time
	NonceSeen        func(nonce string) (bool, error)
	RememberNonce    func(nonce string) error
}

type MandateResult struct {
	Claims    map[string]interface{}
	IssuerJWT string
	HolderJWK map[string]interface{}
	KeySource string
}

type compactJWS struct {
	headerB64  string
	payloadB64 string
	sigB64     string
	header     map[string]interface{}
	payload    map[string]interface{}
}

func parseCompactJWS(token string) (*compactJWS, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, ErrMandateMalformed
	}
	hRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMandateMalformed
	}
	pRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMandateMalformed
	}
	j := &compactJWS{headerB64: parts[0], payloadB64: parts[1], sigB64: parts[2]}
	if err := json.Unmarshal(hRaw, &j.header); err != nil {
		return nil, ErrMandateMalformed
	}
	if err := json.Unmarshal(pRaw, &j.payload); err != nil {
		return nil, ErrMandateMalformed
	}
	return j, nil
}

func (j *compactJWS) alg() string {
	s, _ := j.header["alg"].(string)
	return s
}

func (j *compactJWS) signingInput() []byte { return []byte(j.headerB64 + "." + j.payloadB64) }

func ecdsaPubFromJWK(jwk map[string]interface{}) (*ecdsa.PublicKey, error) {
	if kty, _ := jwk["kty"].(string); kty != "EC" {
		return nil, fmt.Errorf("jwk kty %v unsupported", jwk["kty"])
	}
	if crv, _ := jwk["crv"].(string); crv != "P-256" {
		return nil, fmt.Errorf("jwk crv %v unsupported", jwk["crv"])
	}
	xs, _ := jwk["x"].(string)
	ys, _ := jwk["y"].(string)
	xb, err := base64.RawURLEncoding.DecodeString(xs)
	if err != nil {
		return nil, fmt.Errorf("jwk x: %w", err)
	}
	yb, err := base64.RawURLEncoding.DecodeString(ys)
	if err != nil {
		return nil, fmt.Errorf("jwk y: %w", err)
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}, nil
}

// didKeyToECDSA decodes a did:key that encodes a P-256 public key
// (multicodec 0x1200, multibase base58btc "z"). Any other curve is refused.
func didKeyToECDSA(did string) (*ecdsa.PublicKey, error) {
	did = strings.TrimSpace(did)
	if !strings.HasPrefix(did, "did:key:z") {
		return nil, fmt.Errorf("not a did:key")
	}
	raw, err := base58Decode(strings.TrimPrefix(did, "did:key:z"))
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 || raw[0] != 0x80 || raw[1] != 0x24 {
		return nil, fmt.Errorf("did:key is not P-256 (multicodec)")
	}
	pt := raw[2:]
	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), pt)
	if x == nil {
		if len(pt) == 65 {
			x, y = elliptic.Unmarshal(elliptic.P256(), pt)
		}
	}
	if x == nil {
		return nil, fmt.Errorf("did:key point decode failed")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

func resolveIssuerKey(j *compactJWS, opts MandateVerifyOpts) (*ecdsa.PublicKey, string, error) {
	if kid, _ := j.header["kid"].(string); kid != "" {
		for _, k := range opts.PlatformKeys {
			if kk, _ := k["kid"].(string); kk == kid {
				pub, err := ecdsaPubFromJWK(k)
				if err != nil {
					return nil, "", ErrAgentMissingKey
				}
				return pub, "platform_profile", nil
			}
		}
	}
	if iss, _ := j.payload["iss"].(string); strings.HasPrefix(iss, "did:key:z") {
		if pub, err := didKeyToECDSA(iss); err == nil {
			return pub, "did:key", nil
		}
	}
	if kid, _ := j.header["kid"].(string); strings.HasPrefix(kid, "did:key:z") {
		if pub, err := didKeyToECDSA(kid); err == nil {
			return pub, "did:key", nil
		}
	}
	if opts.AllowUnpinnedKey {
		if jwk, ok := j.header["jwk"].(map[string]interface{}); ok {
			if pub, err := ecdsaPubFromJWK(jwk); err == nil {
				return pub, "embedded_jwk_unpinned", nil
			}
		}
	}
	return nil, "", ErrAgentMissingKey
}

func mandateExp(payload map[string]interface{}) (time.Time, bool) {
	switch v := payload["exp"].(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return time.Unix(i, 0), true
		}
	}
	return time.Time{}, false
}

func mandateNum(payload map[string]interface{}, key string) (int64, bool) {
	switch v := payload[key].(type) {
	case float64:
		return int64(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}
	}
	return 0, false
}

// VerifyCheckoutMandate verifies an SD-JWT+kb checkout mandate. It does NOT
// do the AP2 terms match against the live cart - the caller does that with
// the returned Claims, because only the caller has the recomputed checkout.
func VerifyCheckoutMandate(sdjwt string, opts MandateVerifyOpts) (*MandateResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	segs := strings.Split(strings.TrimSpace(sdjwt), "~")
	if len(segs) < 2 {
		return nil, ErrMandateMalformed
	}
	issuerToken := segs[0]
	kbToken := segs[len(segs)-1]
	if strings.TrimSpace(kbToken) == "" {
		return nil, ErrMandateMalformed
	}
	disclosures := segs[1 : len(segs)-1]
	if len(disclosures) > ap2MaxDisclosures {
		return nil, ErrMandateMalformed
	}

	issuer, err := parseCompactJWS(issuerToken)
	if err != nil {
		return nil, err
	}
	if issuer.alg() != "ES256" {
		return nil, ErrMandateInvalidSignature
	}
	issuerKey, keySource, err := resolveIssuerKey(issuer, opts)
	if err != nil {
		return nil, err
	}
	if !es256Verify(issuer.signingInput(), issuer.sigB64, issuerKey) {
		return nil, ErrMandateInvalidSignature
	}

	exp, ok := mandateExp(issuer.payload)
	if !ok {
		return nil, ErrMandateMalformed
	}
	if now.After(exp) {
		return nil, ErrMandateExpired
	}
	if iat, ok := mandateNum(issuer.payload, "iat"); ok {
		if time.Unix(iat, 0).After(now.Add(ap2IatSkew)) {
			return nil, ErrMandateScopeMismatch
		}
	}
	if sdAlg, ok := issuer.payload["_sd_alg"].(string); ok && sdAlg != "" && !strings.EqualFold(sdAlg, "sha-256") {
		return nil, ErrMandateMalformed
	}

	sdSet := collectSDDigests(issuer.payload)
	disclosed := map[string]interface{}{}
	for _, d := range disclosures {
		if len(d) > ap2MaxDisclosuresize {
			return nil, ErrMandateMalformed
		}
		digest := sha256.Sum256([]byte(d))
		if _, present := sdSet[base64.RawURLEncoding.EncodeToString(digest[:])]; !present {
			return nil, ErrMandateScopeMismatch
		}
		raw, derr := base64.RawURLEncoding.DecodeString(d)
		if derr != nil {
			return nil, ErrMandateMalformed
		}
		var arr []interface{}
		if jerr := json.Unmarshal(raw, &arr); jerr != nil {
			return nil, ErrMandateMalformed
		}
		if len(arr) == 3 {
			if name, ok := arr[1].(string); ok {
				disclosed[name] = arr[2]
			}
		}
	}

	// Key binding JWT.
	kb, err := parseCompactJWS(kbToken)
	if err != nil {
		return nil, err
	}
	if kb.alg() != "ES256" {
		return nil, ErrMandateInvalidSignature
	}
	if typ, _ := kb.header["typ"].(string); typ != "kb+jwt" {
		return nil, ErrMandateMalformed
	}
	cnf, _ := issuer.payload["cnf"].(map[string]interface{})
	holderJWK, _ := cnf["jwk"].(map[string]interface{})
	if holderJWK == nil {
		return nil, ErrAgentMissingKey
	}
	holderKey, herr := ecdsaPubFromJWK(holderJWK)
	if herr != nil {
		return nil, ErrAgentMissingKey
	}
	if !es256Verify(kb.signingInput(), kb.sigB64, holderKey) {
		return nil, ErrMandateInvalidSignature
	}
	if aud, _ := kb.payload["aud"].(string); opts.ExpectedAudience != "" && aud != opts.ExpectedAudience {
		return nil, ErrMandateScopeMismatch
	}
	if kiat, ok := mandateNum(kb.payload, "iat"); ok {
		t := time.Unix(kiat, 0)
		if t.After(now.Add(ap2IatSkew)) || t.Before(now.Add(-ap2IatSkew)) {
			return nil, ErrMandateScopeMismatch
		}
	} else {
		return nil, ErrMandateMalformed
	}
	nonce, _ := kb.payload["nonce"].(string)
	if strings.TrimSpace(nonce) == "" {
		return nil, ErrMandateMalformed
	}
	if opts.NonceSeen != nil {
		seen, nerr := opts.NonceSeen(nonce)
		if nerr != nil {
			return nil, nerr
		}
		if seen {
			return nil, ErrMandateScopeMismatch
		}
	}
	sdHashInput := issuerToken + "~"
	for _, d := range disclosures {
		sdHashInput += d + "~"
	}
	want := sha256.Sum256([]byte(sdHashInput))
	if got, _ := kb.payload["sd_hash"].(string); got != base64.RawURLEncoding.EncodeToString(want[:]) {
		return nil, ErrMandateScopeMismatch
	}
	if opts.RememberNonce != nil {
		if rerr := opts.RememberNonce(nonce); rerr != nil {
			logger.Warn("AP2 mandate: nonce remember failed nonce=%s err=%v", nonce, rerr)
		}
	}

	claims := map[string]interface{}{}
	for k, v := range issuer.payload {
		switch k {
		case "_sd", "_sd_alg", "cnf":
			continue
		}
		claims[k] = v
	}
	for k, v := range disclosed {
		claims[k] = v
	}

	logger.Warn("AP2 checkout mandate verified key_source=%s aud=%q disclosures=%d", keySource, opts.ExpectedAudience, len(disclosures))
	return &MandateResult{Claims: claims, IssuerJWT: issuerToken, HolderJWK: holderJWK, KeySource: keySource}, nil
}

func collectSDDigests(payload map[string]interface{}) map[string]struct{} {
	out := map[string]struct{}{}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			if sd, ok := t["_sd"].([]interface{}); ok {
				for _, d := range sd {
					if s, ok := d.(string); ok {
						out[s] = struct{}{}
					}
				}
			}
			for k, cv := range t {
				if k == "_sd" {
					continue
				}
				walk(cv)
			}
		case []interface{}:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(payload)
	return out
}

// LooksLikeMandateJWT reports whether a payment credential token is itself a
// (SD-)JWT that must be verified, as opposed to an opaque gateway token
// that is simply forwarded to the charger.
func LooksLikeMandateJWT(token string) bool {
	token = strings.TrimSpace(token)
	base := token
	if i := strings.Index(token, "~"); i >= 0 {
		base = token[:i]
	}
	return jwtCompactRe.MatchString(base)
}

// VerifyPaymentMandate verifies a payment mandate token and that it is bound
// to the given checkout JWT (sha-256 of the compact checkout JWT), per the
// UCP AP2 Mandates Extension. A token that is not a JWT is treated as an
// opaque gateway token by the caller and never reaches here.
func VerifyPaymentMandate(token, checkoutJWT string, opts MandateVerifyOpts) error {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	issuerToken := token
	if i := strings.Index(token, "~"); i >= 0 {
		issuerToken = token[:i]
	}
	j, err := parseCompactJWS(issuerToken)
	if err != nil {
		return err
	}
	if j.alg() != "ES256" {
		return ErrMandateInvalidSignature
	}
	key, _, err := resolveIssuerKey(j, opts)
	if err != nil {
		return err
	}
	if !es256Verify(j.signingInput(), j.sigB64, key) {
		return ErrMandateInvalidSignature
	}
	if exp, ok := mandateExp(j.payload); ok && now.After(exp) {
		return ErrMandateExpired
	}
	want := sha256.Sum256([]byte(checkoutJWT))
	wantB64 := base64.RawURLEncoding.EncodeToString(want[:])
	for _, k := range []string{"checkout_hash", "checkout_jwt_hash", "sd_hash", "cart_hash"} {
		if got, _ := j.payload[k].(string); got != "" {
			if got == wantB64 {
				return nil
			}
			return ErrMandateScopeMismatch
		}
	}
	// No explicit binding claim present - accept only when the caller did
	// not require binding (checkoutJWT empty).
	if strings.TrimSpace(checkoutJWT) == "" {
		return nil
	}
	return ErrMandateScopeMismatch
}
