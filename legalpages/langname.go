package legalpages

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	privateLangPrefix = "x-"
	privateLangMaxLen = 48
)

var regionalVariants = []string{
	"zh-Hans", "zh-Hant", "pt-BR", "pt-PT", "es-419", "es-ES", "es-MX",
	"en-GB", "en-US", "en-AU", "en-CA", "fr-CA", "fr-CH", "de-AT", "de-CH",
	"nl-BE", "sr-Cyrl", "sr-Latn",
}

var (
	langIndexOnce   sync.Once
	langIndex       map[string]language.Tag
	regionIndex     map[string]language.Region
	nameTargets     []language.Tag
	suggestionCache sync.Map
)

func foldName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	stripped, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err == nil {
		s = stripped
	}
	var b strings.Builder
	pendingSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

func buildLangIndex() {
	langIndex = map[string]language.Tag{}
	regionIndex = map[string]language.Region{}

	const az = "abcdefghijklmnopqrstuvwxyz"
	var bases []language.Tag
	seen := map[string]bool{}
	tryBase := func(code string) {
		b, err := language.ParseBase(code)
		if err != nil {
			return
		}
		t, err := language.Compose(b)
		if err != nil || seen[t.String()] {
			return
		}
		seen[t.String()] = true
		bases = append(bases, t)
	}
	for _, a := range az {
		for _, b := range az {
			tryBase(string([]rune{a, b}))
		}
	}
	for _, a := range az {
		for _, b := range az {
			for _, c := range az {
				tryBase(string([]rune{a, b, c}))
			}
		}
	}

	english := display.English.Languages()
	var locales []language.Tag
	for _, t := range bases {
		if len(t.String()) == 2 {
			locales = append(locales, t)
		}
		if n := english.Name(t); n != "" && n != t.String() {
			nameTargets = append(nameTargets, t)
		}
	}
	for _, code := range regionalVariants {
		if t, err := language.Parse(code); err == nil {
			nameTargets = append(nameTargets, t)
		}
	}

	put := func(name string, t language.Tag) {
		key := foldName(name)
		if key == "" {
			return
		}
		if _, exists := langIndex[key]; !exists {
			langIndex[key] = t
		}
	}
	for _, t := range nameTargets {
		put(english.Name(t), t)
		put(display.Self.Name(t), t)
		put(t.String(), t)
	}
	for _, loc := range locales {
		namer := display.Languages(loc)
		if namer == nil {
			continue
		}
		for _, t := range nameTargets {
			put(namer.Name(t), t)
		}
	}
	for _, t := range bases {
		put(t.String(), t)
	}

	regionLocales := append([]language.Tag{language.English}, locales...)
	for _, a := range az {
		for _, b := range az {
			r, err := language.ParseRegion(string([]rune{a, b}))
			if err != nil {
				continue
			}
			for _, loc := range regionLocales {
				regionNamer := display.Regions(loc)
				if regionNamer == nil {
					continue
				}
				name := foldName(regionNamer.Name(r))
				if name != "" {
					if _, exists := regionIndex[name]; !exists {
						regionIndex[name] = r
					}
				}
			}
		}
	}
}

func lookupLanguageName(key string) (language.Tag, bool) {
	if t, ok := langIndex[key]; ok {
		return t, true
	}
	words := strings.Fields(key)
	for split := 1; split < len(words); split++ {
		head := strings.Join(words[:split], " ")
		tail := strings.Join(words[split:], " ")
		if t, ok := langIndex[head]; ok {
			if r, ok := regionIndex[tail]; ok {
				if withRegion, err := language.Compose(t, r); err == nil {
					return withRegion, true
				}
			}
		}
		if t, ok := langIndex[tail]; ok {
			if r, ok := regionIndex[head]; ok {
				if withRegion, err := language.Compose(t, r); err == nil {
					return withRegion, true
				}
			}
		}
	}
	return language.Und, false
}

func IsPrivateLang(code string) bool {
	return strings.HasPrefix(code, privateLangPrefix)
}

func SameLang(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// NormalizeLang turns what an admin typed into the code stored on a variant.
// resolved is true when the text named a real language (or was a BCP 47 tag),
// in which case code is that tag in lower case, ready for hreflang. Anything
// else is kept as a private-use code so the page still works, but is never
// offered as an hreflang value.
func NormalizeLang(input string) (code string, resolved bool) {
	input = strings.TrimSpace(input)
	key := foldName(input)
	if key == "" {
		return "", false
	}
	langIndexOnce.Do(buildLangIndex)
	if t, ok := lookupLanguageName(key); ok {
		return strings.ToLower(t.String()), true
	}
	if t, err := language.Parse(input); err == nil && t != language.Und {
		return strings.ToLower(t.String()), true
	}
	private := strings.ReplaceAll(key, " ", "-")
	if utf8.RuneCountInString(private) > privateLangMaxLen {
		private = string([]rune(private)[:privateLangMaxLen])
	}
	return privateLangPrefix + private, false
}

// LanguageLabel renders a stored code the way the editor shows it, e.g.
// "English (en)", with the name in the viewer's language.
func LanguageLabel(code, viewer string) string {
	if IsPrivateLang(code) {
		text := strings.ReplaceAll(strings.TrimPrefix(code, privateLangPrefix), "-", " ")
		if r, size := utf8.DecodeRuneInString(text); size > 0 {
			text = string(unicode.ToUpper(r)) + text[size:]
		}
		return text
	}
	t, err := language.Parse(code)
	if err != nil {
		return code
	}
	namer := display.Languages(viewerTag(viewer))
	if namer == nil {
		namer = display.Languages(language.English)
	}
	name := namer.Name(t)
	if name == "" || name == t.String() {
		return code
	}
	return name + " (" + code + ")"
}

func viewerTag(viewer string) language.Tag {
	if t, err := language.Parse(viewer); err == nil {
		return t
	}
	return language.English
}

// LanguageSuggestions lists language names in the viewer's language for a
// datalist. It is autocomplete only; any text is accepted.
func LanguageSuggestions(viewer string) []string {
	if cached, ok := suggestionCache.Load(viewer); ok {
		return cached.([]string)
	}
	langIndexOnce.Do(buildLangIndex)
	namer := display.Languages(viewerTag(viewer))
	if namer == nil {
		namer = display.Languages(language.English)
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range nameTargets {
		n := namer.Name(t)
		if n == "" || n == t.String() || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	suggestionCache.Store(viewer, out)
	return out
}
