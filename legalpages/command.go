package legalpages

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	commandMaxLen  = 32
	menuDescMaxLen = 256
)

var commandAliases = map[string][]string{
	"terms": {"tos", "terms_of_service", "terms_and_conditions"},
	"tos":   {"terms", "terms_of_service", "terms_and_conditions"},
}

var commandStrip = regexp.MustCompile(`[^a-z0-9_]+`)

// CommandName turns a page slug into the chat command that serves it:
// lowercase, hyphens become underscores, anything Telegram would reject is
// dropped, at most 32 characters. A slug that reduces to nothing has no
// command.
func CommandName(slug string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	s = strings.NewReplacer("-", "_", " ", "_").Replace(s)
	s = commandStrip.ReplaceAllString(s, "")
	if len(s) > commandMaxLen {
		s = s[:commandMaxLen]
	}
	return s
}

// LiveVariant is one language version of a live page, as the chat bots need
// it. It is JSON-shaped so a bot process can fetch the list from its
// controller.
type LiveVariant struct {
	PageID      int    `json:"page_id"`
	Lang        string `json:"lang"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	UpdatedAt   string `json:"updated_at"`
	ShowUpdated bool   `json:"show_updated"`
}

// MenuEntry is one line of a bot's command menu.
type MenuEntry struct {
	Command     string
	Description string
}

// LiveVariants lists every enabled language version of every enabled page
// that has a slug and a body.
func (s Store) LiveVariants() ([]LiveVariant, error) {
	rows, err := s.DB.Query(`SELECT page_id, lang, slug, title, body, updated_at, show_updated FROM ` + s.tbl() +
		` WHERE slug != '' AND body != '' AND enabled = 1 AND page_enabled = 1 ORDER BY page_id, lang`)
	if err != nil {
		return nil, fmt.Errorf("list live pages in %s: %w", s.Table, err)
	}
	defer rows.Close()
	var out []LiveVariant
	for rows.Next() {
		var v LiveVariant
		var su int
		if err := rows.Scan(&v.PageID, &v.Lang, &v.Slug, &v.Title, &v.Body, &v.UpdatedAt, &su); err != nil {
			return nil, fmt.Errorf("scan live page in %s: %w", s.Table, err)
		}
		v.ShowUpdated = su != 0
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list live pages in %s: %w", s.Table, err)
	}
	return out, nil
}

// langChain expands the wanted languages into the order they are tried:
// each one, then its base ("pt-br" -> "pt"), without repeats.
func langChain(langs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(l string) {
		l = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(l, "_", "-")))
		if l == "" || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	}
	for _, l := range langs {
		add(l)
		if i := strings.IndexByte(l, '-'); i > 0 {
			add(l[:i])
		}
	}
	return out
}

func pickVariant(vars []LiveVariant, pageID int, langs []string, named string) (LiveVariant, bool) {
	for _, lang := range append(langChain(langs), strings.ToLower(named)) {
		for _, v := range vars {
			if v.PageID == pageID && strings.EqualFold(v.Lang, lang) {
				return v, true
			}
		}
	}
	return LiveVariant{}, false
}

// ResolveCommand finds the live page whose slug (in any language) is cmd and
// returns it in the first of langs that has a live variant, falling back to
// the language version the command itself named.
func ResolveCommand(vars []LiveVariant, cmd string, langs []string) (Resolved, bool) {
	cmd = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cmd)), "/")
	if cmd == "" {
		return Resolved{}, false
	}
	for _, want := range append([]string{cmd}, commandAliases[cmd]...) {
		for _, v := range vars {
			if CommandName(v.Slug) != want {
				continue
			}
			p, ok := pickVariant(vars, v.PageID, langs, v.Lang)
			if !ok {
				return Resolved{}, false
			}
			return Resolved{PageID: p.PageID, Lang: p.Lang, Title: p.Title, Body: p.Body, UpdatedAt: p.UpdatedAt, ShowUpdated: p.ShowUpdated}, true
		}
	}
	return Resolved{}, false
}

// ResolveCommand is the store-backed lookup. err is set only when the read
// itself failed; found is false when no live page carries the command.
func (s Store) ResolveCommand(cmd string, langs []string) (Resolved, bool, error) {
	vars, err := s.LiveVariants()
	if err != nil {
		return Resolved{}, false, err
	}
	r, ok := ResolveCommand(vars, cmd, langs)
	return r, ok, nil
}

// MenuEntries is the page part of a bot's command menu for one language: one
// entry per page, using the command and title of its version in the first of
// langs that has one.
func MenuEntries(vars []LiveVariant, langs []string) []MenuEntry {
	ids := map[int]bool{}
	var order []int
	for _, v := range vars {
		if !ids[v.PageID] {
			ids[v.PageID] = true
			order = append(order, v.PageID)
		}
	}
	sort.Ints(order)
	seen := map[string]bool{}
	var out []MenuEntry
	for _, id := range order {
		var first LiveVariant
		for _, v := range vars {
			if v.PageID == id {
				first = v
				break
			}
		}
		v, ok := pickVariant(vars, id, langs, first.Lang)
		if !ok {
			continue
		}
		cmd := CommandName(v.Slug)
		if cmd == "" || seen[cmd] {
			continue
		}
		desc := strings.TrimSpace(v.Title)
		if desc == "" {
			desc = cmd
		}
		if r := []rune(desc); len(r) > menuDescMaxLen {
			desc = string(r[:menuDescMaxLen])
		}
		seen[cmd] = true
		out = append(out, MenuEntry{Command: cmd, Description: desc})
	}
	return out
}
