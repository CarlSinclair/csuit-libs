package notifykit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	items   []Item
	failing bool
}

func (f *fakeStore) Counts(uid int64) ([]CategoryCount, error) {
	if f.failing {
		return nil, errors.New("boom")
	}
	m := map[string]*CategoryCount{}
	for _, it := range f.items {
		c := m[it.Category]
		if c == nil {
			c = &CategoryCount{Category: it.Category}
			m[it.Category] = c
		}
		c.Total++
		if !it.Read {
			c.Unread++
		}
		if it.CreatedAt.After(c.LatestAt) {
			c.LatestAt, c.LatestTitle = it.CreatedAt, it.Title
		}
	}
	var out []CategoryCount
	for _, c := range m {
		out = append(out, *c)
	}
	return out, nil
}

func (f *fakeStore) List(uid int64, fl Filter) ([]Item, int, error) {
	if f.failing {
		return nil, 0, errors.New("boom")
	}
	var all []Item
	for _, it := range f.items {
		if fl.Category != "" && it.Category != fl.Category {
			continue
		}
		if fl.UnreadOnly && it.Read {
			continue
		}
		all = append(all, it)
	}
	total := len(all)
	if fl.Offset >= len(all) {
		return nil, total, nil
	}
	all = all[fl.Offset:]
	if fl.Limit > 0 && len(all) > fl.Limit {
		all = all[:fl.Limit]
	}
	return all, total, nil
}

func (f *fakeStore) Get(uid, id int64) (Item, error) {
	for _, it := range f.items {
		if it.ID == id {
			return it, nil
		}
	}
	return Item{}, errors.New("not found")
}

func (f *fakeStore) MarkRead(uid, id int64) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Read = true
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeStore) MarkCategoryRead(uid int64, cat string) (int64, error) {
	var n int64
	for i := range f.items {
		if f.items[i].Category == cat && !f.items[i].Read {
			f.items[i].Read = true
			n++
		}
	}
	return n, nil
}

func newHandler(s Store) http.Handler {
	return New(Config{Store: s, Prefix: "/notifications", Fallback: "/", UserID: func(r *http.Request) (int64, bool) {
		if r.Header.Get("X-Test-User") == "" {
			return 0, false
		}
		return 1, true
	}})
}

func do(h http.Handler, method, path string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if auth {
		req.Header.Set("X-Test-User", "1")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func seed(n int, cat string, read bool) []Item {
	var out []Item
	for i := 0; i < n; i++ {
		out = append(out, Item{ID: int64(100 + i), Category: cat, Title: "t", Read: read, CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute)})
	}
	return out
}

func TestSortCountsSecurityFirstAndUnreadFirst(t *testing.T) {
	got := SortCounts([]CategoryCount{
		{Category: "orders", Unread: 2, Total: 2},
		{Category: "security", Unread: 1, Total: 1},
		{Category: "billing", Unread: 0, Total: 4},
		{Category: "weird", Unread: 1, Total: 1},
	})
	if got[0].Category != CatSecurity {
		t.Fatalf("first = %s", got[0].Category)
	}
	if got[len(got)-1].Category != CatBilling {
		t.Fatalf("read-only group should sort last, got %s", got[len(got)-1].Category)
	}
	foundOther := false
	for _, c := range got {
		if c.Category == CatOther {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatal("unknown category not normalised to other")
	}
}

func TestRegistryUnknownEvent(t *testing.T) {
	r := Registry{"login_alert": CatSecurity}
	if r.Category("login_alert") != CatSecurity || r.Category("nope") != CatOther {
		t.Fatal("registry lookup wrong")
	}
}

func TestSafeLocalPath(t *testing.T) {
	bad := []string{"", "http://evil.com", "//evil.com", "/\\evil.com", "javascript:alert(1)", "/a\r\nb"}
	for _, b := range bad {
		if _, ok := SafeLocalPath(b); ok {
			t.Errorf("accepted %q", b)
		}
	}
	if p, ok := SafeLocalPath("/orders/5"); !ok || p != "/orders/5" {
		t.Fatal("rejected a good path")
	}
}

func TestUnauthorized(t *testing.T) {
	rr := do(newHandler(&fakeStore{}), "GET", "/notifications/summary", false)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rr.Code)
	}
}

