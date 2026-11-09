package emailkit

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestMerchantOverridesAndContrast(t *testing.T) {
	th := Merchant("Brand", Overrides{BrandName: "Shop", Primary: "#ffee00", Text: "red;}</style>"})
	if th.BrandName != "Shop" || th.Primary != "#ffee00" || th.PrimaryHover != "#ffee00" {
		t.Fatalf("unexpected theme %+v", th)
	}
	if th.Text != DefaultText {
		t.Fatalf("unsafe color accepted: %q", th.Text)
	}
	if th.ButtonText != "#111827" {
		t.Fatalf("light primary needs dark text, got %q", th.ButtonText)
	}
	if Platform("B").ButtonText != "#ffffff" {
		t.Fatal("platform button text must be white")
	}
}

func TestRenderWrapsFragment(t *testing.T) {
	th := Platform("Brand")
	fsys := fstest.MapFS{"x.html": {Data: []byte(`{{ heading .T }}{{ button .U "Go" }}`)}}
	tpl, err := ParseFS(th, fsys, "x.html")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(tpl, map[string]any{"T": "<b>Hi</b>", "U": "javascript:alert(1)"}, th, Page{Lang: "pt", Title: "S", Rights: "All rights reserved.", Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<html lang="pt">`, `name="viewport"`, `&lt;b&gt;Hi&lt;/b&gt;`, `href="#"`, `&copy; 2026 Brand. All rights reserved.`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}
