package embedded

import (
	"testing"
	"time"

	"github.com/CarlSinclair/csuit-libs/redis"
)

func TestEmbeddedServesTheSharedClientAndExpiresKeys(t *testing.T) {
	opts, stop, err := Start("emb:")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	redis.Configure(opts)
	redis.Init("")
	c := redis.Client()
	if c == nil {
		t.Fatal("shared client must connect to the embedded server")
	}
	key := redis.Key("probe")
	if err := c.Set(redis.Ctx, key, "1", 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Eval(redis.Ctx, `return redis.call('GET', KEYS[1])`, []string{key}).Text(); err != nil || v != "1" {
		t.Fatalf("lua read = %q %v", v, err)
	}
	time.Sleep(3500 * time.Millisecond)
	if n, err := c.Exists(redis.Ctx, key).Result(); err != nil || n != 0 {
		t.Fatalf("key must expire in real time: exists=%d err=%v", n, err)
	}
}
