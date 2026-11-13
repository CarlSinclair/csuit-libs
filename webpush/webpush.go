package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/apikit"
	"github.com/CarlSinclair/csuit-libs/logger"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/hkdf"
)

// Web Push (RFC 8291 message encryption + RFC 8292 VAPID) - real push
// notifications delivered by the browser/OS even when no admin tab is open,
// complementing the existing in-app bell (services/notifications.go), which
// only reaches an already-open page. This file owns the VAPID keypair, the
// per-device subscription table, and the encrypt+send call; the dispatch
// decision (which events, which users) stays in NotifyUser
// (notifications.go), which calls SendWebPushToUser the same way it already
// calls the in-app/email/telegram channels.

type Config struct {
	DB              func() *sql.DB
	MySQL           bool
	GetSetting      func(key string) (string, error)
	SetSetting      func(key, value string) error
	InstanceURL     func() string
	FallbackSubject string
}

var cfg Config

func Configure(c Config) { cfg = c }

func database() (*sql.DB, error) {
	if cfg.DB == nil {
		return nil, errors.New("webpush: not configured")
	}
	d := cfg.DB()
	if d == nil {
		return nil, errors.New("webpush: database not initialised")
	}
	return d, nil
}

const vapidPublicKeySettingKey = "vapid_public_key"
const vapidPrivateKeySettingKey = "vapid_private_key"

var webpushHTTPClient = apikit.NewSafeClient(func(host string) error {
	if !pushHostAllowed(host) {
		return ErrInvalidPushEndpoint
	}
	return nil
})

// unifiedPushClient delivers to UnifiedPush distributors, whose hosts are not
// known in advance (a self-hosted ntfy, for one). The safe client still
// refuses private, loopback and link-local destinations and any redirect.
var unifiedPushClient = apikit.NewSafeClient(nil)

var pushHostSuffixes = []string{
	"fcm.googleapis.com",
	"push.services.mozilla.com",
	"push.apple.com",
	"notify.windows.com",
}

var ErrInvalidPushEndpoint = errors.New("push endpoint is not an https URL on a known push service")

func ValidatePushEndpoint(raw string) error {
	u, err := publicHTTPSEndpoint(raw)
	if err != nil {
		return err
	}
	if !pushHostAllowed(u.Hostname()) {
		return ErrInvalidPushEndpoint
	}
	return nil
}

// ValidateDeviceEndpoint accepts the endpoint of a native app's push
// distributor, whose host is not known in advance (a self-hosted ntfy, for
// one). It must still be a public https address; delivery goes through the
// safe client, which also refuses private ranges and redirects.
func ValidateDeviceEndpoint(raw string) error {
	_, err := publicHTTPSEndpoint(raw)
	return err
}

func publicHTTPSEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" {
		return nil, ErrInvalidPushEndpoint
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, ErrInvalidPushEndpoint
	}
	if net.ParseIP(u.Hostname()) != nil || !strings.Contains(u.Hostname(), ".") {
		return nil, ErrInvalidPushEndpoint
	}
	return u, nil
}

func pushHostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range pushHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// PushSubscription is one browser/device's Web Push registration.
type PushSubscription struct {
	ID        int64  `db:"id"`
	UserID    int64  `db:"user_id"`
	Endpoint  string `db:"endpoint"`
	P256dh    string `db:"p256dh"`
	Auth      string `db:"auth"`
	UserAgent string `db:"user_agent"`
}

// GetVAPIDPublicKey returns the raw (uncompressed point) VAPID public key,
// base64url-encoded for use as a PushManager applicationServerKey. Keys are
// generated on first use and stored in business_config, the private half
// encrypted at rest the same way payment-provider secrets are (see
// secretStoreConfigKeys in services/config.go).
func GetVAPIDPublicKey() (string, error) {
	pub, err := cfg.GetSetting(vapidPublicKeySettingKey)
	if err != nil {
		return "", err
	}
	if pub = strings.TrimSpace(pub); pub != "" {
		return pub, nil
	}
	return generateAndStoreVAPIDKeys()
}

