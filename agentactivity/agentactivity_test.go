package agentactivity

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestList(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE audit_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, action TEXT, entity_id TEXT, details TEXT, created_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`(1, 'agent_booking_create', 'b1', '{"n":1}', '2026-01-01 10:00:00')`,
		`(1, 'login', 'b1', '', '2026-01-01 11:00:00')`,
		`(2, 'agent_booking_cancel', 'b1', 'notjson', '2026-01-01 12:00:00')`,
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO audit_logs (user_id, action, entity_id, details, created_at) VALUES ` + r); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db, Table: "audit_logs", TimeCol: "created_at", UserCol: "user_id"}
	ctx := context.Background()
	got, err := s.ListByEntity(ctx, "b1", 10)
	if err != nil || len(got) != 2 || got[0].Action != "booking_cancel" || got[1].Details["n"] == nil {
		t.Fatalf("entity: %v %+v", err, got)
	}
	got, err = s.ListByUser(ctx, 1, 10)
	if err != nil || len(got) != 1 || got[0].Action != "booking_create" {
		t.Fatalf("user: %v %+v", err, got)
	}
	if _, err := (&Store{}).ListByEntity(ctx, "x", 1); err == nil {
		t.Fatal("expected config error")
	}
}
