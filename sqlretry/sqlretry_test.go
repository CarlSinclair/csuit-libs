package sqlretry

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestIsBusyIgnoresOtherErrors(t *testing.T) {
	if IsBusy(nil) {
		t.Fatal("nil is not busy")
	}
	if IsBusy(errors.New("syntax error")) {
		t.Fatal("a plain error is not busy")
	}
}

func TestOpenReadWriteAndConcurrentWriters(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db") + "?_pragma=busy_timeout(1)"
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := db.Exec(`INSERT INTO t (v) VALUES ('x')`)
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent insert failed despite retry: %v", err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 8 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}
