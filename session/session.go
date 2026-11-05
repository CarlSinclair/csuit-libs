package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultCacheTTL = 60 * time.Second
	aliveKeyPrefix  = "session:"
	revokedKeyPref  = "session_revoked:"
)

type Store interface {
	Alive(ctx context.Context, userID int64, sid string) (bool, error)
	Revoke(ctx context.Context, userID int64, sid string) error
	RevokeAll(ctx context.Context, userID int64, except string) ([]string, error)
}

type Manager struct {
	Store         Store
	CacheTTL      time.Duration
	CacheDisabled bool
}

func (m *Manager) ttl() time.Duration {
	if m.CacheTTL > 0 {
		return m.CacheTTL
	}
	return defaultCacheTTL
}

func aliveKey(sid string) string   { return redis.Key(aliveKeyPrefix + sid) }
func revokedKey(sid string) string { return redis.Key(revokedKeyPref + sid) }

func (m *Manager) Valid(ctx context.Context, userID int64, sid string) (bool, error) {
	if sid == "" {
		return false, nil
	}
	if m == nil || m.Store == nil {
		return false, errors.New("session store is not configured")
	}
	want := strconv.FormatInt(userID, 10)

	if client := redis.Client(); client != nil && !m.CacheDisabled {
		vals, err := client.MGet(ctx, revokedKey(sid), aliveKey(sid)).Result()
		if err != nil {
			logger.Persistent("session.cache.read", 3, 10*time.Minute, "Session cache read failed, using database sid=%s err=%v", sid, err)
		} else if len(vals) == 2 {
			logger.ResetPersistent("session.cache.read")
			if vals[0] != nil {
				return false, nil
			}
			if s, ok := vals[1].(string); ok && s == want {
				return true, nil
			}
		}
	}

	alive, err := m.Store.Alive(ctx, userID, sid)
	if err != nil {
		return false, fmt.Errorf("session lookup uid=%d sid=%s: %w", userID, sid, err)
	}
	if alive {
		m.cache(ctx, userID, sid)
	}
	return alive, nil
}

func (m *Manager) cache(ctx context.Context, userID int64, sid string) {
	client := redis.Client()
	if client == nil || m.CacheDisabled {
		return
	}
	if err := client.Set(ctx, aliveKey(sid), strconv.FormatInt(userID, 10), m.ttl()).Err(); err != nil {
		logger.Persistent("session.cache.write", 3, 10*time.Minute, "Session cache write failed uid=%d sid=%s err=%v", userID, sid, err)
		return
	}
	logger.ResetPersistent("session.cache.write")
}

func (m *Manager) Touch(ctx context.Context, userID int64, sid string) {
	if sid == "" {
		return
	}
	m.cache(ctx, userID, sid)
}

func (m *Manager) Revoke(ctx context.Context, userID int64, sid string) error {
	if m == nil || m.Store == nil {
		return errors.New("session store is not configured")
	}
	if sid == "" {
		return errors.New("session id is empty")
	}
	markErr := m.mark(ctx, sid)
	if err := m.Store.Revoke(ctx, userID, sid); err != nil {
		return fmt.Errorf("revoke session uid=%d sid=%s: %w", userID, sid, err)
	}
	if markErr != nil {
		return fmt.Errorf("session uid=%d sid=%s removed from database, cache may serve it up to %s: %w", userID, sid, m.ttl(), markErr)
	}
	return nil
}

func (m *Manager) RevokeAll(ctx context.Context, userID int64, except string) error {
	if m == nil || m.Store == nil {
		return errors.New("session store is not configured")
	}
	sids, err := m.Store.RevokeAll(ctx, userID, except)
	if err != nil {
		return fmt.Errorf("revoke all sessions uid=%d: %w", userID, err)
	}
	var firstErr error
	for _, sid := range sids {
		if merr := m.mark(ctx, sid); merr != nil && firstErr == nil {
			firstErr = merr
		}
	}
	if firstErr != nil {
		return fmt.Errorf("sessions uid=%d removed from database, cache may serve some up to %s: %w", userID, m.ttl(), firstErr)
	}
	return nil
}

func (m *Manager) mark(ctx context.Context, sid string) error {
	if m.CacheDisabled {
		return nil
	}
	client := redis.Client()
	if client == nil {
		return errors.New("redis unavailable")
	}
	pipe := client.TxPipeline()
	pipe.Set(ctx, revokedKey(sid), "1", 2*m.ttl())
	pipe.Del(ctx, aliveKey(sid))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		logger.Persistent("session.cache.revoke", 3, 10*time.Minute, "Session cache revoke failed sid=%s err=%v", sid, err)
		return err
	}
	logger.ResetPersistent("session.cache.revoke")
	return nil
}
