package localemeta

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

var Prefixes = []string{"locale_"}

// Load, when set, is called before every read so an app can lazily build its
// catalogs the first time they are needed.
var Load func()

func hasMetaPrefix(k string) bool {
	for _, p := range Prefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

var (
	localeMetaMu sync.RWMutex
	localeMeta   = map[string]map[string]string{}
)

// Record keeps the locale-describing entries (locale_*) of one
// catalog file so they can be read back for that language alone, without the
// English fallback the message bundle applies.
func Record(code string, raw []byte) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return
	}
	meta := map[string]string{}
	for k, v := range top {
		if s, ok := v.(string); ok && hasMetaPrefix(k) {
			meta[k] = s
		}
	}
	localeMetaMu.Lock()
	localeMeta[strings.ToLower(code)] = meta
	localeMetaMu.Unlock()
}

func Reset() {
	localeMetaMu.Lock()
	localeMeta = map[string]map[string]string{}
	localeMetaMu.Unlock()
}

func metaBase(code string) string {
	code = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
	if i := strings.IndexByte(code, '-'); i > 0 {
		code = code[:i]
	}
	return code
}

// metaCatalog resolves a language tag to the catalog that serves it: the exact
// code, else the catalog of the same base language ("pt" -> "pt-br").
func metaCatalog(lang string) string {
	c := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
	if c == "" {
		return ""
	}
	if _, ok := localeMeta[c]; ok {
		return c
	}
	base := metaBase(c)
	if _, ok := localeMeta[base]; ok {
		return base
	}
	codes := make([]string, 0, len(localeMeta))
	for code := range localeMeta {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if metaBase(code) == base {
			return code
		}
	}
	return ""
}

// LocaleMeta reads a locale-describing entry ("locale_tag", "locale_flag", ...)
// from that language's own catalog, never from English, so a language that does
// not define one is not mistaken for English. "" when absent.
func Get(lang, key string) string {
	if Load != nil {
		Load()
	}
	localeMetaMu.RLock()
	defer localeMetaMu.RUnlock()
	code := metaCatalog(lang)
	if code == "" {
		return ""
	}
	return strings.TrimSpace(localeMeta[code][key])
}

// GetOr is LocaleMeta with a fallback.
func GetOr(lang, key, def string) string {
	if v := Get(lang, key); v != "" {
		return v
	}
	return def
}

// UsesCommaDecimal reports whether lang writes decimals with a comma, as its
// catalog declares (locale_comma_decimal).
func UsesCommaDecimal(lang string) bool {
	return Get(lang, "locale_comma_decimal") == "1"
}
