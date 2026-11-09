package legalpages

import (
	"strings"
	"testing"
)

func TestCommandName(t *testing.T) {
	cases := map[string]string{
		"privacy":               "privacy",
		"Refund-Policy":         "refund_policy",
		"perguntas-frequentes":  "perguntas_frequentes",
		"termos de uso":         "termos_de_uso",
		"a/b?c":                 "abc",
		"---":                   "___",
		"":                      "",
		strings.Repeat("a", 40): strings.Repeat("a", 32),
	}
	for in, want := range cases {
		if got := CommandName(in); got != want {
			t.Errorf("CommandName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatMessagesSplitsLongPages(t *testing.T) {
	body := strings.Repeat("word ", 30) + "\n\n" + strings.Repeat("more ", 30)
	msgs := ChatMessages(Resolved{Title: "Title", Body: body}, 200)
	if len(msgs) < 2 {
		t.Fatalf("want the page split into several messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if n := len([]rune(m.HTML)); n > 200 {
			t.Errorf("message of %d runes exceeds the limit", n)
		}
	}
	if !strings.HasPrefix(msgs[0].HTML, "<b>Title</b>") {
		t.Errorf("first message should start with the bold title, got %q", msgs[0].HTML[:12])
	}
}

func TestTelegramHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"heading", "## Delivery\nWe deliver.", "<b>Delivery</b>\nWe deliver."},
		{"bold and italic", "We are **open** from *6pm* to __midnight__.", "We are <b>open</b> from <i>6pm</i> to <b>midnight</b>."},
		{"link", "See [our map](https://example.com/map) today.", `See <a href="https://example.com/map">our map</a> today.`},
		{"unsafe link", "[x](javascript:alert(1))", "x (javascript:alert(1))"},
		{"escapes markup", "1 < 2 & <b>x</b>", "1 &lt; 2 &amp; &lt;b&gt;x&lt;/b&gt;"},
		{"bullets", "- one\n* two", "• one\n• two"},
		{"code", "Use `a<b` here", "Use <code>a&lt;b</code> here"},
		{"fence", "```\nx < y\n```", "<pre>x &lt; y</pre>"},
		{"quote", "> a\n> b", "<blockquote>a\nb</blockquote>"},
	}
	for _, c := range cases {
		if got := TelegramHTML(c.in); got != c.want {
			t.Errorf("%s: TelegramHTML(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestResolveCommandPicksTheAskersLanguage(t *testing.T) {
	vars := []LiveVariant{
		{PageID: 1, Lang: "en", Slug: "tos", Title: "Terms", Body: "en body"},
		{PageID: 1, Lang: "pt", Slug: "termos", Title: "Termos", Body: "pt body"},
		{PageID: 2, Lang: "en", Slug: "privacy", Title: "Privacy", Body: "p"},
	}
	for _, c := range []struct{ cmd, lang, want string }{
		{"terms", "pt", "pt body"},
		{"tos", "en", "en body"},
		{"termos", "en", "en body"},
		{"terms", "es", "en body"},
		{"/privacy", "pt-BR", "p"},
	} {
		got, ok := ResolveCommand(vars, c.cmd, []string{c.lang, "en"})
		if !ok || got.Body != c.want {
			t.Errorf("ResolveCommand(%q, %q) = %q, %v; want %q", c.cmd, c.lang, got.Body, ok, c.want)
		}
	}
	if _, ok := ResolveCommand(vars, "nothing", []string{"en"}); ok {
		t.Error("an unknown command must not resolve")
	}
}

func TestMenuEntriesUseTheMenuLanguage(t *testing.T) {
	vars := []LiveVariant{
		{PageID: 1, Lang: "en", Slug: "tos", Title: "Terms", Body: "b"},
		{PageID: 1, Lang: "pt", Slug: "termos", Title: "Termos", Body: "b"},
		{PageID: 2, Lang: "en", Slug: "privacy-policy", Title: "Privacy", Body: "b"},
	}
	pt := MenuEntries(vars, []string{"pt", "en"})
	if len(pt) != 2 || pt[0].Command != "termos" || pt[0].Description != "Termos" || pt[1].Command != "privacy_policy" {
		t.Errorf("pt menu = %+v", pt)
	}
	en := MenuEntries(vars, []string{"en"})
	if len(en) != 2 || en[0].Command != "tos" {
		t.Errorf("en menu = %+v", en)
	}
}
