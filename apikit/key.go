package apikit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	timeLayout    = "2006-01-02 15:04:05"
	touchInterval = time.Minute
	prefixLen     = 12
)

type Key struct {
	ID         int64
	Label      string
	Prefix     string
	Scopes     []string
	RateLimit  int
	CreatedBy  int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
	LastUsedIP string
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

func (k *Key) Revoked() bool { return k.RevokedAt != nil }

func (k *Key) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !k.ExpiresAt.After(now)
}

func (k *Key) HasScope(scope string) bool { return ScopesInclude(k.Scopes, scope) }

func ScopesInclude(granted []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return true
	}
	resource, action, _ := strings.Cut(want, ":")
	for _, g := range granted {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == want || g == "*" {
			return true
		}
		if action == "read" && g == resource+":write" {
			return true
		}
	}
	return false
}

func NormalizeScopes(requested []string, allowed []string) ([]string, error) {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(a))] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(requested))
	for _, r := range requested {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		if !allowedSet[r] {
			return nil, fmt.Errorf("unknown scope %q", r)
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	return out, nil
}

func NewToken(tokenPrefix string) (raw, hash, display string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("apikit: random token: %w", err)
	}
	raw = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	display = raw
	if len(display) > prefixLen {
		display = display[:prefixLen]
	}
	return raw, HashToken(raw), display, nil
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type Store interface {
	Insert(ctx context.Context, k Key, hash string) (int64, error)
	Lookup(ctx context.Context, hash string) (*Key, error)
	Touch(ctx context.Context, id int64, ip string, now time.Time) error
	List(ctx context.Context, owner int64) ([]Key, error)
	Revoke(ctx context.Context, id int64, owner int64, now time.Time) (bool, error)
}

type SQLStore struct {
	DB *sql.DB
}

const keyColumns = `id, key_prefix, label, scopes, rate_limit, created_by, created_at, last_used_at, last_used_ip, expires_at, revoked_at`

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseDBTime(v any) (*time.Time, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case time.Time:
		if x.IsZero() {
			return nil, nil
		}
		t := x.UTC()
		return &t, nil
	case []byte:
		return parseDBTimeString(string(x))
	case string:
		return parseDBTimeString(x)
	default:
		return nil, fmt.Errorf("apikit: unsupported time value %T", v)
	}
}

func parseDBTimeString(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0000-00-00") {
		return nil, nil
	}
	for _, layout := range []string{timeLayout, time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, fmt.Errorf("apikit: unparseable time %q", s)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanKey(sc rowScanner) (*Key, error) {
	var (
		k                                 Key
		scopes                            string
		created, lastUsed, expires, revok any
	)
	if err := sc.Scan(&k.ID, &k.Prefix, &k.Label, &scopes, &k.RateLimit, &k.CreatedBy, &created, &lastUsed, &k.LastUsedIP, &expires, &revok); err != nil {
		return nil, err
	}
	k.Scopes = splitScopes(scopes)
	var err error
	var t *time.Time
	if t, err = parseDBTime(created); err != nil {
		return nil, err
	}
	if t != nil {
		k.CreatedAt = *t
	}
	if k.LastUsedAt, err = parseDBTime(lastUsed); err != nil {
		return nil, err
	}
	if k.ExpiresAt, err = parseDBTime(expires); err != nil {
		return nil, err
	}
	if k.RevokedAt, err = parseDBTime(revok); err != nil {
		return nil, err
	}
	return &k, nil
}

func splitScopes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *SQLStore) Insert(ctx context.Context, k Key, hash string) (int64, error) {
	var expires any
	if k.ExpiresAt != nil {
		expires = formatTime(*k.ExpiresAt)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_keys (key_hash, key_prefix, label, scopes, rate_limit, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, k.Prefix, k.Label, strings.Join(k.Scopes, ","), k.RateLimit, k.CreatedBy, formatTime(time.Now()), expires)
	if err != nil {
		return 0, fmt.Errorf("apikit: insert key label=%q: %w", k.Label, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("apikit: read new key id label=%q: %w", k.Label, err)
	}
	return id, nil
}

func (s *SQLStore) Lookup(ctx context.Context, hash string) (*Key, error) {
	k, err := scanKey(s.DB.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE key_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apikit: lookup key: %w", err)
	}
	return k, nil
}

func (s *SQLStore) Touch(ctx context.Context, id int64, ip string, now time.Time) error {
	cutoff := formatTime(now.Add(-touchInterval))
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ?, last_used_ip = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		formatTime(now), ip, id, cutoff); err != nil {
		return fmt.Errorf("apikit: touch key id=%d: %w", id, err)
	}
	return nil
}

func (s *SQLStore) List(ctx context.Context, owner int64) ([]Key, error) {
	q := `SELECT ` + keyColumns + ` FROM api_keys`
	var args []any
	if owner != 0 {
		q += ` WHERE created_by = ?`
		args = append(args, owner)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("apikit: list keys owner=%d: %w", owner, err)
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("apikit: scan key: %w", err)
		}
		out = append(out, *k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apikit: iterate keys: %w", err)
	}
	return out, nil
}

func (s *SQLStore) Revoke(ctx context.Context, id int64, owner int64, now time.Time) (bool, error) {
	q := `UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`
	args := []any{formatTime(now), id}
	if owner != 0 {
		q += ` AND created_by = ?`
		args = append(args, owner)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("apikit: revoke key id=%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("apikit: revoke key id=%d rows: %w", id, err)
	}
	return n > 0, nil
}
