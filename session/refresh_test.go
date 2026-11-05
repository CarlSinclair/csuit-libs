package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeRefresh struct {
	mu      sync.Mutex
	rows    map[string]*fakeRow
	findErr error
}

type fakeRow struct {
	uid        int64
	sid        string
	expired    bool
	supersedAt time.Time
	by         string
}

func (f *fakeRefresh) Find(_ context.Context, hash string) (RefreshRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return RefreshRecord{}, f.findErr
	}
	r, ok := f.rows[hash]
	if !ok {
		return RefreshRecord{}, ErrRefreshNotFound
	}
	rec := RefreshRecord{UserID: r.uid, SID: r.sid, Expired: r.expired, SupersededBy: r.by}
	if !r.supersedAt.IsZero() {
		rec.Superseded = true
		rec.SupersededAgo = time.Since(r.supersedAt)
	}
	return rec, nil
}

func (f *fakeRefresh) Rotate(_ context.Context, _ RefreshRecord, oldHash, newHash string, uid int64, sid string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.rows[oldHash]
	if !ok || !old.supersedAt.IsZero() {
		return false, nil
	}
	old.supersedAt = time.Now()
	old.by = newHash
	f.rows[newHash] = &fakeRow{uid: uid, sid: sid}
	return true, nil
}

var mintN int

func mintFn(uid int64, sid string) (Minted, error) {
	mintN++
	h := fmt.Sprintf("h%d", mintN)
	return Minted{Token: "tok-" + h, Hash: h, Expires: time.Now().Add(time.Hour)}, nil
}

func newRefresher(rows map[string]*fakeRow) (*Refresher, *fakeRefresh, *fakeStore) {
	st := &fakeStore{alive: map[string]int64{"s1": 1, "s2": 1, "o": 2}}
	fr := &fakeRefresh{rows: rows}
	return &Refresher{Store: fr, Sessions: &Manager{Store: st, CacheDisabled: true}}, fr, st
}

func yes() bool { return true }
func no() bool  { return false }

func TestRefreshRotatesOnce(t *testing.T) {
	r, fr, _ := newRefresher(map[string]*fakeRow{"a": {uid: 1, sid: "s1"}})
	out, err := r.Refresh(context.Background(), "a", no, mintFn)
	if err != nil || out.Kind != Rotated || out.NewToken == "" || out.SID != "s1" {
		t.Fatalf("got %+v %v", out, err)
	}
	if fr.rows["a"].supersedAt.IsZero() || fr.rows["a"].by == "" {
		t.Fatal("old token was not superseded")
	}
}

func TestRefreshUnknownAndExpired(t *testing.T) {
	r, _, _ := newRefresher(map[string]*fakeRow{"e": {uid: 1, sid: "s1", expired: true}})
	if _, err := r.Refresh(context.Background(), "missing", no, mintFn); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := r.Refresh(context.Background(), "e", no, mintFn); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("expired: %v", err)
	}
}

func TestRefreshStoreErrorIsNotInvalid(t *testing.T) {
	r, fr, _ := newRefresher(map[string]*fakeRow{})
	fr.findErr = errors.New("db down")
	_, err := r.Refresh(context.Background(), "a", no, mintFn)
	if err == nil || errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("a database failure must not read as an invalid token: %v", err)
	}
}

func TestRefreshConcurrentSingleWinner(t *testing.T) {
	r, _, _ := newRefresher(map[string]*fakeRow{"a": {uid: 1, sid: "s1"}})
	var wg sync.WaitGroup
	var mu sync.Mutex
	kinds := map[Kind]int{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := r.Refresh(context.Background(), "a", no, func(uid int64, sid string) (Minted, error) {
				mu.Lock()
				defer mu.Unlock()
				return mintFn(uid, sid)
			})
			if err != nil {
				t.Errorf("race loser errored: %v", err)
				return
			}
			mu.Lock()
			kinds[out.Kind]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if kinds[Rotated] != 1 || kinds[Absorbed] != 7 {
		t.Fatalf("want 1 rotated and 7 absorbed, got %v", kinds)
	}
}

func TestRefreshReuseOutsideGraceRevokesAll(t *testing.T) {
	r, _, st := newRefresher(map[string]*fakeRow{
		"old": {uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: "new"},
		"new": {uid: 1, sid: "s1"},
	})
	_, err := r.Refresh(context.Background(), "old", no, mintFn)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("want reuse error, got %v", err)
	}
	if len(st.alive) != 1 || st.alive["o"] != 2 {
		t.Fatalf("only the other user's session should survive: %v", st.alive)
	}
}

