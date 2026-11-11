package agentkeys

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Key struct {
	ID         int64
	KeyHash    string
	Label      string
	Scopes     string
	CreatedAt  sql.NullTime
	LastUsedAt sql.NullTime
	RevokedAt  sql.NullTime
}

type Store struct {
	DB            func() *sql.DB
	Prefix        string
	DefaultScopes string
	NowSQL        string
}

func (s *Store) db() (*sql.DB, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("agent keys: store not configured")
	}
	d := s.DB()
	if d == nil {
		return nil, errors.New("agent keys: database not initialised")
	}
	return d, nil
}

func (s *Store) now() string {
	if s.NowSQL != "" {
		return s.NowSQL
	}
	return "CURRENT_TIMESTAMP"
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

const columns = `id, key_hash, label, scopes, created_at, last_used_at, revoked_at`

func scan(row interface{ Scan(...any) error }) (*Key, error) {
	var k Key
	if err := row.Scan(&k.ID, &k.KeyHash, &k.Label, &k.Scopes, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) Create(label, scopes string) (rawKey string, id int64, err error) {
	d, err := s.db()
	if err != nil {
		return "", 0, err
	}
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", 0, fmt.Errorf("agent key: rand: %w", err)
	}
	rawKey = s.Prefix + hex.EncodeToString(buf)
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		scopes = s.DefaultScopes
	}
	res, err := d.Exec(`INSERT INTO agent_api_keys (key_hash, label, scopes) VALUES (?, ?, ?)`,
		hashKey(rawKey), strings.TrimSpace(label), scopes)
	if err != nil {
		return "", 0, fmt.Errorf("agent key: insert: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return "", 0, fmt.Errorf("agent key: read inserted id: %w", err)
	}
	logger.Info("CreateAgentKey: minted id=%d label=%q scopes=%q", id, label, scopes)
	return rawKey, id, nil
}

func (s *Store) Lookup(raw string) (*Key, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	k, err := scan(d.QueryRow(`SELECT `+columns+` FROM agent_api_keys WHERE key_hash = ? AND revoked_at IS NULL`, hashKey(raw)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("agent key: lookup: %w", err)
	}
	if _, e := d.Exec(`UPDATE agent_api_keys SET last_used_at = `+s.now()+` WHERE id = ?`, k.ID); e != nil {
		logger.Warn("LookupAgentKey: touch last_used_at id=%d err=%v", k.ID, e)
	}
	return k, nil
}

func (k *Key) HasScope(scope string) bool {
	if k == nil {
		return false
	}
	for _, s := range strings.Split(k.Scopes, ",") {
		if strings.EqualFold(strings.TrimSpace(s), scope) {
			return true
		}
	}
	return false
}

func (s *Store) List() ([]Key, error) {
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT ` + columns + ` FROM agent_api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("agent key: list: %w", err)
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		k, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("agent key: list scan: %w", err)
		}
		out = append(out, *k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent key: list rows: %w", err)
	}
	return out, nil
}

func (s *Store) Revoke(id int64) error {
	d, err := s.db()
	if err != nil {
		return err
	}
	if _, err := d.Exec(`UPDATE agent_api_keys SET revoked_at = `+s.now()+` WHERE id = ? AND revoked_at IS NULL`, id); err != nil {
		return fmt.Errorf("agent key: revoke id=%d: %w", id, err)
	}
	logger.Info("RevokeAgentKey: id=%d", id)
	return nil
}
