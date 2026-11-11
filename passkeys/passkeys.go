package passkeys

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

const challengeTTL = 5 * time.Minute

var ErrRejected = errors.New("passkey not recognised")

type Dialect struct {
	Now          string
	RowID        string
	StartedAlt   string
	TimeArgs     bool
	TagStartedAt bool
}

var (
	SQLite      = Dialect{Now: "CURRENT_TIMESTAMP", RowID: "id", StartedAlt: "created_at", TimeArgs: true}
	SQLiteRowID = Dialect{Now: "CURRENT_TIMESTAMP", RowID: "rowid", StartedAlt: "expires_at", TimeArgs: true, TagStartedAt: true}
	MySQL       = Dialect{Now: "NOW()", RowID: "id", StartedAlt: "created_at", TagStartedAt: true}
)

type Service struct {
	DB          func() *sql.DB
	DisplayName string
	Dialect     Dialect
	Revoke      func(ctx context.Context, userID int64, sid string) error
}

type user struct {
	id     int64
	handle []byte
	email  string
	creds  []webauthn.Credential
}

func (s *Service) userHandle(userID int64) ([]byte, error) {
	var h []byte
	if err := s.DB().QueryRow(`SELECT webauthn_id FROM users WHERE id = ?`, userID).Scan(&h); err != nil {
		return nil, fmt.Errorf("read webauthn handle uid=%d: %w", userID, err)
	}
	if len(h) > 0 {
		return h, nil
	}
	h = make([]byte, 32)
	if _, err := rand.Read(h); err != nil {
		return nil, fmt.Errorf("webauthn handle entropy uid=%d: %w", userID, err)
	}
	if _, err := s.DB().Exec(`UPDATE users SET webauthn_id = ? WHERE id = ? AND (webauthn_id IS NULL OR length(webauthn_id) = 0)`, h, userID); err != nil {
		return nil, fmt.Errorf("store webauthn handle uid=%d: %w", userID, err)
	}
	if err := s.DB().QueryRow(`SELECT webauthn_id FROM users WHERE id = ?`, userID).Scan(&h); err != nil {
		return nil, fmt.Errorf("reread webauthn handle uid=%d: %w", userID, err)
	}
	return h, nil
}

func (u *user) WebAuthnID() []byte                         { return u.handle }
func (u *user) WebAuthnName() string                       { return u.email }
func (u *user) WebAuthnDisplayName() string                { return u.email }
func (u *user) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Service) rpFor(r *http.Request) (*webauthn.WebAuthn, error) {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return nil, errors.New("request has no host")
	}
	id := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		id = h
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: s.DisplayName,
		RPID:          id,
		RPOrigins:     []string{"https://" + host},
	})
}

