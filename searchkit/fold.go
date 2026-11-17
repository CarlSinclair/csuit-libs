// Package searchkit normalizes text for keyword search so that every index
// and every fallback scan treats the same input the same way: case, accents,
// width variants and script-specific spelling variants are folded, and text
// is split into words that suit the script it is written in.
package searchkit

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var specialFolds = map[rune]string{
	'ß': "ss",
	'æ': "ae",
	'œ': "oe",
	'ø': "o",
	'ł': "l",
	'đ': "d",
	'ð': "d",
	'þ': "th",
	'ı': "i",
	'ħ': "h",
	'ς': "σ",
	'ى': "ي",
	'ة': "ه",
}

// Fold lowercases s and removes distinctions a searcher does not type:
// accents on Latin, Greek, Cyrillic, Arabic and Hebrew letters, the
// diaeresis on Russian yo, Arabic vowel marks, tatweel and common letter
// variants, zero-width characters and full or half width forms. Marks that
// change the letter itself (Cyrillic short i and Ukrainian yi, kana voicing
// marks, Indic vowel signs) are kept. Input in decomposed or composed form
// gives the same result.
func Fold(s string) string {
	s = norm.NFKD.String(s)
	var b strings.Builder
	b.Grow(len(s))
	var base rune
	for _, r := range s {
		switch {
		case r == '​':
			b.WriteByte(' ')
			base = ' '
			continue
		case unicode.Is(unicode.Cf, r), r == 'ـ':
			continue
		case unicode.In(r, unicode.Mn, unicode.Me):
			if dropMark(base, r) {
				continue
			}
			b.WriteRune(r)
			continue
		}
		r = unicode.ToLower(r)
		base = r
		if rep, ok := specialFolds[r]; ok {
			b.WriteString(rep)
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}

func dropMark(base, mark rune) bool {
	switch {
	case unicode.Is(unicode.Cyrillic, base):
		if mark == '̆' {
			return false
		}
		if mark == '̈' {
			return base == 'е'
		}
		return true
	case unicode.Is(unicode.Latin, base), unicode.Is(unicode.Greek, base),
		unicode.Is(unicode.Arabic, base), unicode.Is(unicode.Hebrew, base):
		return true
	}
	return false
}

const (
	classSeparator = iota
	classWord
	classUnsegmented
	classMark
)

func isUnsegmented(r rune) bool {
	return r == 'ー' || unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana,
		unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

// IsUnsegmented reports whether r belongs to a script written without spaces
// between words (Chinese, Japanese, Thai and similar), where a single
// character can be a whole word.
func IsUnsegmented(r rune) bool { return isUnsegmented(r) }

func classify(r rune) int {
	switch {
	case unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me):
		return classMark
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		if isUnsegmented(r) {
			return classUnsegmented
		}
		return classWord
	}
	return classSeparator
}

func tokens(s string, index bool) []string {
	var out []string
	seen := map[string]bool{}
	add := func(w string) {
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	var run []rune
	cls := classSeparator
	flush := func() {
		switch cls {
		case classWord:
			if len(run) >= 2 {
				add(string(run))
			}
		case classUnsegmented:
			if index || len(run) == 1 {
				for _, r := range run {
					add(string(r))
				}
			}
			for i := 0; i+1 < len(run); i++ {
				add(string(run[i : i+2]))
			}
		}
		run = run[:0]
	}
	for _, r := range Fold(s) {
		c := classify(r)
		if c == classMark {
			if cls != classSeparator {
				run = append(run, r)
			}
			continue
		}
		if c != cls {
			flush()
			cls = c
		}
		if c != classSeparator {
			run = append(run, r)
		}
	}
	flush()
	return out
}

// Words splits a search query into folded words. Words of other scripts
// shorter than two letters are dropped. Chinese, Japanese, Thai and similar
// text without spaces becomes overlapping two-character pieces (a single
// character stays as it is), which is what IndexWords stores.
func Words(s string) []string { return tokens(s, false) }

// IndexWords is Words for the text being indexed: unsegmented scripts also
// yield each single character, so one-character queries find them.
func IndexWords(s string) []string { return tokens(s, true) }

// Match reports whether every word of query is the start of some word in
// the given fields. It is the scan used when no search index is available,
// and it agrees with what the indexed search is meant to return. A query with
// no usable words matches nothing, so callers decide what an empty query shows.
func Match(query string, fields ...string) bool {
	q := Words(query)
	if len(q) == 0 {
		return false
	}
	var have []string
	for _, f := range fields {
		have = append(have, IndexWords(f)...)
	}
	for _, w := range q {
		found := false
		for _, h := range have {
			if strings.HasPrefix(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
