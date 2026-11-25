package accountkit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type rec struct {
	deleted, revoked, cleared int
	audits                    []string
	flashes                   []string
}

func newKit(t *testing.T, r *rec, hasPass bool, guard error) *Kit {
	t.Helper()
	k, err := New(Config{
		Identity: func(*http.Request) (Subject, error) {
			return Subject{ID: 7, Label: "ana@example.com", HasPassword: hasPass}, nil
		},
		CheckPassword: func(s Subject, pw string) bool { return pw == "secret" },
		Guard:         func(context.Context, Subject) error { return guard },
		Delete:        func(context.Context, Subject) error { r.deleted++; return nil },
		RevokeAll:     func(*http.Request, Subject, bool) error { r.revoked++; return nil },
		ClearSession:  func(http.ResponseWriter) { r.cleared++ },
		Export:        func(context.Context, Subject) (map[string]any, error) { return map[string]any{"profile": 1}, nil },
		Audit:         func(_ *http.Request, _ Subject, a string) { r.audits = append(r.audits, a) },
		Flash: func(_ http.ResponseWriter, _ *http.Request, kind, key string) {
			r.flashes = append(r.flashes, kind+":"+key)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func post(k *Kit, h func(http.ResponseWriter, *http.Request), form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/account/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestDeleteNeedsPassword(t *testing.T) {
	r := &rec{}
	k := newKit(t, r, true, nil)
	w := post(k, k.DeleteHandler, url.Values{"password": {"wrong"}})
	if w.Code != http.StatusSeeOther || r.deleted != 0 || len(r.flashes) != 1 || r.flashes[0] != "error:"+KeyBadPassword {
		t.Fatalf("wrong password: code=%d deleted=%d flashes=%v", w.Code, r.deleted, r.flashes)
	}
	w = post(k, k.DeleteHandler, url.Values{"password": {"secret"}})
	if r.deleted != 1 || r.revoked != 1 || r.cleared != 1 || len(r.audits) != 1 || r.audits[0] != "account_deleted" || w.Header().Get("Location") != "/" {
		t.Fatalf("delete: %+v loc=%s", r, w.Header().Get("Location"))
	}
}

func TestDeleteWithTypedLabelWhenNoPassword(t *testing.T) {
	r := &rec{}
	k := newKit(t, r, false, nil)
	post(k, k.DeleteHandler, url.Values{"confirm": {"nope"}})
	if r.deleted != 0 {
		t.Fatal("deleted without confirmation")
	}
	post(k, k.DeleteHandler, url.Values{"confirm": {"ANA@example.com"}})
	if r.deleted != 1 {
		t.Fatal("typed label should confirm, case-insensitively")
	}
}

func TestGuardBlocksAndJSONMode(t *testing.T) {
	r := &rec{}
	k := newKit(t, r, true, &Error{Status: 409, Key: "last_admin"})
	req := httptest.NewRequest("POST", "/api/account/delete", strings.NewReader(`{"password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	k.DeleteHandler(w, req)
	if w.Code != 409 || r.deleted != 0 || !strings.Contains(w.Body.String(), "last_admin") {
		t.Fatalf("guard: %d %s", w.Code, w.Body.String())
	}
}

func TestDeleteFailureDoesNotAuditOrClear(t *testing.T) {
	r := &rec{}
	k, _ := New(Config{
		Identity:      func(*http.Request) (Subject, error) { return Subject{ID: 1, HasPassword: true}, nil },
		CheckPassword: func(Subject, string) bool { return true },
		Delete:        func(context.Context, Subject) error { return errors.New("db down") },
		RevokeAll:     func(*http.Request, Subject, bool) error { return nil },
		ClearSession:  func(http.ResponseWriter) { r.cleared++ },
		Export:        func(context.Context, Subject) (map[string]any, error) { return nil, nil },
		Audit:         func(_ *http.Request, _ Subject, a string) { r.audits = append(r.audits, a) },
	})
	w := post(k, k.DeleteHandler, url.Values{"password": {"x"}})
	if len(r.audits) != 0 || r.cleared != 0 || w.Code != http.StatusSeeOther {
		t.Fatalf("failed delete must not audit or clear: %+v", r)
	}
}

func TestExportAndPanel(t *testing.T) {
	r := &rec{}
	k := newKit(t, r, true, nil)
	w := httptest.NewRecorder()
	k.ExportHandler(w, httptest.NewRequest("GET", "/account/export", nil))
	if !strings.Contains(w.Header().Get("Content-Disposition"), "account-export.json") || !strings.Contains(w.Body.String(), "exported_at") || len(r.audits) != 1 {
		t.Fatalf("export: %v %s", w.Header(), w.Body.String())
	}
	html := string(Panel(PanelOptions{Subject: Subject{Label: "a@b.c"}, NeedsCode: true, HasDeactivate: true}))
	for _, want := range []string{"modal-delete-account", "modal-deactivate-account", `name="confirm"`, `name="totp_code"`, "/account/export"} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel misses %s", want)
		}
	}
}

func TestInputReachesHooksAndWritersOverride(t *testing.T) {
	var seen map[string]string
	var errKey string
	k, err := New(Config{
		Identity:      func(*http.Request) (Subject, error) { return Subject{ID: 1, HasPassword: true}, nil },
		CheckPassword: func(s Subject, pw string) bool { return pw == "secret" },
		Delete:        func(ctx context.Context, _ Subject) error { seen = Input(ctx); return nil },
		RevokeAll:     func(*http.Request, Subject, bool) error { return nil },
		ClearSession:  func(http.ResponseWriter) {},
		Export:        func(context.Context, Subject) (map[string]any, error) { return map[string]any{}, nil },
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, key string) {
			errKey = key
			w.WriteHeader(status)
		},
		WriteOK: func(w http.ResponseWriter, _ *http.Request, to string) { _, _ = w.Write([]byte("ok:" + to)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		k.DeleteHandler(w, req)
		return w
	}
	if w := do(`{"password":"nope"}`); w.Code != http.StatusUnauthorized || errKey != KeyBadPassword {
		t.Fatalf("bad password: code=%d key=%q", w.Code, errKey)
	}
	w := do(`{"password":"secret","hide_comments":true,"n":3}`)
	if w.Body.String() != "ok:/" {
		t.Fatalf("body %q", w.Body.String())
	}
	if seen["hide_comments"] != "true" || seen["n"] != "3" || seen["password"] != "secret" {
		t.Fatalf("input not exposed: %v", seen)
	}
}
