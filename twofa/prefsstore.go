package twofa

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// SQLPrefs keeps each person's Prefs in the user_twofa_prefs table (user_id
// primary key, prefs text). SQLite selects the upsert syntax of SQLite instead
// of MySQL.
type SQLPrefs struct {
	DB     func() *sql.DB
	SQLite bool
}

func (s SQLPrefs) Load(userID int64) (Prefs, error) {
	var raw string
	err := s.DB().QueryRow("SELECT prefs FROM user_twofa_prefs WHERE user_id = ?", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Prefs{}, nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("read 2fa prefs user_id=%d: %w", userID, err)
	}
	return ParsePrefs(raw), nil
}

func (s SQLPrefs) Get(userID int64) Prefs {
	p, err := s.Load(userID)
	if err != nil {
		logger.Error("2FA prefs read failed user_id=%d err=%v", userID, err)
		return Prefs{}
	}
	return p
}

func (s SQLPrefs) Save(userID int64, p Prefs) error {
	q := "INSERT INTO user_twofa_prefs (user_id, prefs) VALUES (?, ?) ON DUPLICATE KEY UPDATE prefs = VALUES(prefs)"
	if s.SQLite {
		q = "INSERT INTO user_twofa_prefs (user_id, prefs) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET prefs = excluded.prefs, updated_at = CURRENT_TIMESTAMP"
	}
	if _, err := s.DB().Exec(q, userID, p.Encode()); err != nil {
		return fmt.Errorf("save 2fa prefs user_id=%d: %w", userID, err)
	}
	return nil
}

func (s SQLPrefs) Delete(userID int64) {
	if _, err := s.DB().Exec("DELETE FROM user_twofa_prefs WHERE user_id = ?", userID); err != nil {
		logger.Error("2FA prefs cleanup failed user_id=%d err=%v", userID, err)
	}
}
