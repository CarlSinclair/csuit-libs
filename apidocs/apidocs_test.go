package apidocs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CarlSinclair/csuit-libs/apikit"
)

type sampleItem struct {
	SKU   string   `json:"sku"`
	Price int      `json:"price"`
	Tags  []string `json:"tags"`
	Dims  struct {
		W int `json:"w"`
	} `json:"dims"`
}

func testOps() []apikit.Op {
	return []apikit.Op{
		{Method: http.MethodGet, Path: "/api/v1/items", Scope: "items:read", Summary: "List items", Tag: "Items", Response: sampleItem{}, List: true},
		{Method: http.MethodPut, Path: "/api/v1/items/{sku}", Scope: "items:write", Summary: "Put an item", Tag: "Items", Request: sampleItem{}, Response: sampleItem{}, Idempotent: true},
	}
}

func TestRenderBody(t *testing.T) {
	doc := Build(testOps(), Config{Product: "Demo", IntroKey: "intro", BaseURL: "https://x.test/api/v1/", Events: []string{"item.created"}})
	body, err := RenderBody(doc, func(k string) string { return "[" + k + "]" })
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"[api_docs_auth_title]", "[intro]", "https://x.test/api/v1/items", "items:write",
		"Idempotency-Key: $(uuidgen)", "dims.w", "tags", "item.created", "#tag-items", "whsec_",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered docs missing %q", want)
		}
	}
	if strings.Contains(s, "<script") {
		t.Errorf("rendered docs must not contain script tags")
	}
}

func TestRenderBodyNoEvents(t *testing.T) {
	doc := Build(testOps(), Config{Product: "Demo", IntroKey: "intro", BaseURL: "https://x.test/api/v1"})
	body, err := RenderBody(doc, func(k string) string { return k })
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(body), "api_docs_webhooks_title") {
		t.Errorf("webhooks section must be omitted when the product has no events")
	}
}

func TestCSSHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	CSSHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/docs.css", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") || rec.Body.Len() == 0 {
		t.Errorf("css handler: code=%d type=%q len=%d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
}
