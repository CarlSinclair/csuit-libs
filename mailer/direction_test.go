package mailer

import (
	"strings"
	"testing"
)

func TestApplyDirection(t *testing.T) {
	prev := LocaleDirFn
	LocaleDirFn = func(lang string) string {
		if lang == "ar" {
			return "rtl"
		}
		return "ltr"
	}
	defer func() { LocaleDirFn = prev }()

	doc := `<html lang="x"><body>hello</body></html>`
	cases := []struct {
		name string
		data interface{}
		body string
		rtl  bool
	}{
		{"dir wins", map[string]interface{}{"Dir": "rtl", "Lang": "en"}, doc, true},
		{"ltr explicit", map[string]interface{}{"Dir": "ltr", "Lang": "ar"}, doc, false},
		{"lang ar", map[string]interface{}{"Lang": "ar"}, doc, true},
		{"lang en", map[string]interface{}{"Lang": "en"}, doc, false},
		{"struct lang", struct{ Lang string }{"ar"}, doc, true},
		{"heuristic arabic", map[string]interface{}{}, `<html><body>مرحبا بكم في المتجر</body></html>`, true},
		{"heuristic latin", map[string]interface{}{}, `<html><body>hello there</body></html>`, false},
		{"already has dir", map[string]interface{}{"Lang": "ar"}, `<html dir="ltr"><body>x</body></html>`, false},
	}
	for _, c := range cases {
		got := strings.Contains(ApplyDirection(c.body, c.data), `dir="rtl"`)
		if got != c.rtl {
			t.Errorf("%s: rtl=%v want %v", c.name, got, c.rtl)
		}
	}
}
