package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	goredis "github.com/redis/go-redis/v9"
)

type Settings struct {
	Addr     string
	Password string
	DB       int
	Prefix   string
}

type Options struct {
	Settings      func() (Settings, error)
	DefaultPrefix string
}

var (
	RDB    *goredis.Client
	Ctx    = context.Background()
	Prefix string
)

var (
	redisMu     sync.RWMutex
	activeAddr  string
	opts        Options
	reconnectMu sync.Mutex
	reconnecter bool
)

const reconnectInterval = 10 * time.Second

func Configure(o Options) {
	redisMu.Lock()
	opts = o
	redisMu.Unlock()
}

func currentOptions() Options {
	redisMu.RLock()
	defer redisMu.RUnlock()
	return opts
}

func readSettings(addrOverride string) (Settings, error) {
	o := currentOptions()
	if o.Settings == nil {
		return Settings{}, errors.New("redis settings source is not configured")
	}
	s, err := o.Settings()
	if err != nil {
		return Settings{}, fmt.Errorf("read redis settings: %w", err)
	}
	if v := strings.TrimSpace(addrOverride); v != "" {
		s.Addr = v
	}
	s.Addr = strings.TrimSpace(s.Addr)
	if s.Addr == "" {
		return Settings{}, errors.New("redis address is not set")
	}
	if s.DB < 0 {
		return Settings{}, fmt.Errorf("redis db %d is invalid", s.DB)
	}
	s.Prefix = o.DefaultPrefix + strings.TrimSpace(s.Prefix)
	return s, nil
}

func setClient(client *goredis.Client, addr string) {
	redisMu.Lock()
	old := RDB
	RDB = client
	activeAddr = addr
	redisMu.Unlock()
	if old != nil && old != client {
		if err := old.Close(); err != nil {
			logger.Warn("Redis: closing previous client failed: %v", err)
		}
	}
}

func Client() *goredis.Client {
	redisMu.RLock()
	client := RDB
	redisMu.RUnlock()
	return client
}

func ActiveAddr() string {
	redisMu.RLock()
	defer redisMu.RUnlock()
	return activeAddr
}

func connect(s Settings) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:         s.Addr,
		Password:     s.Password,
		DB:           s.DB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolTimeout:  5 * time.Second,
		MaxRetries:   1,
	})
	if err := client.Ping(Ctx).Err(); err != nil {
		if cerr := client.Close(); cerr != nil {
			logger.Warn("Redis: closing failed client addr=%s: %v", s.Addr, cerr)
		}
		return nil, err
	}
	return client, nil
}

func Init(addrOverride string) {
	s, err := readSettings(addrOverride)
	if err != nil {
		logger.Error("Redis Init: settings unavailable, retrying in background: %v", err)
		startReconnect(addrOverride)
		return
	}
	Prefix = s.Prefix
	client, err := connect(s)
	if err != nil {
		logger.Persistent("redis.init", 3, 10*time.Minute, "Redis Init: connection failed addr=%s prefix=%s, retrying in background err=%v", s.Addr, s.Prefix, err)
		startReconnect(addrOverride)
		return
	}
	setClient(client, s.Addr)
	logger.ResetPersistent("redis.init")
	logger.Info("Redis Init: connected addr=%s prefix=%s", s.Addr, s.Prefix)
}

func startReconnect(addrOverride string) {
	reconnectMu.Lock()
	if reconnecter {
		reconnectMu.Unlock()
		return
	}
	reconnecter = true
	reconnectMu.Unlock()

	go func() {
		defer func() {
			reconnectMu.Lock()
			reconnecter = false
			reconnectMu.Unlock()
		}()
		for {
			time.Sleep(reconnectInterval)
			if Client() != nil {
				return
			}
			s, err := readSettings(addrOverride)
			if err != nil {
				logger.Persistent("redis.reconnect", 3, 10*time.Minute, "Redis reconnect: settings unavailable: %v", err)
				continue
			}
			client, err := connect(s)
			if err != nil {
				logger.Persistent("redis.reconnect", 3, 10*time.Minute, "Redis reconnect: still down addr=%s err=%v", s.Addr, err)
				continue
			}
			Prefix = s.Prefix
			setClient(client, s.Addr)
			logger.ResetPersistent("redis.reconnect")
			logger.Info("Redis reconnect: connected addr=%s", s.Addr)
			return
		}
	}()
}

func Reconnect(reason error) error {
	reconnectMu.Lock()
	defer reconnectMu.Unlock()

	if reason != nil {
		logger.Warn("Redis Reconnect: attempting reconnect addr=%s reason=%v", ActiveAddr(), reason)
	}
	s, err := readSettings("")
	if err != nil {
		logger.Persistent("redis.reconnect", 3, 10*time.Minute, "Redis Reconnect: settings unavailable: %v", err)
		return err
	}
	client, err := connect(s)
	if err != nil {
		logger.Persistent("redis.reconnect", 3, 10*time.Minute, "Redis Reconnect: failed addr=%s err=%v", s.Addr, err)
		return err
	}
	Prefix = s.Prefix
	setClient(client, s.Addr)
	logger.ResetPersistent("redis.reconnect")
	logger.Info("Redis Reconnect: connected addr=%s", s.Addr)
	return nil
}

func EnsureInitialized(component string) error {
	component = strings.TrimSpace(component)
	if component == "" {
		component = "unknown"
	}
	if c := Client(); c != nil {
		if err := c.Ping(Ctx).Err(); err != nil {
			logger.Warn("Redis already initialized but ping failed component=%s addr=%s err=%v", component, ActiveAddr(), err)
			return err
		}
		return nil
	}
	if err := Reconnect(nil); err != nil {
		return fmt.Errorf("redis init component=%s: %w", component, err)
	}
	logger.Info("Redis initialized component=%s addr=%s", component, ActiveAddr())
	return nil
}

func Key(name string) string {
	return Prefix + name
}

func IsReady() bool {
	client := Client()
	if client == nil {
		logger.Persistent("redis.ready.nil", 3, 10*time.Minute, "Redis IsReady: client is nil")
		return false
	}
	if _, err := client.Ping(Ctx).Result(); err != nil {
		logger.Persistent("redis.ready.ping", 3, 10*time.Minute, "Redis IsReady: ping failed addr=%s err=%v", client.Options().Addr, err)
		return false
	}
	return true
}

func Diagnostics() string {
	client := Client()
	return fmt.Sprintf("active_addr=%s prefix=%s client_ready=%t", ActiveAddr(), Prefix, client != nil)
}

func Close() {
	if c := Client(); c != nil {
		if err := c.Close(); err != nil {
			logger.Warn("Redis Close: closing client failed: %v", err)
		}
	}
}

func CurrentPrefix() string {
	redisMu.RLock()
	defer redisMu.RUnlock()
	return Prefix
}
