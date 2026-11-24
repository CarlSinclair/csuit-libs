package twofa

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed locales/*.json
var sharedFS embed.FS

var (
	sharedOnce sync.Once
	shared     map[string]map[string]string
)

func loadShared() {
	shared = map[string]map[string]string{}
	entries, err := sharedFS.ReadDir("locales")
	if err != nil {
		logger.Error("twofa: reading shared strings failed: %v", err)
		return
	}
	for _, e := range entries {
		b, err := sharedFS.ReadFile("locales/" + e.Name())
		if err != nil {
			logger.Error("twofa: reading shared strings %s failed: %v", e.Name(), err)
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			logger.Error("twofa: parsing shared strings %s failed: %v", e.Name(), err)
			continue
		}
		shared[strings.TrimSuffix(e.Name(), ".json")] = m
	}
}

// SharedStrings returns the wording every product shares for 2FA (the code
// field, the method list, the approval link and its email) in a language,
// matched by its base code so "pt-br" and "pt" both find Portuguese. An unknown
// language gets English. Products load it after their own strings so the one
// shared wording wins.
func SharedStrings(lang string) map[string]string {
	sharedOnce.Do(loadShared)
	lang = strings.ToLower(strings.TrimSpace(lang))
	lang = strings.ReplaceAll(lang, "_", "-")
	if i := strings.Index(lang, "-"); i > 0 {
		lang = lang[:i]
	}
	if m, ok := shared[lang]; ok {
		return m
	}
	return shared["en"]
}
