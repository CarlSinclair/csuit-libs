package sessionstore

import (
	"errors"

	"github.com/CarlSinclair/csuit-libs/redis"
)

var ErrRedisNotConfigured = errors.New("redis is not configured: authentication is disabled until it is")

func RedisConfigured() bool { return redis.Client() != nil }
