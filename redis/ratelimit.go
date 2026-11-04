package redis

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	goredis "github.com/redis/go-redis/v9"
)

type RateLimitConfig struct {
	Limit     int
	Window    time.Duration
	Whitelist []string
}

var RateLimitConfigFn func() RateLimitConfig

var rateScript = goredis.NewScript(`
local c = redis.call("INCR", KEYS[1])
if c == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {c, ttl}
`)

func rateConfig() RateLimitConfig {
	cfg := RateLimitConfig{Limit: 100, Window: 60 * time.Second}
	if RateLimitConfigFn != nil {
		c := RateLimitConfigFn()
		if c.Limit >= 0 {
			cfg.Limit = c.Limit
		}
		if c.Window > 0 {
			cfg.Window = c.Window
		}
		cfg.Whitelist = c.Whitelist
	}
	return cfg
}

func AllowPublic(key string) (bool, int, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return true, 0, nil
	}
	cfg := rateConfig()
	for _, w := range cfg.Whitelist {
		if strings.TrimSpace(w) == key {
			return true, 0, nil
		}
	}
	if cfg.Limit <= 0 {
		return true, 0, nil
	}
	return allow("ratelimit:public:"+key, cfg.Limit, cfg.Window)
}

func Allow(key string, limit int, window time.Duration) (bool, int, error) {
	key = strings.TrimSpace(key)
	if key == "" || limit <= 0 {
		return true, 0, nil
	}
	if window <= 0 {
		window = 60 * time.Second
	}
	return allow("ratelimit:"+key, limit, window)
}

func allow(name string, limit int, window time.Duration) (bool, int, error) {
	key := name
	client := Client()
	if client == nil {
		logger.Persistent("redis.ratelimit", 3, 10*time.Minute, "RateLimiter: redis unavailable, allowing request key=%s", key)
		return true, 0, nil
	}

	res, err := rateScript.Run(Ctx, client, []string{Key(name)}, window.Milliseconds()).Result()
	if err != nil {
		logger.Persistent("redis.ratelimit", 3, 10*time.Minute, "RateLimiter: script failed, allowing request key=%s err=%v", key, err)
		return true, 0, err
	}
	logger.ResetPersistent("redis.ratelimit")

	arr, ok := res.([]interface{})
	if !ok || len(arr) != 2 {
		logger.Error("RateLimiter: unexpected script result key=%s result=%#v", key, res)
		return true, 0, nil
	}
	count := toInt64(arr[0])
	ttlMs := toInt64(arr[1])
	if count <= int64(limit) {
		return true, 0, nil
	}
	retryAfter := int(math.Ceil(float64(ttlMs) / 1000.0))
	if retryAfter < 1 {
		retryAfter = 1
	}
	return false, retryAfter, nil
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			logger.Warn("RateLimiter: unparseable numeric value %q: %v", t, err)
		}
		return n
	}
	return 0
}
