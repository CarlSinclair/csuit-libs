package redis

import (
	"context"
	"fmt"
	"github.com/CarlSinclair/csuit-libs/logger"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type alertStateStore struct{ name string }

func AlertStateStore(name string) logger.AlertStateStore { return alertStateStore{name: name} }

func (a alertStateStore) Load() ([]byte, error) {
	c := Client()
	if c == nil {
		return nil, fmt.Errorf("redis is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := c.Get(ctx, Key(a.name)).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get %s: %w", Key(a.name), err)
	}
	return raw, nil
}

func (a alertStateStore) Save(raw []byte) error {
	c := Client()
	if c == nil {
		return fmt.Errorf("redis is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Set(ctx, Key(a.name), raw, 0).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", Key(a.name), err)
	}
	return nil
}
