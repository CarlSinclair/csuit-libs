package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/session"
)

const dbTimeLayout = "2006-01-02 15:04:05"

type Store struct {
	DB             func() *sql.DB
	MySQL          bool
	StartedFromNow bool
}

func (s *Store) Alive(ctx context.Context, userID int64, sid string) (bool, error) {
	var n int
	var err error
	if s.MySQL {
		err = s.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM refresh_tokens WHERE user_id = ? AND sid = ? AND expires_at > NOW()`,
			userID, sid).Scan(&n)
	} else {
		err = s.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM refresh_tokens WHERE user_id = ? AND sid = ? AND expires_at > ?`,
			userID, sid, time.Now().UTC()).Scan(&n)
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) Revoke(ctx context.Context, userID int64, sid string) error {
	_, err := s.DB().ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ? AND sid = ?`, userID, sid)
	return err
}

func (s *Store) RevokeAll(ctx context.Context, userID int64, except string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT DISTINCT sid FROM refresh_tokens WHERE user_id = ? AND sid IS NOT NULL AND sid <> ''`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	var sids []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if sid != except {
			sids = append(sids, sid)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close session list: %w", err)
	}
	if except == "" {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, userID); err != nil {
			return nil, err
		}
		return sids, nil
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ? AND (sid IS NULL OR sid <> ?)`, userID, except); err != nil {
		return nil, err
	}
	return sids, nil
}

func parseDBTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		return parseDBTimeString(t)
	case []byte:
		return parseDBTimeString(string(t))
	}
	return time.Time{}, false
}

func parseDBTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{dbTimeLayout, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999 -0700 MST", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (s *Store) Find(ctx context.Context, hash string) (session.RefreshRecord, error) {
	if s.MySQL {
		return s.findMySQL(ctx, hash)
	}
	var (
		rec          session.RefreshRecord
		supersededAt any
		sid          sql.NullString
	)
	err := s.DB().QueryRowContext(ctx,
		`SELECT user_id, sid, superseded_at, superseded_by, expires_at <= ? FROM refresh_tokens WHERE token_hash = ?`,
		time.Now().UTC(), hash,
	).Scan(&rec.UserID, &sid, &supersededAt, &rec.SupersededBy, &rec.Expired)
	if errors.Is(err, sql.ErrNoRows) {
		return session.RefreshRecord{}, session.ErrRefreshNotFound
	}
	if err != nil {
		return session.RefreshRecord{}, err
	}
	rec.SID = sid.String
	if supersededAt != nil {
		t, ok := parseDBTime(supersededAt)
		if !ok {
			return session.RefreshRecord{}, fmt.Errorf("refresh token superseded_at has unreadable value %v", supersededAt)
		}
		rec.Superseded = true
		rec.SupersededAgo = time.Since(t)
		if rec.SupersededAgo < 0 {
			rec.SupersededAgo = 0
		}
	}
	return rec, nil
}

func (s *Store) findMySQL(ctx context.Context, hash string) (session.RefreshRecord, error) {
	var (
		rec        session.RefreshRecord
		expired    int
		superseded int
		agoSec     int64
		by         string
		sid        sql.NullString
	)
	err := s.DB().QueryRowContext(ctx, `
		SELECT user_id, sid, (expires_at <= NOW()), (superseded_at IS NOT NULL),
		       COALESCE(TIMESTAMPDIFF(SECOND, superseded_at, NOW()), 0), COALESCE(superseded_by, '')
		FROM refresh_tokens WHERE token_hash = ?`, hash,
	).Scan(&rec.UserID, &sid, &expired, &superseded, &agoSec, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return session.RefreshRecord{}, session.ErrRefreshNotFound
	}
	if err != nil {
		return session.RefreshRecord{}, err
	}
	rec.SID = sid.String
	rec.Expired = expired == 1
	rec.Superseded = superseded == 1
	rec.SupersededAgo = time.Duration(agoSec) * time.Second
	rec.SupersededBy = by
	return rec, nil
}

func isDeadlock(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "Error 1213") || strings.Contains(err.Error(), "Error 1205"))
}

func (s *Store) Rotate(ctx context.Context, _ session.RefreshRecord, oldHash, newHash string, userID int64, sid string, newExpiry time.Time) (bool, error) {
	if !s.MySQL {
		return s.rotateSQLite(ctx, oldHash, newHash, userID, sid, newExpiry)
	}
	for attempt := 1; ; attempt++ {
		won, err := s.rotateMySQL(ctx, oldHash, newHash, userID, sid, newExpiry)
		if err == nil {
			return won, nil
		}
		if attempt >= 3 || !isDeadlock(err) {
			return false, err
		}
		logger.Warn("AUTH rotate_deadlock_retry uid=%d attempt=%d err=%v", userID, attempt, err)
		time.Sleep(time.Duration(attempt*25) * time.Millisecond)
	}
}

func (s *Store) rotateMySQL(ctx context.Context, oldHash, newHash string, userID int64, sid string, newExpiry time.Time) (bool, error) {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET superseded_at = NOW(), superseded_by = ? WHERE token_hash = ? AND superseded_at IS NULL`,
		newHash, oldHash)
	if err != nil {
		return false, fmt.Errorf("claim rotation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim rotation rows: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent, location, sid, started_at)
		SELECT ?, ?, ?, user_agent, location, ?, COALESCE(started_at, created_at)
		FROM refresh_tokens WHERE token_hash = ?`,
		userID, newHash, newExpiry, sid, oldHash); err != nil {
		return false, fmt.Errorf("store successor: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = ? AND (expires_at < NOW() OR (superseded_at IS NOT NULL AND superseded_at < NOW() - INTERVAL 1 HOUR))`,
		userID); err != nil {
		return false, fmt.Errorf("prune old tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit rotation: %w", err)
	}
	return true, nil
}

func (s *Store) rotateSQLite(ctx context.Context, oldHash, newHash string, userID int64, sid string, newExpiry time.Time) (bool, error) {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET superseded_at = ?, superseded_by = ? WHERE token_hash = ? AND superseded_at IS NULL`,
		now.Format(dbTimeLayout), newHash, oldHash)
	if err != nil {
		return false, fmt.Errorf("claim rotation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim rotation rows: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	if s.StartedFromNow {
		_, err = tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent, location, sid, started_at)
		SELECT ?, ?, ?, user_agent, location, ?, COALESCE(started_at, ?)
		FROM refresh_tokens WHERE token_hash = ?`,
			userID, newHash, newExpiry.UTC(), sid, now.Format(dbTimeLayout), oldHash)
	} else {
		_, err = tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent, location, sid, started_at)
		SELECT ?, ?, ?, user_agent, location, ?, COALESCE(started_at, created_at)
		FROM refresh_tokens WHERE token_hash = ?`,
			userID, newHash, newExpiry.UTC(), sid, oldHash)
	}
	if err != nil {
		return false, fmt.Errorf("store successor: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = ? AND (expires_at < ? OR (superseded_at IS NOT NULL AND superseded_at < ?))`,
		userID, now, now.Add(-time.Hour).Format(dbTimeLayout)); err != nil {
		return false, fmt.Errorf("prune old tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit rotation: %w", err)
	}
	logger.Info("AUTH refresh_rotated uid=%d sid=%s", userID, sid)
	return true, nil
}
