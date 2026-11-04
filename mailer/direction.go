package mailer

import (
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

var (
	emailStylePattern = regexp.MustCompile(`(?is)<(style|script)[^>]*>.*?</(style|script)>`)
	emailTagPattern   = regexp.MustCompile(`(?s)<[^>]*>`)
	emailHTMLTag      = regexp.MustCompile(`(?i)<html\b([^>]*)>`)
)

func isRTLScript(r rune) bool {
	return unicode.Is(unicode.Arabic, r) || unicode.Is(unicode.Hebrew, r) || unicode.Is(unicode.Syriac, r) || unicode.Is(unicode.Thaana, r)
}

func emailIsRTL(html string) bool {
	text := emailTagPattern.ReplaceAllString(emailStylePattern.ReplaceAllString(html, " "), " ")
	rtl, other := 0, 0
	for _, r := range text {
		switch {
		case isRTLScript(r):
			rtl++
		case unicode.IsLetter(r):
			other++
		}
	}
	return rtl > 0 && rtl >= other
}

func emailLang(data interface{}) string {
	if m, ok := data.(map[string]interface{}); ok {
		if l, ok := m["Lang"].(string); ok {
			return strings.TrimSpace(l)
		}
		return ""
	}
	v := reflect.ValueOf(data)
	for v.IsValid() && (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) {
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.Struct {
		if f := v.FieldByName("Lang"); f.IsValid() && f.Kind() == reflect.String {
			return strings.TrimSpace(f.String())
		}
	}
	return ""
}

func emailDir(data interface{}) string {
	if m, ok := data.(map[string]interface{}); ok {
		if d, ok := m["Dir"].(string); ok {
			return strings.ToLower(strings.TrimSpace(d))
		}
	}
	return ""
}

var LocaleDirFn func(lang string) string

func ApplyDirection(html string, data interface{}) string {
	if dir := emailDir(data); dir == "rtl" || dir == "ltr" {
		if dir == "ltr" {
			return html
		}
	} else if lang := emailLang(data); lang != "" {
		if LocaleDirFn == nil || LocaleDirFn(lang) != "rtl" {
			return html
		}
	} else if !emailIsRTL(html) {
		return html
	}
	loc := emailHTMLTag.FindStringSubmatchIndex(html)
	if loc == nil || strings.Contains(strings.ToLower(html[loc[2]:loc[3]]), "dir=") {
		return html
	}
	return html[:loc[3]] + ` dir="rtl"` + html[loc[3]:]
}
