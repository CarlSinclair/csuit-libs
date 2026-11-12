package agentidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	StatusPending = "pending"
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

var (
	ErrTooSoon  = errors.New("agentidentity: a confirmation was requested a moment ago")
	ErrInvalid  = errors.New("agentidentity: invalid or expired token")
	ErrBadStore = errors.New("agentidentity: store is not configured")
)

type Store struct {
	DB          *sql.DB
	Table       string
	SubjectCol  string
	Prefix      string
	PendingTTL  time.Duration
	ActiveTTL   time.Duration
	ResendAfter time.Duration
}

type Summary struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
}

func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (s *Store) check() error {
	if s == nil || s.DB == nil || !safeIdent(s.Table) || !safeIdent(s.SubjectCol) || s.Prefix == "" {
		return ErrBadStore
	}
	return nil
}

func (s *Store) pendingTTL() time.Duration {
	if s.PendingTTL > 0 {
		return s.PendingTTL
	}
	return 30 * time.Minute
}

func (s *Store) activeTTL() time.Duration {
	if s.ActiveTTL > 0 {
		return s.ActiveTTL
	}
	return 90 * 24 * time.Hour
}

func (s *Store) resendAfter() time.Duration {
	if s.ResendAfter > 0 {
		return s.ResendAfter
	}
	return 2 * time.Minute
}

func hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func secret(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Store) Request(ctx context.Context, subjectID int64, name string) (token, confirm string, err error) {
	if err := s.check(); err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	var recent int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.Table+" WHERE "+s.SubjectCol+" = ? AND status = ? AND created_at > ?",
		subjectID, StatusPending, now.Add(-s.resendAfter())).Scan(&recent); err != nil {
		logger.Error("agentidentity Request: throttle check failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", fmt.Errorf("could not verify limits: %w", err)
	}
	if recent > 0 {
		return "", "", ErrTooSoon
	}
	tokenSecret, err := secret(24)
	if err != nil {
		logger.Error("agentidentity Request: random failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	confirm, err = secret(24)
	if err != nil {
		logger.Error("agentidentity Request: random failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	token = s.Prefix + tokenSecret
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("agentidentity Request: begin failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE "+s.Table+" SET status = ?, revoked_at = ? WHERE "+s.SubjectCol+" = ? AND status = ?",
		StatusRevoked, now, subjectID, StatusPending); err != nil {
		logger.Error("agentidentity Request: superseding pending failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.Table+" ("+s.SubjectCol+", token_hash, confirm_hash, name, status, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		subjectID, hash(token), hash(confirm), strings.TrimSpace(name), StatusPending, now, now.Add(s.pendingTTL())); err != nil {
		logger.Error("agentidentity Request: insert failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		logger.Error("agentidentity Request: commit failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", "", err
	}
	return token, confirm, nil
}

func (s *Store) Confirm(ctx context.Context, confirm string) (int64, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	confirm = strings.TrimSpace(confirm)
	if confirm == "" {
		return 0, ErrInvalid
	}
	now := time.Now().UTC()
	h := hash(confirm)
	res, err := s.DB.ExecContext(ctx, "UPDATE "+s.Table+" SET status = ?, confirmed_at = ?, expires_at = ? WHERE confirm_hash = ? AND status = ? AND expires_at > ?",
		StatusActive, now, now.Add(s.activeTTL()), h, StatusPending, now)
	if err != nil {
		logger.Error("agentidentity Confirm: update failed table=%s err=%v", s.Table, err)
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		logger.Error("agentidentity Confirm: rows affected failed table=%s err=%v", s.Table, err)
		return 0, err
	}
	if n != 1 {
		logger.Warn("agentidentity Confirm: no pending unexpired token matched table=%s", s.Table)
		return 0, ErrInvalid
	}
	var subject int64
	if err := s.DB.QueryRowContext(ctx, "SELECT "+s.SubjectCol+" FROM "+s.Table+" WHERE confirm_hash = ?", h).Scan(&subject); err != nil {
		logger.Error("agentidentity Confirm: subject lookup failed table=%s err=%v", s.Table, err)
		return 0, err
	}
	return subject, nil
}

func (s *Store) Resolve(ctx context.Context, token string) (int64, bool) {
	if s.check() != nil {
		return 0, false
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, s.Prefix) {
		return 0, false
	}
	var id, subject int64
	err := s.DB.QueryRowContext(ctx, "SELECT id, "+s.SubjectCol+" FROM "+s.Table+" WHERE token_hash = ? AND status = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) LIMIT 1",
		hash(token), StatusActive, time.Now().UTC()).Scan(&id, &subject)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.Error("agentidentity Resolve: lookup failed table=%s err=%v", s.Table, err)
		}
		return 0, false
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE "+s.Table+" SET last_used_at = ? WHERE id = ?", time.Now().UTC(), id); err != nil {
		logger.Warn("agentidentity Resolve: last_used update failed table=%s id=%d err=%v", s.Table, id, err)
	}
	return subject, true
}

func (s *Store) Mint(ctx context.Context, subjectID int64, name string) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	tokenSecret, err := secret(32)
	if err != nil {
		logger.Error("agentidentity Mint: random failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", err
	}
	token := s.Prefix + tokenSecret
	now := time.Now().UTC()
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO "+s.Table+" ("+s.SubjectCol+", token_hash, name, status, created_at, confirmed_at) VALUES (?, ?, ?, ?, ?, ?)",
		subjectID, hash(token), strings.TrimSpace(name), StatusActive, now, now); err != nil {
		logger.Error("agentidentity Mint: insert failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return "", err
	}
	return token, nil
}

func (s *Store) List(ctx context.Context, subjectID int64) ([]Summary, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id, COALESCE(name, ''), created_at, last_used_at FROM "+s.Table+" WHERE "+s.SubjectCol+" = ? AND status = ? AND revoked_at IS NULL ORDER BY created_at DESC, id DESC", subjectID, StatusActive)
	if err != nil {
		logger.Error("agentidentity List: query failed table=%s subject=%d err=%v", s.Table, subjectID, err)
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var m Summary
		if err := rows.Scan(&m.ID, &m.Name, &m.CreatedAt, &m.LastUsedAt); err != nil {
			logger.Error("agentidentity List: scan failed table=%s err=%v", s.Table, err)
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		logger.Error("agentidentity List: iterate failed table=%s err=%v", s.Table, err)
		return nil, err
	}
	return out, nil
}

func (s *Store) Revoke(ctx context.Context, token string) error {
	if err := s.check(); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, s.Prefix) {
		return ErrInvalid
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE "+s.Table+" SET status = ?, revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL", StatusRevoked, time.Now().UTC(), hash(token))
	if err != nil {
		logger.Error("agentidentity Revoke: update failed table=%s err=%v", s.Table, err)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalid
	}
	return nil
}

func (s *Store) RevokeByID(ctx context.Context, subjectID, id int64) error {
	if err := s.check(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE "+s.Table+" SET status = ?, revoked_at = ? WHERE id = ? AND "+s.SubjectCol+" = ? AND revoked_at IS NULL", StatusRevoked, time.Now().UTC(), id, subjectID)
	if err != nil {
		logger.Error("agentidentity RevokeByID: update failed table=%s subject=%d id=%d err=%v", s.Table, subjectID, id, err)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalid
	}
	return nil
}