func (s *Service) loadCredentials(userID int64) ([]webauthn.Credential, error) {
	rows, err := s.DB().Query(`SELECT credential FROM user_passkeys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("load passkeys uid=%d: %w", userID, err)
	}
	defer rows.Close()
	var out []webauthn.Credential
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan passkey uid=%d: %w", userID, err)
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			logger.Error("AUTH passkey_decode uid=%d err=%v", userID, err)
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func putChallenge(key string, sd *webauthn.SessionData) error {
	if redis.Client() == nil {
		return errors.New("redis is not available")
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return redis.Client().Set(context.Background(), redis.Key(key), raw, challengeTTL).Err()
}

func takeChallenge(key string) (*webauthn.SessionData, error) {
	if redis.Client() == nil {
		return nil, errors.New("redis is not available")
	}
	raw, err := redis.Client().GetDel(context.Background(), redis.Key(key)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("challenge missing or expired: %w", err)
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

func credKey(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }

func (s *Service) newUser(id int64, email string, creds []webauthn.Credential) (*user, error) {
	h, err := s.userHandle(id)
	if err != nil {
		return nil, err
	}
	return &user{id: id, handle: h, email: email, creds: creds}, nil
}

func (s *Service) BeginRegistration(r *http.Request, userID int64, email string) (*protocol.CredentialCreation, error) {
	wa, err := s.rpFor(r)
	if err != nil {
		return nil, err
	}
	creds, err := s.loadCredentials(userID)
	if err != nil {
		return nil, err
	}
	excl := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		excl = append(excl, c.Descriptor())
	}
	rk := true
	u, err := s.newUser(userID, email, creds)
	if err != nil {
		return nil, err
	}
	creation, sd, err := wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			RequireResidentKey: &rk,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(excl))
	if err != nil {
		return nil, fmt.Errorf("begin registration uid=%d: %w", userID, err)
	}
	if err := putChallenge(fmt.Sprintf("passkey:reg:%d", userID), sd); err != nil {
		return nil, fmt.Errorf("store registration challenge uid=%d: %w", userID, err)
	}
	return creation, nil
}

func (s *Service) FinishRegistration(r *http.Request, userID int64, email, name string) error {
	wa, err := s.rpFor(r)
	if err != nil {
		return err
	}
	sd, err := takeChallenge(fmt.Sprintf("passkey:reg:%d", userID))
	if err != nil {
		return err
	}
	creds, err := s.loadCredentials(userID)
	if err != nil {
		return err
	}
	u, err := s.newUser(userID, email, creds)
	if err != nil {
		return err
	}
	cred, err := wa.FinishRegistration(u, *sd, r)
	if err != nil {
		return fmt.Errorf("finish registration uid=%d: %w", userID, err)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("encode passkey uid=%d: %w", userID, err)
	}
	name = strings.TrimSpace(name)
	if len(name) > 80 {
		name = name[:80]
	}
	if _, err := s.DB().Exec(`INSERT INTO user_passkeys (user_id, name, cred_id, credential) VALUES (?, ?, ?, ?)`,
		userID, name, credKey(cred.ID), string(raw)); err != nil {
		return fmt.Errorf("store passkey uid=%d: %w", userID, err)
	}
	return nil
}

func (s *Service) BeginLogin(r *http.Request) (*protocol.CredentialAssertion, string, error) {
	wa, err := s.rpFor(r)
	if err != nil {
		return nil, "", err
	}
	assertion, sd, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", fmt.Errorf("begin passkey login: %w", err)
	}
	token := uuid.NewString()
	if err := putChallenge("passkey:login:"+token, sd); err != nil {
		return nil, "", fmt.Errorf("store login challenge: %w", err)
	}
	return assertion, token, nil
}

func (s *Service) FinishLogin(r *http.Request, token string) (int64, error) {
	wa, err := s.rpFor(r)
	if err != nil {
		return 0, err
	}
	sd, err := takeChallenge("passkey:login:" + token)
	if err != nil {
		return 0, err
	}
	var found *user
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		var uid int64
		var email string
		err := s.DB().QueryRow(`SELECT u.id, u.email FROM user_passkeys p JOIN users u ON u.id = p.user_id WHERE p.cred_id = ?`, credKey(rawID)).Scan(&uid, &email)
		if err != nil {
			return nil, ErrRejected
		}
		stored, err := s.userHandle(uid)
		if err != nil {
			return nil, err
		}
		if !hmac.Equal(userHandle, stored) {
			return nil, ErrRejected
		}
		creds, err := s.loadCredentials(uid)
		if err != nil {
			return nil, err
		}
		u, err := s.newUser(uid, email, creds)
		if err != nil {
			return nil, err
		}
		found = u
		return found, nil
	}
	_, cred, err := wa.FinishPasskeyLogin(handler, *sd, r)
	if err != nil || found == nil {
		return 0, fmt.Errorf("finish passkey login: %w", errors.Join(ErrRejected, err))
	}
	if cred.Authenticator.CloneWarning {
		return found.id, fmt.Errorf("passkey clone warning uid=%d: %w", found.id, ErrRejected)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return 0, fmt.Errorf("encode passkey uid=%d: %w", found.id, err)
	}
	if _, err := s.DB().Exec(`UPDATE user_passkeys SET credential = ?, last_used_at = `+s.Dialect.Now+` WHERE user_id = ? AND cred_id = ?`,
		string(raw), found.id, credKey(cred.ID)); err != nil {
		return 0, fmt.Errorf("update passkey use uid=%d: %w", found.id, err)
	}
	return found.id, nil
}

type Info struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
}

func (s *Service) List(userID int64) ([]Info, error) {
	rows, err := s.DB().Query(`SELECT id, name, created_at, last_used_at FROM user_passkeys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys uid=%d: %w", userID, err)
	}
	defer rows.Close()
	var out []Info
	for rows.Next() {
		var p Info
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan passkey uid=%d: %w", userID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Delete(userID, id int64) error {
	res, err := s.DB().Exec(`DELETE FROM user_passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete passkey id=%d uid=%d: %w", id, userID, err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("delete passkey id=%d uid=%d rows affected: %w", id, userID, rowsErr)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func hashRefresh(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func (s *Service) TagSession(refreshToken, location, userAgent string) {
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	q := `UPDATE refresh_tokens SET location = ?, user_agent = ?`
	if s.Dialect.TagStartedAt {
		q += `, started_at = ` + s.Dialect.Now
	}
	q += ` WHERE token_hash = ?`
	if _, err := s.DB().Exec(q, location, userAgent, hashRefresh(refreshToken)); err != nil {
		logger.Error("AUTH tag_session_failed err=%v", err)
	}
}

type SessionInfo struct {
	ID        int64
	Location  string
	UserAgent string
	StartedAt time.Time
	Current   bool
}

func (s *Service) ListSessions(userID int64, currentRefresh string) ([]SessionInfo, error) {
	cur := ""
	if currentRefresh != "" {
		cur = hashRefresh(currentRefresh)
	}
	started := "COALESCE(started_at, " + s.Dialect.StartedAlt + ")"
	where := `user_id = ? AND superseded_at IS NULL AND expires_at > ` + s.Dialect.Now
	args := []any{cur, userID}
	if s.Dialect.TimeArgs {
		where = `user_id = ? AND expires_at > ? AND superseded_at IS NULL`
		args = append(args, time.Now().UTC())
	}
	rows, err := s.DB().Query(`SELECT `+s.Dialect.RowID+`, location, user_agent, `+started+`, token_hash = ?
		FROM refresh_tokens
		WHERE `+where+`
		ORDER BY `+started+` DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions uid=%d: %w", userID, err)
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.ID, &si.Location, &si.UserAgent, &si.StartedAt, &si.Current); err != nil {
			return nil, fmt.Errorf("scan session uid=%d: %w", userID, err)
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

func (s *Service) RevokeSession(userID, id int64) error {
	var sid string
	if err := s.DB().QueryRow(`SELECT sid FROM refresh_tokens WHERE `+s.Dialect.RowID+` = ? AND user_id = ?`, id, userID).Scan(&sid); err != nil {
		return err
	}
	if sid == "" {
		if _, err := s.DB().Exec(`DELETE FROM refresh_tokens WHERE `+s.Dialect.RowID+` = ? AND user_id = ?`, id, userID); err != nil {
			return fmt.Errorf("revoke session id=%d uid=%d: %w", id, userID, err)
		}
		return nil
	}
	if err := s.Revoke(context.Background(), userID, sid); err != nil {
		logger.Error("RevokeSession failed id=%d uid=%d err=%v", id, userID, err)
		return err
	}
	return nil
}

func (si SessionInfo) Device() string {
	ua := si.UserAgent
	browser := ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	osName := ""
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iOS"}, {"iPad", "iOS"}, {"Windows", "Windows"}, {"Mac OS X", "macOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			osName = o.name
			break
		}
	}
	switch {
	case browser != "" && osName != "":
		return browser + " / " + osName
	case browser != "":
		return browser
	}
	return osName
}
