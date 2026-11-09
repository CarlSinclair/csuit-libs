package legalpages

import (
	"html"
	"regexp"
	"strings"
)

var (
	chatImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	chatLink      = regexp.MustCompile(`\[([^\]]+)\]\(\s*([^)\s]+)[^)]*\)`)
	chatBold      = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	chatItalic    = regexp.MustCompile(`(^|[\s(])[*_]([^\s*_][^*_\n]*?)[*_]($|[\s).,;:!?])`)
	chatHeading   = regexp.MustCompile(`^\s{0,3}#{1,6}\s*(.*)$`)
	chatBullet    = regexp.MustCompile(`^(\s*)[*+-]\s+(.*)$`)
	chatRule      = regexp.MustCompile(`^\s*(-(\s*-){2,}|\*(\s*\*){2,}|_(\s*_){2,})\s*$`)
	chatTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	chatSafeHref  = regexp.MustCompile(`(?i)^(https?://|mailto:)`)
	chatFenceOpen = "```"
)

// ChatMessage is one message of a page rendered for a chat: HTML in the
// subset Telegram accepts, and the same text as plain text for when Telegram
// rejects the markup.
type ChatMessage struct {
	HTML  string
	Plain string
}

func chatInline(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 {
		parts[len(parts)-2] += "`" + parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(p) + "</code>")
			continue
		}
		p = html.EscapeString(p)
		p = chatImage.ReplaceAllString(p, "$1")
		p = chatLink.ReplaceAllStringFunc(p, func(m string) string {
			sub := chatLink.FindStringSubmatch(m)
			if chatSafeHref.MatchString(sub[2]) {
				return `<a href="` + sub[2] + `">` + sub[1] + `</a>`
			}
			return sub[1] + " (" + sub[2] + ")"
		})
		p = chatBold.ReplaceAllString(p, "<b>$2</b>")
		p = chatItalic.ReplaceAllString(p, "$1<i>$2</i>$3")
		b.WriteString(p)
	}
	return b.String()
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// TelegramHTML converts page Markdown into the HTML subset Telegram renders
// (b, i, a, code, pre, blockquote). Everything is escaped first, so source
// text can never inject markup. Headings become bold lines, lists become
// bullets and tables become one line per row.
func TelegramHTML(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, chatFenceOpen):
			var code []string
			for i++; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), chatFenceOpen) {
					break
				}
				code = append(code, lines[i])
			}
			out = append(out, "<pre>"+html.EscapeString(strings.Join(code, "\n"))+"</pre>")
		case trimmed == "":
			out = append(out, "")
		case chatRule.MatchString(trimmed):
			out = append(out, "")
		case chatHeading.MatchString(line):
			out = append(out, "<b>"+chatInline(chatHeading.FindStringSubmatch(line)[1])+"</b>")
		case strings.Contains(trimmed, "|") && i+1 < len(lines) && chatTableSep.MatchString(lines[i+1]):
			out = append(out, "<b>"+chatInline(strings.Join(tableCells(trimmed), " | "))+"</b>")
			for i += 2; i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != ""; i++ {
				out = append(out, chatInline(strings.Join(tableCells(lines[i]), " | ")))
			}
			i--
		case strings.HasPrefix(trimmed, ">"):
			var quote []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				quote = append(quote, chatInline(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"))))
			}
			i--
			out = append(out, "<blockquote>"+strings.Join(quote, "\n")+"</blockquote>")
		case chatBullet.MatchString(line):
			m := chatBullet.FindStringSubmatch(line)
			out = append(out, m[1]+"• "+chatInline(m[2]))
		default:
			out = append(out, chatInline(line))
		}
	}
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}

func splitBlocks(md string) []string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var blocks []string
	var cur []string
	fenced := false
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), chatFenceOpen) {
			fenced = !fenced
		}
		if strings.TrimSpace(l) == "" && !fenced {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	return blocks
}

// ChatMessages renders a page for a chat: the title, then the body, split
// between blocks into messages whose HTML is at most limit characters.
func ChatMessages(r Resolved, limit int) []ChatMessage {
	if limit <= 0 {
		limit = 4000
	}
	var out []ChatMessage
	add := func(h, p string) {
		if strings.TrimSpace(h) == "" {
			return
		}
		if n := len(out); n > 0 && len([]rune(out[n-1].HTML))+2+len([]rune(h)) <= limit {
			out[n-1].HTML += "\n\n" + h
			out[n-1].Plain += "\n\n" + p
			return
		}
		out = append(out, ChatMessage{HTML: h, Plain: p})
	}
	if t := strings.TrimSpace(r.Title); t != "" {
		add("<b>"+html.EscapeString(t)+"</b>", t)
	}
	for _, block := range splitBlocks(r.Body) {
		pieces := []string{block}
		if len([]rune(TelegramHTML(block))) > limit {
			pieces = strings.SplitAfter(block, "\n")
		}
		for _, piece := range pieces {
			for len([]rune(TelegramHTML(piece))) > limit {
				rs := []rune(piece)
				add(TelegramHTML(string(rs[:limit/2])), PlainText(string(rs[:limit/2])))
				piece = string(rs[limit/2:])
			}
			add(TelegramHTML(piece), PlainText(piece))
		}
	}
	return out
}
