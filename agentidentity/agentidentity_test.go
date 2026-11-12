package agentidentity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE agent_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		customer_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL,
		confirm_hash TEXT,
		name TEXT,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		confirmed_at DATETIME,
		expires_at DATETIME,
		last_used_at DATETIME,
		revoked_at DATETIME)`)
	if err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db, Table: "agent_tokens", SubjectCol: "customer_id", Prefix: "tst_"}
}

func TestRequestConfirmResolveRevoke(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	token, confirm, err := s.Request(ctx, 7, "Claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve(ctx, token); ok {
		t.Fatal("pending token must not resolve")
	}
	if _, _, err := s.Request(ctx, 7, "again"); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("want ErrTooSoon, got %v", err)
	}
	subject, err := s.Confirm(ctx, confirm)
	if err != nil || subject != 7 {
		t.Fatalf("confirm: %v %d", err, subject)
	}
	if _, err := s.Confirm(ctx, confirm); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second confirm should fail, got %v", err)
	}
	got, ok := s.Resolve(ctx, token)
	if !ok || got != 7 {
		t.Fatalf("resolve: %v %d", ok, got)
	}
	list, err := s.List(ctx, 7)
	if err != nil || len(list) != 1 || list[0].Name != "Claude" || !list[0].LastUsedAt.Valid {
		t.Fatalf("list: %v %+v", err, list)
	}
	if err := s.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve(ctx, token); ok {
		t.Fatal("revoked token must not resolve")
	}
	if err := s.Revoke(ctx, token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second revoke: %v", err)
	}
}

func TestExpiredPendingAndForeignRevoke(t *testing.T) {
	s := newStore(t)
	s.PendingTTL = time.Millisecond
	ctx := context.Background()
	_, confirm, err := s.Request(ctx, 1, "x")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := s.Confirm(ctx, confirm); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired confirm: %v", err)
	}
	tok, err := s.Mint(ctx, 1, "minted")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve(ctx, tok); !ok {
		t.Fatal("minted token should resolve")
	}
	list, _ := s.List(ctx, 1)
	if len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	if err := s.RevokeByID(ctx, 2, list[0].ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign revoke: %v", err)
	}
	if err := s.RevokeByID(ctx, 1, list[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestBadStore(t *testing.T) {
	s := &Store{}
	if _, _, err := s.Request(context.Background(), 1, ""); !errors.Is(err, ErrBadStore) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Resolve(context.Background(), "x"); ok {
		t.Fatal("unconfigured store resolved")
	}
}
