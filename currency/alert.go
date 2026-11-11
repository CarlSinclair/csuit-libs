package currency

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"

	goredis "github.com/redis/go-redis/v9"
)

const (
	fetchAttempts = 3
	alertFailures = 2
	alertWindow   = 6 * time.Hour
	staleAlertAge = 36 * time.Hour
	persistKey    = "currency.refresh"
	redisPrefix   = "currency:rates:"
)

var fetchBackoff = []time.Duration{15 * time.Second, 60 * time.Second}

func refreshWithRetry(base string) (snapshot, error) {
	var snap snapshot
	err := logger.Retry(logger.RetryPolicy{
		Key:       persistKey,
		Attempts:  fetchAttempts,
		Backoff:   fetchBackoff,
		Threshold: alertFailures,
		Window:    alertWindow,
	}, "Currency fetch base="+base, func() error {
		var rerr error
		snap, rerr = refresh(base)
		return rerr
	})
	return snap, err
}

func reportFailure(what, base string, err error, alreadyCounted bool) {
	cacheMu.RLock()
	lastGood := cache[base].UpdatedAt
	cacheMu.RUnlock()
	if !lastGood.IsZero() && time.Since(lastGood) >= staleAlertAge {
		logger.Error("%s failed and rates are stale (last_good=%s, last rates still served): base=%s err=%v", what, lastGood.Format(time.RFC3339), base, err)
		return
	}
	if alreadyCounted {
		return
	}
	logger.Persistent(persistKey, alertFailures, alertWindow, "%s failed (last_good=%s, last rates still served): base=%s err=%v", what, lastGood.Format(time.RFC3339), base, err)
}

func persist(snap snapshot) {
	rdb := redis.Client()
	if rdb == nil {
		logger.Error("Currency rates not persisted, redis unavailable: base=%s", snap.Base)
		return
	}
	data, err := json.Marshal(snap)
	if err != nil {
		logger.Error("Currency rates not persisted, marshal failed: base=%s err=%v", snap.Base, err)
		return
	}
	if err := rdb.Set(redis.Ctx, redis.Key(redisPrefix+snap.Base), data, 0).Err(); err != nil {
		logger.Error("Currency rates not persisted, redis write failed: base=%s err=%v", snap.Base, err)
	}
}

func loadPersisted(base string) (snapshot, bool) {
	rdb := redis.Client()
	if rdb == nil {
		logger.Warn("Persisted currency rates unavailable, redis not connected: base=%s", base)
		return snapshot{}, false
	}
	raw, err := rdb.Get(redis.Ctx, redis.Key(redisPrefix+base)).Bytes()
	if err != nil {
		if !errors.Is(err, goredis.Nil) {
			logger.Error("Persisted currency rates read failed: base=%s err=%v", base, err)
		}
		return snapshot{}, false
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil || len(snap.Rates) == 0 {
		logger.Error("Persisted currency rates unreadable: base=%s err=%v", base, err)
		return snapshot{}, false
	}
	logger.Warn("Loaded persisted currency rates from redis: base=%s last_updated=%s", base, snap.UpdatedAt.Format(time.RFC3339))
	return snap, true
}
