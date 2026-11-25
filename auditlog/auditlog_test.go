package auditlog

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStandardSchemaRoundTrip(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(Schema("sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users VALUES(1,'Ana','ana@example.com')`); err != nil {
		t.Fatal(err)
	}
	l, err := New(Config{DB: db, TimeUnix: true, UsersTable: "users", UsersIDCol: "id", UsersLabelExpr: "COALESCE(NULLIF(u.name,''),u.email)"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("CF-IPCity", "Recife")
	r.Header.Set("CF-IPCountry", "br")
	r.Header.Set("User-Agent", "test-agent")
	l.Record(r, 1, "course_created", "entity", "7", "title", "Go 101", "paid", "true")
	l.Record(nil, 0, "login_failed", "status", StatusWarn)
	got, total, err := l.List(context.Background(), Filter{}, 10, 0)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("list: %v total=%d len=%d", err, total, len(got))
	}
	first := got[1]
	if first.UserLabel != "Ana" || first.ResourceType != "course" || first.EntityID != "7" || first.Location != "Recife, BR" || first.UserAgent != "test-agent" || first.Details["title"] != "Go 101" || first.At.IsZero() {
		t.Fatalf("unexpected entry: %+v", first)
	}
	if got[0].Status != StatusWarn {
		t.Fatalf("status = %q", got[0].Status)
	}
	if _, total, _ := l.List(context.Background(), Filter{Q: "Ana"}, 10, 0); total != 1 {
		t.Fatalf("search by user name found %d", total)
	}
	if _, total, _ := l.List(context.Background(), Filter{Q: "%"}, 10, 0); total != 0 {
		t.Fatalf("percent must be literal, found %d", total)
	}
	if _, total, _ := l.List(context.Background(), Filter{From: time.Now().Add(time.Hour)}, 10, 0); total != 0 {
		t.Fatalf("future filter found %d", total)
	}
}

func TestLegacyMappedTable(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, action TEXT, entity_id TEXT, location TEXT, details TEXT, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	l, err := New(Config{DB: db, Cols: Columns{ResourceType: "-", Status: "-", UserAgent: "-", CreatedAt: "timestamp"}})
	if err != nil {
		t.Fatal(err)
	}
	l.Record(nil, 5, "order_refund", "entity", "9", "note", "x")
	got, _, err := l.List(context.Background(), Filter{}, 5, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %d", err, len(got))
	}
	if got[0].ResourceType != "order" || got[0].At.IsZero() || got[0].Details["note"] != "x" {
		t.Fatalf("unexpected: %+v", got[0])
	}
}

func TestFreeTextDetailsAndPage(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, bot_id INTEGER, action TEXT, status TEXT, message TEXT, location TEXT, user_agent TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO audit_logs(user_id,bot_id,action,status,message) VALUES(3,4,'bot_start','error','boom <b>')`); err != nil {
		t.Fatal(err)
	}
	l, err := New(Config{DB: db, Cols: Columns{ResourceType: "-", EntityID: "-", Details: "message", ScopeID: "bot_id"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/admin/audit?status=error&scope=4", nil)
	o := PageOptions{Base: "/admin/audit", ShowStatus: true}
	v := l.Page(r, o)
	if v.Err || len(v.Rows) != 1 || v.Rows[0].Lines[0].Value != "boom <b>" {
		t.Fatalf("page: %+v", v)
	}
	html := string(Render(v, o))
	if strings.Contains(html, "boom <b>") || !strings.Contains(html, "boom &lt;b&gt;") {
		t.Fatalf("details must be escaped: %s", html)
	}
	if !strings.Contains(html, "modal-audit-1") {
		t.Fatalf("modal missing")
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	if _, err := New(Config{DB: testDB(t), Table: "a;drop"}); err == nil {
		t.Fatal("unsafe table accepted")
	}
	if _, err := New(Config{DB: testDB(t), Cols: Columns{Action: "x y"}}); err == nil {
		t.Fatal("unsafe column accepted")
	}
}