func generateAndStoreVAPIDKeys() (string, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		logger.Error("generateAndStoreVAPIDKeys: key generation failed err=%v", err)
		return "", err
	}
	pubBytes := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y)
	pubB64 := base64.RawURLEncoding.EncodeToString(pubBytes)

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		logger.Error("generateAndStoreVAPIDKeys: private key marshal failed err=%v", err)
		return "", err
	}
	privB64 := base64.RawURLEncoding.EncodeToString(privBytes)

	if err := cfg.SetSetting(vapidPrivateKeySettingKey, privB64); err != nil {
		logger.Error("generateAndStoreVAPIDKeys: store private key failed err=%v", err)
		return "", err
	}
	if err := cfg.SetSetting(vapidPublicKeySettingKey, pubB64); err != nil {
		logger.Error("generateAndStoreVAPIDKeys: store public key failed err=%v", err)
		return "", err
	}
	logger.Info("generateAndStoreVAPIDKeys: generated new VAPID keypair")
	return pubB64, nil
}

func loadVAPIDPrivateKey() (*ecdsa.PrivateKey, error) {
	val, err := cfg.GetSetting(vapidPrivateKeySettingKey)
	if err != nil {
		return nil, err
	}
	val = strings.TrimSpace(val)
	if val == "" {
		if _, genErr := generateAndStoreVAPIDKeys(); genErr != nil {
			return nil, genErr
		}
		val, err = cfg.GetSetting(vapidPrivateKeySettingKey)
		if err != nil {
			return nil, err
		}
		val = strings.TrimSpace(val)
		if val == "" {
			return nil, fmt.Errorf("vapid private key unavailable after generation")
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(val)
	if err != nil {
		return nil, fmt.Errorf("vapid private key decode failed: %w", err)
	}
	return x509.ParseECPrivateKey(raw)
}

// endpointHash is the unique-index key for push_subscriptions - push
// endpoint URLs share a long, provider-specific prefix (e.g. every FCM
// endpoint starts the same way) and can run past what a MySQL utf8mb4 index
// can cover, so the raw endpoint (stored in a TEXT column) is deduped by the
// sha256 of its full length instead of a truncated prefix of itself.
func endpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

// SavePushSubscription upserts a subscription by its endpoint - re-subscribing
// the same browser (e.g. after clearing site data reissues the same endpoint
// in some browsers, or a plain re-registration) just refreshes the row
// instead of accumulating duplicates.
func SavePushSubscription(userID int64, endpoint, p256dh, auth, userAgent string) error {
	return savePushSubscription(userID, endpoint, p256dh, auth, userAgent, ValidatePushEndpoint)
}

// SaveDeviceSubscription stores a native app's registration. It is the same
// kind of per-device opt-in as a browser subscription and lives in the same
// table, so the notification settings apply to both unchanged.
func SaveDeviceSubscription(userID int64, endpoint, p256dh, auth, userAgent string) error {
	return savePushSubscription(userID, endpoint, p256dh, auth, userAgent, ValidateDeviceEndpoint)
}

func savePushSubscription(userID int64, endpoint, p256dh, auth, userAgent string, validate func(string) error) error {
	endpoint = strings.TrimSpace(endpoint)
	p256dh = strings.TrimSpace(p256dh)
	auth = strings.TrimSpace(auth)
	if userID <= 0 || endpoint == "" || p256dh == "" || auth == "" {
		return fmt.Errorf("missing required subscription fields")
	}
	if err := validate(endpoint); err != nil {
		return err
	}
	d, err := database()
	if err != nil {
		return err
	}
	if cfg.MySQL {
		_, err = d.Exec(`
		INSERT INTO push_subscriptions (user_id, endpoint, endpoint_hash, p256dh, auth, user_agent, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON DUPLICATE KEY UPDATE
			user_id = VALUES(user_id),
			endpoint = VALUES(endpoint),
			p256dh = VALUES(p256dh),
			auth = VALUES(auth),
			user_agent = VALUES(user_agent),
			last_used_at = CURRENT_TIMESTAMP
	`, userID, endpoint, endpointHash(endpoint), p256dh, auth, userAgent)
	} else {
		_, err = d.Exec(`
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(endpoint) DO UPDATE SET
			user_id = excluded.user_id,
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			user_agent = excluded.user_agent,
			last_used_at = CURRENT_TIMESTAMP
	`, userID, endpoint, p256dh, auth, userAgent)
	}
	if err != nil {
		logger.Error("SavePushSubscription: upsert failed user_id=%d err=%v", userID, err)
		return err
	}
	return nil
}

// DeletePushSubscription removes one subscription by endpoint - called when
// the browser reports the subscription as no longer valid, or the user
// explicitly turns push off on that device.
func DeletePushSubscription(userID int64, endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("missing endpoint")
	}
	d, err := database()
	if err != nil {
		return err
	}
	if cfg.MySQL {
		_, err = d.Exec(`DELETE FROM push_subscriptions WHERE user_id = ? AND endpoint_hash = ?`, userID, endpointHash(endpoint))
	} else {
		_, err = d.Exec(`DELETE FROM push_subscriptions WHERE user_id = ? AND endpoint = ?`, userID, endpoint)
	}
	if err != nil {
		logger.Error("DeletePushSubscription: delete failed user_id=%d err=%v", userID, err)
		return err
	}
	return nil
}

func listPushSubscriptions(userID int64) ([]PushSubscription, error) {
	d, err := database()
	if err != nil {
		return nil, err
	}
	rs, err := d.Query(`SELECT id, user_id, endpoint, p256dh, auth, COALESCE(user_agent, '') AS user_agent FROM push_subscriptions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var rows []PushSubscription
	for rs.Next() {
		var p PushSubscription
		if err := rs.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent); err != nil {
			return nil, err
		}
		rows = append(rows, p)
	}
	return rows, rs.Err()
}

// UserHasPushSubscription reports whether userID has at least one
// registered device - used to skip the work of building a push payload for
// users who have never enabled it.
func UserHasPushSubscription(userID int64) bool {
	d, err := database()
	if err != nil {
		logger.Error("UserHasPushSubscription: %v user_id=%d", err, userID)
		return false
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`, userID).Scan(&count); err != nil {
		logger.Error("UserHasPushSubscription: count failed user_id=%d err=%v", userID, err)
		return false
	}
	return count > 0
}

type webpushPayload struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Link        string `json:"link,omitempty"`
	Kind        string `json:"kind,omitempty"`
	UnreadCount *int64 `json:"unread_count,omitempty"`
}

