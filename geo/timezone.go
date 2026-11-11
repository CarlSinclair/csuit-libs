package geo

import (
	"net/http"
	"strings"
	"time"
)

func RequestTimezone(r *http.Request) string {
	tz := strings.TrimSpace(r.Header.Get("CF-Timezone"))
	if tz == "" {
		return ""
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ""
	}
	return tz
}
