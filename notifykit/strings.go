package notifykit

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed locales/*.json
var stringsFS embed.FS

var (
	stringsOnce sync.Once
	catalogs    map[string]map[string]string
)

func loadStrings() {
	catalogs = map[string]map[string]string{}
	entries, err := stringsFS.ReadDir("locales")
	if err != nil {
		logger.Error("notifykit: reading shared strings failed: %v", err)
		return
	}
	for _, e := range entries {
		b, err := stringsFS.ReadFile("locales/" + e.Name())
		if err != nil {
			logger.Error("notifykit: reading %s failed: %v", e.Name(), err)
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			logger.Error("notifykit: parsing %s failed: %v", e.Name(), err)
			continue
		}
		catalogs[strings.TrimSuffix(e.Name(), ".json")] = m
	}
}

// SharedStrings returns the danger-zone wording in a language (English when
// unknown). Products merge it into their strings (i18nkit.MergeShared).
func SharedStrings(lang string) map[string]string {
	stringsOnce.Do(loadStrings)
	lang = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(lang, "_", "-")))
	if i := strings.Index(lang, "-"); i > 0 {
		lang = lang[:i]
	}
	if m, ok := catalogs[lang]; ok {
		return m
	}
	return catalogs["en"]
}
