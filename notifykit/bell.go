package notifykit

import (
	"html"
	"strings"
)

type Labels struct {
	Title, Empty, History, Back, MarkGroup, Prev, Next, Close, Error string
	Categories                                                       map[string]string
}

func BellHTML(prefix, csrf, iconHref string, l Labels) string {
	var b strings.Builder
	attr := func(k, v string) {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(v))
		b.WriteString(`"`)
	}
	b.WriteString(`<div class="nk-root" data-notifykit="1"`)
	attr("data-prefix", strings.TrimRight(prefix, "/"))
	attr("data-csrf", csrf)
	attr("data-l-title", l.Title)
	attr("data-l-empty", l.Empty)
	attr("data-l-history", l.History)
	attr("data-l-back", l.Back)
	attr("data-l-markgroup", l.MarkGroup)
	attr("data-l-prev", l.Prev)
	attr("data-l-next", l.Next)
	attr("data-l-close", l.Close)
	attr("data-l-error", l.Error)
	for _, c := range Categories() {
		if v, ok := l.Categories[c]; ok {
			attr("data-c-"+c, v)
		}
	}
	b.WriteString(`>`)
	if iconHref != "" {
		b.WriteString(`<svg class="nk-icon" aria-hidden="true"><use href="`)
		b.WriteString(html.EscapeString(iconHref))
		b.WriteString(`"></use></svg>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
