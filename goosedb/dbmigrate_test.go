package goosedb

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func memDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func cfg(db *sql.DB, files fstest.MapFS) Config {
	return Config{Name: "test", DB: db, Dialect: SQLite, FS: files, Dir: "migrations", Logf: func(string, ...any) {}}
}

var two = fstest.MapFS{
	"migrations/001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a(id INTEGER PRIMARY KEY);\n-- +goose Down\nDROP TABLE a;\n")},
	"migrations/002_b.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b(id INTEGER PRIMARY KEY);\n-- +goose Down\nDROP TABLE b;\n")},
}

func TestUpAppliesAndIsIdempotent(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg(db, two)); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := Up(ctx, cfg(db, two)); err != nil {
		t.Fatalf("second up: %v", err)
	}
	for _, tbl := range []string{"a", "b"} {
		if _, err := db.Exec("INSERT INTO " + tbl + " DEFAULT VALUES"); err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
	pending, err := Pending(ctx, cfg(db, two))
	if err != nil || pending {
		t.Fatalf("pending=%v err=%v, want false nil", pending, err)
	}
}

func TestUpRefusesDatabaseAheadOfBinary(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg(db, two)); err != nil {
		t.Fatalf("up: %v", err)
	}
	older := fstest.MapFS{"migrations/001_a.sql": two["migrations/001_a.sql"]}
	err := Up(ctx, cfg(db, older))
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestUpReportsFailingFile(t *testing.T) {
	db := memDB(t)
	bad := fstest.MapFS{"migrations/001_bad.sql": {Data: []byte("-- +goose Up\nCREATE TABLE nope(;\n")}}
	err := Up(context.Background(), cfg(db, bad))
	if err == nil || !strings.Contains(err.Error(), "001_bad.sql") {
		t.Fatalf("want error naming the file, got %v", err)
	}
}

func TestUpRejectsBadConfig(t *testing.T) {
	if err := Up(context.Background(), Config{Name: "x", Dialect: "oracle"}); err == nil {
		t.Fatal("want error for unknown dialect")
	}
	if err := Up(context.Background(), Config{Name: "x", Dialect: SQLite}); err == nil {
		t.Fatal("want error for missing DB and FS")
	}
}

func TestCheckNoAlter(t *testing.T) {
	ok := fstest.MapFS{"m/001.sql": {Data: []byte("-- ALTER TABLE in a comment\n-- +goose Up\nCREATE TABLE t(id INTEGER);\n")}}
	if err := CheckNoAlter(ok, "m"); err != nil {
		t.Fatalf("comment must pass: %v", err)
	}
	bad := fstest.MapFS{"m/002.sql": {Data: []byte("-- +goose Up\nalter  table t ADD COLUMN x TEXT;\n")}}
	err := CheckNoAlter(bad, "m")
	if err == nil || !strings.Contains(err.Error(), "m/002.sql:2") {
		t.Fatalf("want location of ALTER, got %v", err)
	}
}
