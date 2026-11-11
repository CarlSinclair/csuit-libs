package passkeys

import (
	"bytes"
	"database/sql"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func svc(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, webauthn_id BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email) VALUES (7, 'a@x'), (8, 'b@x')`); err != nil {
		t.Fatal(err)
	}
	return &Service{DisplayName: "Test", DB: func() *sql.DB { return db }, Dialect: SQLite}
}

func TestHandleIsStoredStableAndDiffersBetweenUsers(t *testing.T) {
	s := svc(t)
	a, err := s.userHandle(7)
	if err != nil || len(a) != 32 {
		t.Fatalf("handle for user 7: %v len=%d", err, len(a))
	}
	again, err := s.userHandle(7)
	if err != nil || !bytes.Equal(a, again) {
		t.Fatalf("the same user must always get the stored handle: %v", err)
	}
	other, err := s.userHandle(8)
	if err != nil || bytes.Equal(a, other) {
		t.Fatalf("two users must not share a handle: %v", err)
	}
}

func TestRelyingPartyFollowsTheRequestHost(t *testing.T) {
	s := svc(t)
	cases := map[string]string{"shop.example.com": "shop.example.com", "shop.example.com:8443": "shop.example.com"}
	for host, wantID := range cases {
		r := httptest.NewRequest("GET", "https://"+host+"/", nil)
		wa, err := s.rpFor(r)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if wa.Config.RPID != wantID {
			t.Errorf("%s: RPID = %q, want %q", host, wa.Config.RPID, wantID)
		}
		if len(wa.Config.RPOrigins) != 1 || wa.Config.RPOrigins[0] != "https://"+host {
			t.Errorf("%s: origins = %v", host, wa.Config.RPOrigins)
		}
	}
}

func TestSessionDeviceLabel(t *testing.T) {
	cases := []struct{ ua, want string }{
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36", "Chrome / Linux"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36 Edg/126.0", "Edge / Windows"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1", "Safari / iOS"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0", "Firefox / Linux"},
		{"curl/8.0", ""},
	}
	for _, c := range cases {
		if got := (SessionInfo{UserAgent: c.ua}).Device(); got != c.want {
			t.Errorf("%q: got %q, want %q", c.ua, got, c.want)
		}
	}
}
