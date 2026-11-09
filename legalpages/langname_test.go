package legalpages

import "testing"

func TestNormalizeLang(t *testing.T) {
	cases := []struct {
		in       string
		code     string
		resolved bool
	}{
		{"English", "en", true},
		{"en", "en", true},
		{"español", "es", true},
		{"Klingon Dialect Of Nowhere", "x-klingon-dialect-of-nowhere", false},
	}
	for _, c := range cases {
		code, resolved := NormalizeLang(c.in)
		if code != c.code || resolved != c.resolved {
			t.Errorf("NormalizeLang(%q) = %q,%v want %q,%v", c.in, code, resolved, c.code, c.resolved)
		}
	}
	if code, _ := NormalizeLang("   "); code != "" {
		t.Errorf("blank input must give an empty code, got %q", code)
	}
}

func TestLanguageLabelAndSuggestions(t *testing.T) {
	if got := LanguageLabel("en", "en"); got != "English (en)" {
		t.Errorf("label = %q", got)
	}
	if got := LanguageLabel("en", "zz-unknown"); got == "" {
		t.Errorf("label with an unknown viewer must still render")
	}
	if len(LanguageSuggestions("en")) < 50 {
		t.Errorf("suggestions list unexpectedly short")
	}
	if !SameLang("EN", " en ") {
		t.Errorf("SameLang must ignore case and spaces")
	}
}
