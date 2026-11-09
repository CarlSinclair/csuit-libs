package legalpages

import (
	"regexp"
	"strings"
)

var (
	plainImage   = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	plainLink    = regexp.MustCompile(`\[([^\]]+)\]\(\s*([^)\s]+)[^)]*\)`)
	plainHeading = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s*`)
	plainBullet  = regexp.MustCompile(`(?m)^(\s*)[*+-]\s+`)
	plainBold    = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	plainItalic  = regexp.MustCompile(`(^|[\s(])[*_]([^\s*_][^*_\n]*?)[*_]($|[\s).,;:!?])`)
	plainBlanks  = regexp.MustCompile(`\n{3,}`)
)

// PlainText turns a page's Markdown into readable plain text for places that
// cannot render it, such as a Telegram message.
func PlainText(md string) string {
	s := strings.ReplaceAll(md, "\r\n", "\n")
	s = plainImage.ReplaceAllString(s, "$1")
	s = plainLink.ReplaceAllString(s, "$1 ($2)")
	s = plainHeading.ReplaceAllString(s, "")
	s = plainBullet.ReplaceAllString(s, "$1• ")
	s = plainBold.ReplaceAllString(s, "$2")
	s = plainItalic.ReplaceAllString(s, "$1$2$3")
	s = strings.ReplaceAll(s, "`", "")
	s = plainBlanks.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
