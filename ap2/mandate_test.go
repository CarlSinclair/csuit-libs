package ap2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func testPubJWK(pub *ecdsa.PublicKey) map[string]interface{} {
	return map[string]interface{}{
		"kty": "EC", "crv": "P-256",
		"x": base64.RawURLEncoding.EncodeToString(pub.X.FillBytes(make([]byte, 32))),
		"y": base64.RawURLEncoding.EncodeToString(pub.Y.FillBytes(make([]byte, 32))),
	}
}

func testSignJWT(t *testing.T, header, payload map[string]interface{}, key *ecdsa.PrivateKey) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	si := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sig, err := es256Sign([]byte(si), key)
	if err != nil {
		t.Fatalf("es256Sign: %v", err)
	}
	return si + "." + sig
}

type testMandateOpts struct {
	aud    string
	exp    time.Time
	nonce  string
	sdHash string // override
}

func buildTestMandate(t *testing.T, checkout map[string]interface{}, o testMandateOpts) (mandate string, issuerKey, holderKey *ecdsa.PrivateKey) {
	t.Helper()
	issuerKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	holderKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if o.exp.IsZero() {
		o.exp = time.Now().Add(10 * time.Minute)
	}
	if o.nonce == "" {
		o.nonce = "nonce-" + base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))
	}
	issuerJWT := testSignJWT(t,
		map[string]interface{}{"alg": "ES256", "typ": "JWT", "jwk": testPubJWK(&issuerKey.PublicKey)},
		map[string]interface{}{
			"iss": "https://platform.test", "iat": time.Now().Unix(), "exp": o.exp.Unix(),
			"checkout": checkout,
			"cnf":      map[string]interface{}{"jwk": testPubJWK(&holderKey.PublicKey)},
		}, issuerKey)

	sdHash := o.sdHash
	if sdHash == "" {
		sum := sha256.Sum256([]byte(issuerJWT + "~"))
		sdHash = base64.RawURLEncoding.EncodeToString(sum[:])
	}
	kbJWT := testSignJWT(t,
		map[string]interface{}{"alg": "ES256", "typ": "kb+jwt"},
		map[string]interface{}{"iat": time.Now().Unix(), "aud": o.aud, "nonce": o.nonce, "sd_hash": sdHash},
		holderKey)
	return issuerJWT + "~" + kbJWT, issuerKey, holderKey
}

func memNonce() (func(string) (bool, error), func(string) error) {
	seen := map[string]bool{}
	return func(n string) (bool, error) { return seen[n], nil },
		func(n string) error { seen[n] = true; return nil }
}

func TestES256SignVerifyRoundTrip(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sig, err := es256Sign([]byte("hello"), key)
	if err != nil {
		t.Fatal(err)
	}
	if !es256Verify([]byte("hello"), sig, &key.PublicKey) {
		t.Fatal("valid signature rejected")
	}
	if es256Verify([]byte("hell0"), sig, &key.PublicKey) {
		t.Fatal("tampered payload accepted")
	}
}

func sampleCheckout() map[string]interface{} {
	return map[string]interface{}{
		"id":       "chk_abc",
		"currency": "USD",
		"totals":   []interface{}{map[string]interface{}{"type": "total", "amount": 5400}},
		"line_items": []interface{}{
			map[string]interface{}{"item": map[string]interface{}{"id": "SKU1"}, "quantity": 2},
		},
	}
}

func TestVerifyCheckoutMandate_Accepts(t *testing.T) {
	seen, remember := memNonce()
	m, _, _ := buildTestMandate(t, sampleCheckout(), testMandateOpts{aud: "https://shop.test"})
	res, err := VerifyCheckoutMandate(m, MandateVerifyOpts{
		ExpectedAudience: "https://shop.test", AllowUnpinnedKey: true,
		NonceSeen: seen, RememberNonce: remember,
	})
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	if _, ok := res.Claims["checkout"].(map[string]interface{}); !ok {
		t.Fatalf("claims missing embedded checkout: %v", res.Claims)
	}
}

func TestVerifyCheckoutMandate_Rejects(t *testing.T) {
	cases := []struct {
		name string
		opts testMandateOpts
		vo   func(*MandateVerifyOpts)
		want error
	}{
		{"wrong audience", testMandateOpts{aud: "https://evil.test"}, nil, ErrMandateScopeMismatch},
		{"expired", testMandateOpts{aud: "https://shop.test", exp: time.Now().Add(-time.Minute)}, nil, ErrMandateExpired},
		{"bad sd_hash", testMandateOpts{aud: "https://shop.test", sdHash: "AAAA"}, nil, ErrMandateScopeMismatch},
		{"unpinned key not allowed", testMandateOpts{aud: "https://shop.test"}, func(o *MandateVerifyOpts) { o.AllowUnpinnedKey = false }, ErrAgentMissingKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen, remember := memNonce()
			m, _, _ := buildTestMandate(t, sampleCheckout(), c.opts)
			opts := MandateVerifyOpts{
				ExpectedAudience: "https://shop.test", AllowUnpinnedKey: true,
				NonceSeen: seen, RememberNonce: remember,
			}
			if c.vo != nil {
				c.vo(&opts)
			}
			_, err := VerifyCheckoutMandate(m, opts)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestVerifyCheckoutMandate_NonceReplay(t *testing.T) {
	seen, remember := memNonce()
	m, _, _ := buildTestMandate(t, sampleCheckout(), testMandateOpts{aud: "https://shop.test", nonce: "fixed-nonce"})
	opts := MandateVerifyOpts{ExpectedAudience: "https://shop.test", AllowUnpinnedKey: true, NonceSeen: seen, RememberNonce: remember}
	if _, err := VerifyCheckoutMandate(m, opts); err != nil {
		t.Fatalf("first use should pass: %v", err)
	}
	if _, err := VerifyCheckoutMandate(m, opts); !errors.Is(err, ErrMandateScopeMismatch) {
		t.Fatalf("replay should be rejected, got %v", err)
	}
}

func TestVerifyCheckoutMandate_TamperedSignature(t *testing.T) {
	seen, remember := memNonce()
	m, _, _ := buildTestMandate(t, sampleCheckout(), testMandateOpts{aud: "https://shop.test"})
	tampered := m[:len(m)-3] + "AAA"
	_, err := VerifyCheckoutMandate(tampered, MandateVerifyOpts{
		ExpectedAudience: "https://shop.test", AllowUnpinnedKey: true, NonceSeen: seen, RememberNonce: remember,
	})
	if err == nil {
		t.Fatal("tampered mandate accepted")
	}
}

func TestLooksLikeMandateJWT(t *testing.T) {
	if !LooksLikeMandateJWT("aaa.bbb.ccc") {
		t.Fatal("compact JWT not recognised")
	}
	if !LooksLikeMandateJWT("aaa.bbb.ccc~disc~kb.kb.kb") {
		t.Fatal("SD-JWT not recognised")
	}
	if LooksLikeMandateJWT("tok_visa_1234") {
		t.Fatal("opaque token misread as JWT")
	}
}
