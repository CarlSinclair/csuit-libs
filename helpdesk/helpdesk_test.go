package helpdesk

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE tickets (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, order_id TEXT, subject TEXT NOT NULL, status TEXT DEFAULT 'open', priority TEXT DEFAULT 'medium', source TEXT DEFAULT 'customer', created_at DATETIME, updated_at DATETIME);
CREATE TABLE ticket_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, ticket_id INTEGER NOT NULL, user_id INTEGER NOT NULL, author_kind TEXT DEFAULT 'user', message TEXT NOT NULL, is_internal BOOLEAN DEFAULT 0, created_at DATETIME);
`

func newSvc(t *testing.T, h Hooks) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DB: db, Hooks: h})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("nil DB accepted")
	}
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	if _, err := New(Config{DB: db, RefColumn: "x; DROP TABLE tickets"}); err == nil {
		t.Error("injectable column accepted")
	}
}

func TestCreateReplyGet(t *testing.T) {
	var notified int64
	s := newSvc(t, Hooks{NotifyStaff: func(id int64, ref string) { notified = id }})
	ctx := context.Background()
	id, err := s.Create(ctx, CreateParams{PartyID: 7, Ref: "o1", Subject: "Hi", Body: "help"})
	if err != nil || id == 0 || notified != id {
		t.Fatalf("create id=%d err=%v notified=%d", id, err, notified)
	}
	if _, err := s.Create(ctx, CreateParams{PartyID: 0, Subject: "x", Body: "y"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid create err=%v", err)
	}
	if err := s.Reply(ctx, id, 1, AuthorUser, "internal note", true); err != nil {
		t.Fatal(err)
	}
	tk, err := s.Get(ctx, id)
	if err != nil || len(tk.Messages) != 2 || tk.Status != StatusOpen || tk.Ref != "o1" || tk.Source != SourceCustomer {
		t.Fatalf("ticket=%+v err=%v", tk, err)
	}
	if !tk.Messages[1].Internal {
		t.Error("internal flag lost")
	}
	if err := s.Reply(ctx, id, 1, AuthorUser, "public answer", false); err != nil {
		t.Fatal(err)
	}
	if tk, _ = s.Get(ctx, id); tk.Status != StatusAwaitingUser {
		t.Errorf("staff reply status=%s", tk.Status)
	}
	if err := s.Reply(ctx, id, 7, AuthorParty, "thanks", false); err != nil {
		t.Fatal(err)
	}
	if tk, _ = s.Get(ctx, id); tk.Status != StatusOpen {
		t.Errorf("customer reply status=%s", tk.Status)
	}
	if err := s.Reply(ctx, 999, 1, AuthorUser, "x", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("reply missing err=%v", err)
	}
	if err := s.Reply(ctx, id, 1, AuthorUser, "   ", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty reply err=%v", err)
	}
	if _, err := s.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing err=%v", err)
	}
}

func TestSetStatusAndList(t *testing.T) {
	s := newSvc(t, Hooks{})
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateParams{PartyID: 1, Subject: "a", Body: "a"})
	s.Create(ctx, CreateParams{PartyID: 2, Subject: "b", Body: "b"})
	if err := s.SetStatus(ctx, a, "bogus"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bogus status err=%v", err)
	}
	if err := s.SetStatus(ctx, 999, StatusClosed); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing err=%v", err)
	}
	if err := s.SetStatus(ctx, a, StatusClosed); err != nil {
		t.Fatal(err)
	}
	all, total, err := s.List(ctx, "", 10, 0)
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("all=%d total=%d err=%v", len(all), total, err)
	}
	closed, total, _ := s.List(ctx, StatusClosed, 10, 0)
	if total != 1 || len(closed) != 1 || closed[0].ID != a {
		t.Errorf("closed=%v total=%d", closed, total)
	}
}

func tr(_ int64, key string) string {
	switch key {
	case "cancel_request_ticket_subject_cancel", "cancel_request_ticket_subject_return":
		return "Request %s"
	case "cancel_request_ticket_content":
		return "%s|%s|%s"
	case "cancel_request_reply_handled", "cancel_request_reply_declined":
		return "done %s"
	}
	return key
}

func TestCancelRequestFlow(t *testing.T) {
	var partyMsg string
	var acts []string
	s := newSvc(t, Hooks{
		Translate:   tr,
		NotifyParty: func(id, party int64, m, ev string) error { partyMsg = m; return nil },
		Activity:    func(actor int64, action, target string, meta map[string]any) { acts = append(acts, action) },
	})
	ctx := context.Background()
	id, err := s.CreateCancelRequest(ctx, "o9", "#9", 5, KindCancel, SourceAgent, "", "PAID")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateCancelRequest(ctx, "o9", "#9", 5, KindCancel, SourceCustomer, "x", "PAID")
	if err != nil || again != id {
		t.Fatalf("duplicate not deduped: %d vs %d err=%v", again, id, err)
	}
	if n, _ := s.CountPending(ctx); n != 1 {
		t.Errorf("pending=%d", n)
	}
	tk, _ := s.Get(ctx, id)
	if tk.Source != SourceAgent || tk.Subject != "Request #9" || !strings.Contains(tk.Messages[0].Body, "PAID") || tk.Messages[0].AuthorKind != AuthorParty {
		t.Errorf("ticket=%+v", tk)
	}
	if got := s.FirstMessage(ctx, id); got != tk.Messages[0].Body {
		t.Errorf("first message %q", got)
	}
	pend, err := s.PendingCancelRequests(ctx, 10)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending=%v err=%v", pend, err)
	}
	if err := s.ResolveCancelRequest(ctx, id, 2, "nope", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad action err=%v", err)
	}
	if err := s.ResolveCancelRequest(ctx, id, 2, ActionDeclined, "policy"); err != nil {
		t.Fatal(err)
	}
	if partyMsg != "done policy" || len(acts) != 1 || acts[0] != "cancel_request_declined" {
		t.Errorf("partyMsg=%q acts=%v", partyMsg, acts)
	}
	if err := s.ResolveCancelRequest(ctx, id, 2, ActionHandled, ""); !errors.Is(err, ErrCancelNotFound) {
		t.Errorf("double resolve err=%v", err)
	}
	if n, _ := s.CountPending(ctx); n != 0 {
		t.Errorf("pending after resolve=%d", n)
	}
}

func TestResolveSurfacesNotifyFailure(t *testing.T) {
	s := newSvc(t, Hooks{Translate: tr, NotifyParty: func(int64, int64, string, string) error { return errors.New("smtp down") }})
	ctx := context.Background()
	id, _ := s.CreateCancelRequest(ctx, "o1", "#1", 3, KindReturn, SourceCustomer, "r", "PAID")
	err := s.ResolveCancelRequest(ctx, id, 1, ActionHandled, "")
	var ne *NotifyError
	if !errors.As(err, &ne) {
		t.Fatalf("expected NotifyError, got %v", err)
	}
	tk, _ := s.Get(ctx, id)
	if tk.Status != StatusClosed {
		t.Errorf("status=%s", tk.Status)
	}
}

func TestCloseByRefs(t *testing.T) {
	s := newSvc(t, Hooks{Translate: tr})
	ctx := context.Background()
	a, _ := s.CreateCancelRequest(ctx, "o1", "#1", 1, KindCancel, SourceCustomer, "r", "S")
	b, _ := s.CreateCancelRequest(ctx, "o2", "#2", 1, KindCancel, SourceCustomer, "r", "S")
	if err := s.CloseByRefs(ctx, []string{"o1"}); err != nil {
		t.Fatal(err)
	}
	ta, _ := s.Get(ctx, a)
	tb, _ := s.Get(ctx, b)
	if ta.Status != StatusClosed || tb.Status != StatusActionRequired {
		t.Errorf("a=%s b=%s", ta.Status, tb.Status)
	}
}