// SendWebPushToUser encrypts and delivers title/body (already localized
// plain text - a service worker has no i18n framework to hand a key to) to
// every device userID has subscribed. Dead subscriptions (410 Gone / 404 Not
// Found, meaning the browser or OS discarded the registration) are pruned as
// they're discovered rather than retried forever.
func SendWebPushToUser(userID int64, title, body, link string, unreadCount *int64) {
	subs, err := listPushSubscriptions(userID)
	if err != nil {
		logger.Error("SendWebPushToUser: list subscriptions failed user_id=%d err=%v", userID, err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(webpushPayload{Title: title, Body: body, Link: link, UnreadCount: unreadCount})
	if err != nil {
		logger.Error("SendWebPushToUser: payload marshal failed user_id=%d err=%v", userID, err)
		return
	}
	for _, sub := range subs {
		if err := sendWebPush(sub, payload); err != nil {
			logger.Warn("SendWebPushToUser: send failed user_id=%d endpoint=%s err=%v", userID, sub.Endpoint, err)
		}
	}
}

// SendSessionEndedPush tells every device userID has subscribed that their
// session was just killed server-side (account disabled or deleted - see
// handleAdminUserUpdate/handleAdminUserDelete) so an open tab can prompt an
// in-place reauth instead of the next click just silently failing. Carries
// no title/body: the service worker only forwards this kind to a visible
// tab's page script (see sw.js) and never surfaces it as an OS notification,
// since there's nothing for a closed tab to usefully show.
func SendSessionEndedPush(userID int64) {
	subs, err := listPushSubscriptions(userID)
	if err != nil {
		logger.Error("SendSessionEndedPush: list subscriptions failed user_id=%d err=%v", userID, err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(webpushPayload{Kind: "session_ended"})
	if err != nil {
		logger.Error("SendSessionEndedPush: payload marshal failed user_id=%d err=%v", userID, err)
		return
	}
	for _, sub := range subs {
		if err := sendWebPush(sub, payload); err != nil {
			logger.Warn("SendSessionEndedPush: send failed user_id=%d endpoint=%s err=%v", userID, sub.Endpoint, err)
		}
	}
}

func sendWebPush(sub PushSubscription, payload []byte) error {
	if err := ValidateDeviceEndpoint(sub.Endpoint); err != nil {
		if delErr := DeletePushSubscription(sub.UserID, sub.Endpoint); delErr != nil {
			logger.Error("sendWebPush: removing subscription with invalid endpoint failed user_id=%d err=%v", sub.UserID, delErr)
		} else {
			logger.Warn("sendWebPush: removed subscription with invalid endpoint user_id=%d", sub.UserID)
		}
		return err
	}
	vapidPriv, err := loadVAPIDPrivateKey()
	if err != nil {
		return fmt.Errorf("vapid key unavailable: %w", err)
	}

	body, headers, err := encryptWebPushMessage(sub, payload)
	if err != nil {
		return fmt.Errorf("encrypt failed: %w", err)
	}

	endpointURL, err := url.Parse(sub.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint: %w", err)
	}
	audience := endpointURL.Scheme + "://" + endpointURL.Host

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": audience,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": vapidContactSubject(),
	})
	signed, err := token.SignedString(vapidPriv)
	if err != nil {
		return fmt.Errorf("vapid jwt sign failed: %w", err)
	}
	vapidPubB64 := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), vapidPriv.PublicKey.X, vapidPriv.PublicKey.Y))

	req, err := http.NewRequest(http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", fmt.Sprintf("vapid t=%s, k=%s", signed, vapidPubB64))
	req.Header.Set("TTL", "86400")

	client := unifiedPushClient
	if u, perr := url.Parse(sub.Endpoint); perr == nil && pushHostAllowed(u.Hostname()) {
		client = webpushHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if delErr := DeletePushSubscription(sub.UserID, sub.Endpoint); delErr != nil {
			logger.Error("sendWebPush: prune dead subscription failed user_id=%d err=%v", sub.UserID, delErr)
		}
		return fmt.Errorf("subscription expired (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push service returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func vapidContactSubject() string {
	if cfg.InstanceURL != nil {
		if u := strings.TrimSpace(cfg.InstanceURL()); u != "" {
			return u
		}
	}
	return cfg.FallbackSubject
}

// encryptWebPushMessage implements RFC 8291 (aes128gcm content-coding) for a
// single-record message - every push payload here is well under the 4096
// byte record size, so no chunking is needed.
func encryptWebPushMessage(sub PushSubscription, plaintext []byte) ([]byte, map[string]string, error) {
	clientPubRaw, err := decodeB64URL(sub.P256dh)
	if err != nil {
		return nil, nil, fmt.Errorf("bad p256dh: %w", err)
	}
	authSecret, err := decodeB64URL(sub.Auth)
	if err != nil {
		return nil, nil, fmt.Errorf("bad auth secret: %w", err)
	}

	curve := ecdh.P256()
	clientPub, err := curve.NewPublicKey(clientPubRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("bad client public key: %w", err)
	}
	serverPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serverPubRaw := serverPriv.PublicKey().Bytes()

	sharedSecret, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return nil, nil, fmt.Errorf("ecdh failed: %w", err)
	}

	// RFC 8291 section 3.4: derive IKM from the ECDH shared secret, salted
	// with the subscription's auth secret and bound to both public keys.
	authInfo := append([]byte("WebPush: info\x00"), clientPubRaw...)
	authInfo = append(authInfo, serverPubRaw...)
	prkKey := hkdf.Extract(sha256.New, sharedSecret, authSecret)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prkKey, authInfo), ikm); err != nil {
		return nil, nil, fmt.Errorf("hkdf expand ikm failed: %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	prk := hkdf.Extract(sha256.New, ikm, salt)

	cek := make([]byte, 16)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: aes128gcm\x00")), cek); err != nil {
		return nil, nil, fmt.Errorf("hkdf expand cek failed: %w", err)
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, nil, fmt.Errorf("hkdf expand nonce failed: %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	// RFC 8188 padding delimiter: 0x02 marks this as the final (only) record.
	padded := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, padded, nil)

	// RFC 8188 aes128gcm header: salt(16) || record size(4, big-endian) ||
	// key id length(1) || key id (server's ephemeral public key).
	header := make([]byte, 0, 16+4+1+len(serverPubRaw))
	header = append(header, salt...)
	recordSize := make([]byte, 4)
	binary.BigEndian.PutUint32(recordSize, uint32(4096))
	header = append(header, recordSize...)
	header = append(header, byte(len(serverPubRaw)))
	header = append(header, serverPubRaw...)

	body := append(header, ciphertext...)
	headers := map[string]string{
		"Content-Type":     "application/octet-stream",
		"Content-Encoding": "aes128gcm",
	}
	return body, headers, nil
}

// decodeB64URL decodes a base64url subscription key regardless of whether
// the caller sent it padded (PushManager's toJSON() omits padding, but some
// clients or JSON round-trips add it back) - strip any existing padding and
// decode with the unpadded alphabet, which accepts both shapes unambiguously.
func decodeB64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}
