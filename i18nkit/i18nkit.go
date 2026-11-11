package i18nkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/CarlSinclair/csuit-libs/localemeta"
	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

type contextKey string

const LocalizerKey contextKey = "localizer"
const LanguageKey contextKey = "lang"

type Kit struct {
	Locales           fs.FS
	Glob              string
	JWTSecret         func() []byte
	PreferredLanguage func(userID int64) (string, error)

	Bundle             *i18n.Bundle
	AvailableLanguages []string
}

func (k *Kit) Init() {
	k.Bundle = i18n.NewBundle(language.English)
	k.Bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	files, err := fs.Glob(k.Locales, k.Glob)
	if err != nil {
		logger.Error("I18n: glob locales failed: %v", err)
	}
	k.AvailableLanguages = k.AvailableLanguages[:0]
	localemeta.Reset()
	for _, f := range files {
		buf, rErr := fs.ReadFile(k.Locales, f)
		if rErr != nil {
			logger.Error("I18n: read %s failed: %v", f, rErr)
			continue
		}
		base := path.Base(f)
		if _, pErr := k.Bundle.ParseMessageFileBytes(buf, base); pErr != nil {
			logger.Error("I18n: parse %s failed: %v", f, pErr)
			continue
		}
		langCode := strings.TrimSuffix(base, ".json")
		localemeta.Record(langCode, buf)
		k.AvailableLanguages = append(k.AvailableLanguages, langCode)
		logger.Info("I18n: Loaded %s", langCode)
	}
}

func (k *Kit) ensure() {
	if k.Bundle == nil {
		k.Init()
	}
}

func (k *Kit) MatchLanguage(raw string) string {
	if l := k.AcceptedLanguage(raw); l != "" {
		return l
	}
	return "en"
}

func (k *Kit) AcceptedLanguage(raw string) string {
	k.ensure()
	have := make(map[string]bool, len(k.AvailableLanguages))
	for _, l := range k.AvailableLanguages {
		have[strings.ToLower(l)] = true
	}
	for _, part := range strings.Split(raw, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		tag = strings.ReplaceAll(tag, "_", "-")
		if tag == "" || tag == "*" {
			continue
		}
		if have[tag] {
			return tag
		}
		if base := strings.SplitN(tag, "-", 2)[0]; have[base] {
			return base
		}
	}
	return ""
}

func (k *Kit) IsSupportedLanguage(code string) bool {
	k.ensure()
	for _, l := range k.AvailableLanguages {
		if strings.EqualFold(l, code) {
			return true
		}
	}
	return false
}

func (k *Kit) LanguageForCountry(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if cc == "" {
		return ""
	}
	k.ensure()
	langs := append([]string(nil), k.AvailableLanguages...)
	sort.Strings(langs)
	for _, lang := range langs {
		for _, c := range strings.Split(localemeta.Get(lang, "locale_countries"), ",") {
			if strings.EqualFold(strings.TrimSpace(c), cc) {
				return lang
			}
		}
	}
	return ""
}

func (k *Kit) LanguageForRegion(cc, region string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	region = strings.ToUpper(strings.TrimSpace(region))
	if cc == "" || region == "" {
		return ""
	}
	k.ensure()
	want := cc + "-" + region
	langs := append([]string(nil), k.AvailableLanguages...)
	sort.Strings(langs)
	for _, lang := range langs {
		for _, r := range strings.Split(localemeta.Get(lang, "locale_regions"), ",") {
			if strings.EqualFold(strings.TrimSpace(r), want) {
				return lang
			}
		}
	}
	return ""
}

func (k *Kit) LocationLanguage(cc, region string) string {
	if l := k.LanguageForRegion(cc, region); l != "" && k.IsSupportedLanguage(l) {
		return l
	}
	if l := k.LanguageForCountry(cc); l != "" && k.IsSupportedLanguage(l) {
		return l
	}
	return ""
}

func capitalize(name string) string {
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func LanguageName(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	name := display.Self.Name(tag)
	if name == "" {
		return code
	}
	return capitalize(name)
}

func LanguageDisplayName(code, viewerLang string) string {
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	viewerTag, err := language.Parse(viewerLang)
	if err != nil {
		viewerTag = language.English
	}
	name := display.Languages(viewerTag).Name(tag)
	if name == "" {
		return code
	}
	return capitalize(name)
}

func ClaimUserID(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return 0, false
		}
		return int64(t), true
	case int:
		if t <= 0 {
			return 0, false
		}
		return int64(t), true
	case int64:
		if t <= 0 {
			return 0, false
		}
		return t, true
	case json.Number:
		n, err := t.Int64()
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func (k *Kit) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		langs := []string{}

		if c, err := r.Cookie("session_token"); err == nil && strings.TrimSpace(c.Value) != "" {
			token, _ := jwt.Parse(c.Value, func(t *jwt.Token) (interface{}, error) {
				if t.Method != jwt.SigningMethodHS256 {
					return nil, fmt.Errorf("unexpected signing method")
				}
				return k.JWTSecret(), nil
			})
			if token != nil && token.Valid {
				if claims, ok := token.Claims.(jwt.MapClaims); ok {
					if uid, ok := ClaimUserID(claims["user_id"]); ok {
						dbLang, lErr := k.PreferredLanguage(uid)
						if lErr != nil {
							logger.Error("I18n: load preferred_language failed user=%v: %v", uid, lErr)
						}
						if dbLang != "" {
							langs = append(langs, dbLang)
						}
					}
				}
			}
		}

		if accept := r.Header.Get("Accept-Language"); accept != "" {
			langs = append(langs, accept)
		}
		langs = append(langs, "en")

		k.ensure()
		localizer := i18n.NewLocalizer(k.Bundle, langs...)

		ctx := context.WithValue(r.Context(), LocalizerKey, localizer)
		ctx = context.WithValue(ctx, "localizer", localizer)
		ctx = context.WithValue(ctx, LanguageKey, k.MatchLanguage(strings.Join(langs, ",")))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