func TestRefreshHealsSameDevice(t *testing.T) {
	r, fr, st := newRefresher(map[string]*fakeRow{
		"old":  {uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: "mid"},
		"mid":  {uid: 1, sid: "s1", supersedAt: time.Now().Add(-4 * time.Minute), by: "head"},
		"head": {uid: 1, sid: "s1"},
	})
	out, err := r.Refresh(context.Background(), "old", yes, mintFn)
	if err != nil || out.Kind != Healed || out.NewToken == "" {
		t.Fatalf("got %+v %v", out, err)
	}
	if fr.rows["head"].supersedAt.IsZero() {
		t.Fatal("chain head must be superseded by the healed token")
	}
	if len(st.alive) != 3 {
		t.Fatalf("healing must not revoke sessions: %v", st.alive)
	}
}

func TestRefreshHealRefusesDifferentDeviceAndOldTokens(t *testing.T) {
	rows := func() map[string]*fakeRow {
		return map[string]*fakeRow{
			"old":  {uid: 1, sid: "s1", supersedAt: time.Now().Add(-48 * time.Hour), by: "head"},
			"head": {uid: 1, sid: "s1"},
		}
	}
	r, _, _ := newRefresher(rows())
	if _, err := r.Refresh(context.Background(), "old", yes, mintFn); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("token superseded beyond the heal window must revoke: %v", err)
	}
	rr := rows()
	rr["old"].supersedAt = time.Now().Add(-5 * time.Minute)
	r2, _, _ := newRefresher(rr)
	if _, err := r2.Refresh(context.Background(), "old", no, mintFn); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("a different device must not heal: %v", err)
	}
}

func TestRefreshHealRefusesBrokenAndLongChains(t *testing.T) {
	rows := map[string]*fakeRow{"old": {uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: "gone"}}
	r, _, _ := newRefresher(rows)
	if _, err := r.Refresh(context.Background(), "old", yes, mintFn); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("broken chain must revoke: %v", err)
	}
	long := map[string]*fakeRow{}
	for i := 0; i < 12; i++ {
		long[fmt.Sprintf("c%d", i)] = &fakeRow{uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: fmt.Sprintf("c%d", i+1)}
	}
	long["c12"] = &fakeRow{uid: 1, sid: "s1"}
	r2, _, _ := newRefresher(long)
	if _, err := r2.Refresh(context.Background(), "c0", yes, mintFn); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("chain longer than the cap must revoke: %v", err)
	}
}

func TestRefreshHealRefusesForeignUserInChain(t *testing.T) {
	rows := map[string]*fakeRow{
		"old":  {uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: "evil"},
		"evil": {uid: 2, sid: "o"},
	}
	r, _, _ := newRefresher(rows)
	if _, err := r.Refresh(context.Background(), "old", yes, mintFn); !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("a chain crossing users must revoke: %v", err)
	}
}

func TestRefreshWithinGraceIsAbsorbed(t *testing.T) {
	r, _, _ := newRefresher(map[string]*fakeRow{
		"old": {uid: 1, sid: "s1", supersedAt: time.Now().Add(-10 * time.Second), by: "new"},
		"new": {uid: 1, sid: "s1"},
	})
	out, err := r.Refresh(context.Background(), "old", no, mintFn)
	if err != nil || out.Kind != Absorbed || out.NewToken != "" {
		t.Fatalf("got %+v %v", out, err)
	}
}

type failingRevokeStore struct{ fakeStore }

func (f *failingRevokeStore) RevokeAll(_ context.Context, _ int64, _ string) ([]string, error) {
	return nil, errors.New("database is locked")
}

func TestRefreshReuseStillRejectsWhenRevocationFails(t *testing.T) {
	fr := &fakeRefresh{rows: map[string]*fakeRow{
		"old": {uid: 1, sid: "s1", supersedAt: time.Now().Add(-5 * time.Minute), by: "new"},
		"new": {uid: 1, sid: "s1"},
	}}
	r := &Refresher{Store: fr, Sessions: &Manager{Store: &failingRevokeStore{}, CacheDisabled: true}}
	_, err := r.Refresh(context.Background(), "old", no, mintFn)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("the stale token must still be rejected as reused: %v", err)
	}
	if err.Error() == ErrRefreshReused.Error() {
		t.Fatal("the revocation failure must stay visible in the error")
	}
}
