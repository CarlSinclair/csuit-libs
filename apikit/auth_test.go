package apikit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStore struct {
	keys    map[string]*Key
	touched int
	err     error
}

func (f *fakeStore) Insert(context.Context, Key, string) (int64, error) { return 0, nil }
func (f *fakeStore) Lookup(_ context.Context, hash string) (*Key, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.keys[hash], nil
}
func (f *fakeStore) Touch(context.Context, int64, string, time.Time) error { f.touched++; return nil }
func (f *fakeStore) List(context.Context, int64) ([]Key, error)            { return nil, nil }
func (f *fakeStore) Revoke(context.Context, int64, int64, time.Time) (bool, error) {
	return false, nil
}

func newAuth(keys map[string]Key, enabled bool) (*Authenticator, *fakeStore) {
	st := &fakeStore{keys: map[string]*Key{}}
	for raw, k := range keys {
		kc := k
		st.keys[HashToken(raw)] = &kc
	}
	return New(Config{Store: st, Enabled: func() bool { return enabled }}), st
}

func call(h http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWrapAuthOutcomes(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	auth, st := newAuth(map[string]Key{
		"good":    {ID: 1, Scopes: []string{"orders:read"}},
		"writer":  {ID: 2, Scopes: []string{"orders:write"}},
		"other":   {ID: 3, Scopes: []string{"customers:read"}},
		"revoked": {ID: 4, Scopes: []string{"orders:read"}, RevokedAt: &past},
		"expired": {ID: 5, Scopes: []string{"orders:read"}, ExpiresAt: &past},
	}, true)
	ran := 0
	h := auth.Wrap("orders:read", func(w http.ResponseWriter, r *http.Request, k *Key) {
		ran++
		if got, ok := FromContext(r.Context()); !ok || got.ID != k.ID {
			t.Errorf("key missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	cases := []struct {
		token string
		want  int
	}{
		{"", http.StatusUnauthorized},
		{"nope", http.StatusUnauthorized},
		{"revoked", http.StatusUnauthorized},
		{"expired", http.StatusUnauthorized},
		{"other", http.StatusForbidden},
		{"good", http.StatusNoContent},
		{"writer", http.StatusNoContent},
	}
	for _, c := range cases {
		if got := call(h, c.token).Code; got != c.want {
			t.Errorf("token %q: status %d, want %d", c.token, got, c.want)
		}
	}
	if ran != 2 {
		t.Errorf("handler ran %d times, want 2", ran)
	}
	if st.touched != 2 {
		t.Errorf("touch called %d times, want 2", st.touched)
	}
}

func TestWrapDisabledAndStoreError(t *testing.T) {
	auth, _ := newAuth(map[string]Key{"good": {ID: 1, Scopes: []string{"a:read"}}}, false)
	h := auth.Wrap("a:read", func(w http.ResponseWriter, r *http.Request, k *Key) { w.WriteHeader(http.StatusNoContent) })
	if got := call(h, "good").Code; got != http.StatusForbidden {
		t.Errorf("disabled API: status %d, want 403", got)
	}
	auth, st := newAuth(nil, true)
	st.err = context.DeadlineExceeded
	h = auth.Wrap("a:read", func(w http.ResponseWriter, r *http.Request, k *Key) { w.WriteHeader(http.StatusNoContent) })
	if got := call(h, "good").Code; got != http.StatusInternalServerError {
		t.Errorf("store failure: status %d, want 500", got)
	}
}

func TestPrincipal(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	auth, _ := newAuth(map[string]Key{
		"good":    {ID: 1, Scopes: []string{"a:read"}},
		"revoked": {ID: 2, Scopes: []string{"a:read"}, RevokedAt: &past},
		"expired": {ID: 3, Scopes: []string{"a:read"}, ExpiresAt: &past},
	}, true)
	req := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return r
	}
	if k := auth.Principal(req("good")); k == nil || k.ID != 1 {
		t.Errorf("good key not resolved: %v", k)
	}
	for _, tok := range []string{"", "bad", "revoked", "expired"} {
		if k := auth.Principal(req(tok)); k != nil {
			t.Errorf("token %q resolved to key %d", tok, k.ID)
		}
	}
	off, _ := newAuth(map[string]Key{"good": {ID: 1}}, false)
	if off.Principal(req("good")) != nil {
		t.Error("principal resolved while API disabled")
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":   "abc",
		"bearer abc":   "abc",
		"BEARER  abc ": "abc",
		"Basic abc":    "",
		"Bearer":       "",
		"":             "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := BearerToken(r); got != want {
			t.Errorf("header %q: got %q, want %q", header, got, want)
		}
	}
}

func TestWildcardOnlyGrantsWhenStored(t *testing.T) {
	if _, err := NormalizeScopes([]string{"*"}, []string{"orders:read"}); err == nil {
		t.Error("wildcard scope accepted although not in the allowed list")
	}
	if _, err := NormalizeScopes([]string{"orders:cancel"}, []string{"orders:read", "orders:write"}); err == nil {
		t.Error("unlisted verb accepted")
	}
}
