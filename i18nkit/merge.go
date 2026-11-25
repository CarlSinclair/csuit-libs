package i18nkit

// MergeShared combines several shared-string sources (twofa, auditlog, and so
// on) into one Shared function. Later sources win on a repeated key.
func MergeShared(sources ...func(lang string) map[string]string) func(lang string) map[string]string {
	return func(lang string) map[string]string {
		out := map[string]string{}
		for _, src := range sources {
			if src == nil {
				continue
			}
			for k, v := range src(lang) {
				out[k] = v
			}
		}
		return out
	}
}
