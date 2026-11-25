package geo

import (
	"net/http"
	"strings"
)

// RequestLocation returns a human-readable "City, CC" for a request that came
// through Cloudflare, using the CF-IPCity and CF-IPCountry headers. It returns
// an empty string when neither is known, so callers can store NULL or "".
// The IP itself is never returned or stored: location is the audit standard.
func RequestLocation(r *http.Request) string {
	if r == nil {
		return ""
	}
	country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if country == "XX" || country == "T1" {
		country = ""
	}
	city := strings.TrimSpace(r.Header.Get("CF-IPCity"))
	switch {
	case city != "" && country != "":
		return city + ", " + country
	case city != "":
		return city
	}
	return country
}
