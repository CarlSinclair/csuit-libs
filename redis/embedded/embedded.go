// Package embedded runs a small in-process Redis-compatible server so an
// application that has no Redis of its own can still use every feature of the
// shared packages that are written against Redis (sign-in tokens, rate limits,
// idempotency, queues). The features run their normal code paths against it.
//
// It is opt-in and meant for single-process deployments. Everything it holds
// lives in memory and is lost when the process exits, and it cannot be shared
// between processes. Applications that run a real Redis must not use it.
package embedded

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"

	"github.com/alicebob/miniredis/v2"
)

const tick = time.Second

// Start launches the server on a random loopback port, protected by a random
// password, and returns redis options that point the shared client at it plus
// a stop function. Key expiry is driven by a clock that advances in real time.
func Start(prefix string) (redis.Options, func(), error) {
	srv, err := miniredis.Run()
	if err != nil {
		return redis.Options{}, nil, fmt.Errorf("start embedded redis: %w", err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		srv.Close()
		return redis.Options{}, nil, fmt.Errorf("embedded redis password: %w", err)
	}
	password := hex.EncodeToString(raw)
	srv.RequireAuth(password)

	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				srv.FastForward(tick)
				srv.SetTime(time.Now())
			}
		}
	}()
	stop := func() {
		once.Do(func() {
			close(done)
			srv.Close()
		})
	}
	logger.Info("Redis: using the embedded in-memory server addr=%s (state is lost on restart)", srv.Addr())
	return redis.Options{
		DefaultPrefix: prefix,
		Settings: func() (redis.Settings, error) {
			return redis.Settings{Addr: srv.Addr(), Password: password, Prefix: prefix}, nil
		},
	}, stop, nil
}
