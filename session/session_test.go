package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/alicebob/miniredis/v2"
)

type fakeStore struct {
	alive    map[string]int64
	aliveErr error
	reads    int
}

func (f *fakeStore) Alive(_ context.Context, userID int64, sid string) (bool, error) {
	f.reads++
	if f.aliveErr != nil {
		return false, f.aliveErr
	}
	return f.alive[sid] == userID, nil
}

func (f *fakeStore) Revoke(_ context.Context, userID int64, sid string) error {
	delete(f.alive, sid)
	return nil
}

func (f *fakeStore) RevokeAll(_ context.Context, userID int64, except string) ([]string, error) {
	var out []string
	for sid, uid := range f.alive {
		if uid == userID && sid != except {
			delete(f.alive, sid)
			out = append(out, sid)
		}
	}
	return out, nil
}

func withRedis(t *testing.T) {
	t.Helper()
	mr := miniredis.RunT(t)
	redis.Configure(redis.Options{Settings: func() (redis.Settings, error) {
		return redis.Settings{Addr: mr.Addr(), Prefix: "s:"}, nil
	}})
	redis.Init("")
	t.Cleanup(redis.Close)
}

func TestValidUsesCacheThenDatabase(t *testing.T) {
	withRedis(t)
	st := &fakeStore{alive: map[string]int64{"sid1": 7}}
	m := &Manager{Store: st, CacheTTL: time.Minute}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ok, err := m.Valid(ctx, 7, "sid1")
		if err != nil || !ok {
			t.Fatalf("valid #%d: ok=%v err=%v", i, ok, err)
		}
	}
	if st.reads != 1 {
		t.Fatalf("database read %d times, want 1 (cache should serve the rest)", st.reads)
	}
	if ok, _ := m.Valid(ctx, 8, "sid1"); ok {
		t.Fatal("a cached session must not validate for another user")
	}
}

func TestRevokeBeatsCache(t *testing.T) {
	withRedis(t)
	st := &fakeStore{alive: map[string]int64{"sid1": 7}}
	m := &Manager{Store: st}
	ctx := context.Background()
	if ok, _ := m.Valid(ctx, 7, "sid1"); !ok {
		t.Fatal("session should start valid")
	}
	if err := m.Revoke(ctx, 7, "sid1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.Valid(ctx, 7, "sid1"); ok || err != nil {
		t.Fatalf("revoked session still valid: ok=%v err=%v", ok, err)
	}
}

func TestRevokeAllKeepsExcepted(t *testing.T) {
	withRedis(t)
	st := &fakeStore{alive: map[string]int64{"a": 7, "b": 7, "c": 9}}
	m := &Manager{Store: st}
	ctx := context.Background()
	if err := m.RevokeAll(ctx, 7, "a"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.Valid(ctx, 7, "a"); !ok {
		t.Fatal("excepted session must survive")
	}
	if ok, _ := m.Valid(ctx, 7, "b"); ok {
		t.Fatal("other session of the user must be revoked")
	}
	if ok, _ := m.Valid(ctx, 9, "c"); !ok {
		t.Fatal("another user's session must be untouched")
	}
}

func TestDatabaseErrorIsReportedNotTreatedAsInvalid(t *testing.T) {
	st := &fakeStore{aliveErr: errors.New("db down")}
	m := &Manager{Store: st}
	ok, err := m.Valid(context.Background(), 1, "sid")
	if ok || err == nil {
		t.Fatalf("a store failure must surface as an error: ok=%v err=%v", ok, err)
	}
}

func TestEmptySessionAndMissingStore(t *testing.T) {
	m := &Manager{Store: &fakeStore{}}
	if ok, err := m.Valid(context.Background(), 1, ""); ok || err != nil {
		t.Fatalf("empty sid: ok=%v err=%v", ok, err)
	}
	if _, err := (&Manager{}).Valid(context.Background(), 1, "x"); err == nil {
		t.Fatal("missing store must be an error")
	}
}
