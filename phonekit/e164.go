package phonekit

import (
	"regexp"
	"strings"
)

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// IsE164 reports whether s is a full international number: "+" then 7 to 15
// digits, no spaces or punctuation. This is the format the phone input writes
// to its hidden field.
func IsE164(s string) bool { return e164Re.MatchString(strings.TrimSpace(s)) }

// WhatsAppURL returns the wa.me deep link for an E.164 number, or "" when the
// value is not E.164 (a link built from a bare national number would open the
// wrong country).
func WhatsAppURL(s string) string {
	s = strings.TrimSpace(s)
	if !e164Re.MatchString(s) {
		return ""
	}
	return "https://wa.me/" + s[1:]
}
