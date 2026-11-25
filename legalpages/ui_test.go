package legalpages

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func uiStore(t *testing.T) Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	st := Store{DB: db, Table: "static_pages"}
	if err := st.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return st
}

func TestPostAddSaveDeleteAndEditor(t *testing.T) {
	st := uiStore(t)
	var flashes []string
	changed := 0
	h := Post(AdminOptions{Store: st, Back: "/admin/legal", Actor: func(*http.Request) string { return "a@b" },
		Flash: func(_ http.ResponseWriter, _ *http.Request, kind, key string) {
			flashes = append(flashes, kind+":"+key)
		},
		Changed: func() { changed++ }})
	post := func(v url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/admin/legal", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	w := post(url.Values{"lp_action": {"add"}, "new_lang": {"en"}, "new_slug": {"terms"}, "new_title": {"Terms"}, "new_body": {"Hello"}})
	if w.Code != http.StatusSeeOther || len(flashes) != 1 || flashes[0] != "success:legal_msg_added" || changed != 1 {
		t.Fatalf("add: code=%d flashes=%v changed=%d", w.Code, flashes, changed)
	}
	post(url.Values{"lp_action": {"add"}, "new_lang": {"en"}, "new_slug": {"terms"}, "new_title": {"Again"}, "new_body": {"x"}})
	if flashes[len(flashes)-1] != "error:legal_err_slug_taken" {
		t.Fatalf("duplicate slug flash = %v", flashes)
	}
	res, ok := st.Resolve("terms")
	if !ok || res.Title != "Terms" {
		t.Fatalf("resolve = %+v ok=%v", res, ok)
	}
	html := string(Editor(EditorOptions{Store: st, Lang: "en", PostURL: "/admin/legal", CheckURL: "/c", ResolveURL: "/r", IconHref: func(n string) string { return "/i.svg#fa-" + n }}))
	if !strings.Contains(html, "/terms") || !strings.Contains(html, `action="/admin/legal"`) {
		t.Fatalf("editor missing page: %.300s", html)
	}
	pages, _ := st.ListPages()
	post(url.Values{"lp_action": {"delete"}, "page_id": {itoa(pages[0].ID)}})
	if left, _ := st.ListPages(); len(left) != 0 {
		t.Fatalf("page not deleted: %v", left)
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n))) }
