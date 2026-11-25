package notifykit

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *SQLStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(Schema("sqlite")); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLStore(db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type sent struct{ email, tg, push int }

func newDisp(t *testing.T, st *SQLStore, s *sent, emailErr error) *Dispatcher {
	t.Helper()
	d, err := NewDispatcher([]EventDef{
		{Name: "course_new", Category: CatCourses, Defaults: []string{ChanInApp, ChanEmail}},
		{Name: "login_new", Category: CatSecurity, Defaults: []string{ChanInApp}, Locked: []string{ChanEmail}},
	}, st, Hooks{
		Directory: func(context.Context, int64) (Recipient, error) {
			return Recipient{Email: "a@b.c", Lang: "en", TelegramChatID: 5}, nil
		},
		Render: func(_ context.Context, _ Recipient, ev string, data map[string]string) (Rendered, error) {
			return Rendered{Title: ev, Body: data["x"], Link: "/c"}, nil
		},
		Email:    func(context.Context, Recipient, string, Rendered) error { s.email++; return emailErr },
		Telegram: func(context.Context, int64, Rendered) error { s.tg++; return nil },
		Push:     func(context.Context, int64, Rendered, int64) error { s.push++; return nil },
		Admins:   func(context.Context) ([]int64, error) { return []int64{1, 2}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDefaultsAndPreferences(t *testing.T) {
	st := testStore(t)
	var s sent
	d := newDisp(t, st, &s, nil)
	if err := d.Notify(context.Background(), 9, "course_new", map[string]string{"x": "hi"}); err != nil {
		t.Fatal(err)
	}
	if s.email != 1 || s.tg != 0 || s.push != 0 {
		t.Fatalf("defaults: %+v", s)
	}
	if n, _ := st.Unread(9); n != 1 {
		t.Fatalf("unread = %d", n)
	}
	if err := st.SetPrefs(9, map[string]bool{"courses/email": false, "courses/telegram": true, "courses/push": true}); err != nil {
		t.Fatal(err)
	}
	s = sent{}
	if err := d.Notify(context.Background(), 9, "course_new", nil); err != nil {
		t.Fatal(err)
	}
	if s.email != 0 || s.tg != 1 || s.push != 1 {
		t.Fatalf("after prefs: %+v", s)
	}
	s = sent{}
	if err := st.SetPrefs(9, map[string]bool{"security/email": false}); err != nil {
		t.Fatal(err)
	}
	if err := d.Notify(context.Background(), 9, "login_new", nil); err != nil || s.email != 1 {
		t.Fatalf("locked channel must still send: %v %+v", err, s)
	}
}

func TestChannelFailureDoesNotHideOthers(t *testing.T) {
	st := testStore(t)
	var s sent
	d := newDisp(t, st, &s, errors.New("smtp down"))
	err := d.Notify(context.Background(), 3, "course_new", nil)
	if err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("email failure must be returned: %v", err)
	}
	if n, _ := st.Unread(3); n != 1 {
		t.Fatal("in-app must still be stored when email fails")
	}
}

func TestOnceAndAdmins(t *testing.T) {
	st := testStore(t)
	var s sent
	d := newDisp(t, st, &s, nil)
	ok, err := d.NotifyOnce(context.Background(), 4, "course_new", "w1", nil)
	if err != nil || !ok {
		t.Fatalf("first: %v %v", ok, err)
	}
	ok, err = d.NotifyOnce(context.Background(), 4, "course_new", "w1", nil)
	if err != nil || ok {
		t.Fatalf("second must be skipped: %v %v", ok, err)
	}
	if err := d.NotifyAdmins(context.Background(), "course_new", nil, 1); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Unread(1); n != 0 {
		t.Fatal("skip user must not be notified")
	}
	if n, _ := st.Unread(2); n != 1 {
		t.Fatal("admin 2 must be notified")
	}
}

func TestPrefsPanelRoundTrip(t *testing.T) {
	st := testStore(t)
	var s sent
	d := newDisp(t, st, &s, nil)
	html := string(PrefsPanel(PrefsOptions{Action: "/account/notifications", Channels: []string{ChanInApp, ChanEmail}, Events: d.Events(), Values: map[string]bool{"courses/email": false}}))
	if !strings.Contains(html, `value="courses/email"`) || strings.Contains(html, `value="courses/email" checked`) {
		t.Fatalf("stored off choice must render unchecked: %s", html)
	}
	if !strings.Contains(html, `value="security/email" checked disabled`) {
		t.Fatalf("locked channel must render checked and disabled")
	}
	form := url.Values{"pref": {"courses/inapp"}}
	req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got := ParsePrefs(req, d.Events(), []string{ChanInApp, ChanEmail})
	if !got["courses/inapp"] || got["courses/email"] {
		t.Fatalf("parse: %v", got)
	}
	if _, has := got["security/email"]; has {
		t.Fatal("locked pairs must not be saved")
	}
}