func TestSummaryPlainBelowThreshold(t *testing.T) {
	s := &fakeStore{items: seed(3, CatOrders, false)}
	rr := do(newHandler(s), "GET", "/notifications/summary", true)
	var sum Summary
	if err := json.Unmarshal(rr.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Grouped || sum.Unread != 3 || len(sum.Items) != 3 {
		t.Fatalf("%+v", sum)
	}
	if !strings.HasPrefix(sum.Items[0].OpenURL, "/notifications/open/") {
		t.Fatalf("open url %q", sum.Items[0].OpenURL)
	}
}

func TestSummaryGroupedAboveThreshold(t *testing.T) {
	s := &fakeStore{items: append(seed(4, CatOrders, false), seed(2, CatSecurity, false)...)}
	rr := do(newHandler(s), "GET", "/notifications/summary", true)
	var sum Summary
	_ = json.Unmarshal(rr.Body.Bytes(), &sum)
	if !sum.Grouped || len(sum.Items) != 0 || sum.Categories[0].Category != CatSecurity {
		t.Fatalf("%+v", sum)
	}
}

func TestSummaryExactlyThresholdIsNotGrouped(t *testing.T) {
	s := &fakeStore{items: seed(GroupThreshold, CatOrders, false)}
	rr := do(newHandler(s), "GET", "/notifications/summary", true)
	var sum Summary
	_ = json.Unmarshal(rr.Body.Bytes(), &sum)
	if sum.Grouped {
		t.Fatal("5 unread must not group")
	}
}

func TestSummaryStoreFailureSurfaces(t *testing.T) {
	rr := do(newHandler(&fakeStore{failing: true}), "GET", "/notifications/summary", true)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rr.Code)
	}
}

func TestListPaginatesAndKeepsRead(t *testing.T) {
	s := &fakeStore{items: append(seed(25, CatOrders, true), seed(2, CatOrders, false)...)}
	rr := do(newHandler(s), "GET", "/notifications/list?category=orders&page=2", true)
	var res ListResult
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	if res.Total != 27 || res.Pages != 2 || res.Page != 2 || len(res.Items) != 7 || res.Unread != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestReadCategoryOnlyTouchesThatCategory(t *testing.T) {
	s := &fakeStore{items: append(seed(2, CatOrders, false), seed(2, CatSecurity, false)...)}
	h := newHandler(s)
	rr := do(h, "POST", "/notifications/read-category?category=orders", true)
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	for _, it := range s.items {
		if it.Category == CatOrders && !it.Read {
			t.Fatal("orders still unread")
		}
		if it.Category == CatSecurity && it.Read {
			t.Fatal("security wrongly read")
		}
	}
}

func TestOpenMarksReadAndRedirectsSafely(t *testing.T) {
	s := &fakeStore{items: []Item{
		{ID: 1, Category: CatOrders, Link: "/orders/9"},
		{ID: 2, Category: CatOrders, Link: "https://evil.example/x"},
	}}
	h := newHandler(s)
	rr := do(h, "GET", "/notifications/open/1", true)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/orders/9" || !s.items[0].Read {
		t.Fatalf("code=%d loc=%q read=%v", rr.Code, rr.Header().Get("Location"), s.items[0].Read)
	}
	rr = do(h, "GET", "/notifications/open/2", true)
	if rr.Header().Get("Location") != "/" || !s.items[1].Read {
		t.Fatalf("unsafe link not neutralised: %q", rr.Header().Get("Location"))
	}
	rr = do(h, "GET", "/notifications/open/999", true)
	if rr.Header().Get("Location") != "/" {
		t.Fatal("missing item should fall back")
	}
}

func TestReadSingleAndBadID(t *testing.T) {
	s := &fakeStore{items: seed(1, CatOrders, false)}
	h := newHandler(s)
	if rr := do(h, "POST", "/notifications/read/100", true); rr.Code != 200 || !s.items[0].Read {
		t.Fatal("read failed")
	}
	if rr := do(h, "POST", "/notifications/read/abc", true); rr.Code != http.StatusBadRequest {
		t.Fatalf("code %d", rr.Code)
	}
	if rr := do(h, "GET", "/notifications/read/100", true); rr.Code != http.StatusNotFound {
		t.Fatal("GET must not mark read")
	}
}

func TestBellHTMLEscapesAndAssetsServe(t *testing.T) {
	out := BellHTML("/n/", `"><x>`, "/i.svg#bell", Labels{Title: `a"b`})
	if strings.Contains(out, `"><x>`) || !strings.Contains(out, `data-prefix="/n"`) {
		t.Fatalf("bad html: %s", out)
	}
	for _, p := range []string{"/x/notifykit.js", "/x/notifykit.css"} {
		rr := do(AssetHandler(), "GET", p, false)
		if rr.Code != 200 || rr.Body.Len() == 0 {
			t.Fatalf("%s code %d", p, rr.Code)
		}
	}
	if rr := do(AssetHandler(), "GET", "/x/other.js", false); rr.Code != 404 {
		t.Fatal("unknown asset served")
	}
}

func TestSummaryFlagsUnlinkedItems(t *testing.T) {
	s := &fakeStore{items: []Item{{ID: 1, Category: CatOrders, Link: ""}, {ID: 2, Category: CatOrders, Link: "/x"}}}
	rr := do(newHandler(s), "GET", "/notifications/summary", true)
	var sum Summary
	_ = json.Unmarshal(rr.Body.Bytes(), &sum)
	got := map[int64]bool{}
	for _, it := range sum.Items {
		got[it.ID] = it.HasLink
	}
	if got[1] || !got[2] {
		t.Fatalf("has_link flags wrong: %+v", got)
	}
}
