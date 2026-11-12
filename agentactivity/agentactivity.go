package agentactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const ActionPrefix = "agent_"

type Entry struct {
	Action     string
	FullAction string
	EntityID   string
	Details    map[string]any
	RawDetails string
	At         time.Time
}

type Store struct {
	DB      *sql.DB
	Table   string
	TimeCol string
	UserCol string
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

func (s *Store) list(ctx context.Context, where string, arg any, limit int) ([]Entry, error) {
	if s == nil || s.DB == nil || !safeIdent(s.Table) || !safeIdent(s.TimeCol) {
		return nil, errors.New("agentactivity: store is not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT action, COALESCE(entity_id, ''), COALESCE(details, ''), "+s.TimeCol+" FROM "+s.Table+" WHERE "+where+" AND SUBSTR(action, 1, 6) = 'agent_' ORDER BY "+s.TimeCol+" DESC, id DESC LIMIT ?", arg, limit)
	if err != nil {
		logger.Error("agentactivity list: query failed table=%s err=%v", s.Table, err)
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var details string
		var at sql.NullTime
		if err := rows.Scan(&e.Action, &e.EntityID, &details, &at); err != nil {
			logger.Error("agentactivity list: scan failed table=%s err=%v", s.Table, err)
			return nil, err
		}
		e.FullAction = e.Action
		e.Action = strings.TrimPrefix(e.Action, ActionPrefix)
		e.RawDetails = details
		e.At = at.Time
		if details != "" {
			var d map[string]any
			if err := json.Unmarshal([]byte(details), &d); err != nil {
				logger.Warn("agentactivity list: details unreadable table=%s action=%s err=%v", s.Table, e.Action, err)
			} else {
				e.Details = d
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		logger.Error("agentactivity list: iterate failed table=%s err=%v", s.Table, err)
		return nil, err
	}
	return out, nil
}

func (s *Store) ListByEntity(ctx context.Context, entity string, limit int) ([]Entry, error) {
	return s.list(ctx, "entity_id = ?", entity, limit)
}

func (s *Store) ListByUser(ctx context.Context, userID int64, limit int) ([]Entry, error) {
	if !safeIdent(s.UserCol) {
		return nil, errors.New("agentactivity: UserCol is not configured")
	}
	return s.list(ctx, s.UserCol+" = ?", userID, limit)
}
