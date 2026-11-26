// Package postal resolves a postal code to place data (city, region, and for
// Brazil street and district).
//
// Brazil is served live from ViaCEP with BrasilAPI as a fallback because
// Brazilian codes are street-level and no static dataset matches them. Every
// other country is served from GeoNames postal-code files (gzipped, embedded
// by the product and passed to New as an fs.FS), so lookups need no third-party call at request time and the
// customer's postal code is never sent to an outside service.
package postal

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid     = errors.New("postal: invalid postal code")
	ErrNotFound    = errors.New("postal: postal code not found")
	ErrUnsupported = errors.New("postal: country not supported")
	ErrUpstream    = errors.New("postal: lookup service unavailable")
)

type Place struct {
	Country    string `json:"country"`
	Postal     string `json:"postal"`
	Street     string `json:"street,omitempty"`
	District   string `json:"district,omitempty"`
	City       string `json:"city"`
	Region     string `json:"region,omitempty"`
	RegionCode string `json:"region_code,omitempty"`
	Approx     bool   `json:"approx,omitempty"`
	Source     string `json:"source"`
}

var patterns = map[string]*regexp.Regexp{
	"BR": regexp.MustCompile(`^[0-9]{8}$`),
	"US": regexp.MustCompile(`^[0-9]{5}$`),
	"CA": regexp.MustCompile(`^[A-Z][0-9][A-Z][0-9][A-Z][0-9]$`),
	"GB": regexp.MustCompile(`^[A-Z]{1,2}[0-9][A-Z0-9]?[0-9][A-Z]{2}$`),
	"PT": regexp.MustCompile(`^[0-9]{7}$`),
	"DE": regexp.MustCompile(`^[0-9]{5}$`),
	"FR": regexp.MustCompile(`^[0-9]{5}$`),
	"ES": regexp.MustCompile(`^[0-9]{5}$`),
	"IT": regexp.MustCompile(`^[0-9]{5}$`),
	"MX": regexp.MustCompile(`^[0-9]{5}$`),
	"AR": regexp.MustCompile(`^([A-Z][0-9]{4}[A-Z]{3}|[0-9]{4})$`),
	"NL": regexp.MustCompile(`^[0-9]{4}[A-Z]{2}$`),
}

var generic = regexp.MustCompile(`^[A-Z0-9]{3,10}$`)

// minPrefix lists countries whose GeoNames file holds only the outward part of
// the code. A full code is matched by shortening it down to this length.
var minPrefix = map[string]int{"GB": 2, "CA": 3, "IE": 3, "NL": 4, "PT": 4, "AR": 4}

// Normalize upper-cases the code, drops spaces and hyphens, and validates it
// for the country. US ZIP+4 is reduced to the 5-digit ZIP.
func Normalize(country, code string) (string, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return "", ErrUnsupported
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == ' ' || r == '-' || r == '.' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	c := b.String()
	if country == "US" && len(c) == 9 {
		c = c[:5]
	}
	if re, ok := patterns[country]; ok {
		if !re.MatchString(c) && !prefixOK(country, c) {
			return "", ErrInvalid
		}
		return c, nil
	}
	if !generic.MatchString(c) {
		return "", ErrInvalid
	}
	return c, nil
}

func prefixOK(country, c string) bool {
	n, ok := minPrefix[country]
	return ok && len(c) >= n && generic.MatchString(c)
}

type cacheEntry struct {
	places  []Place
	err     error
	expires time.Time
}

type ttlCache struct {
	mu  sync.Mutex
	max int
	m   map[string]cacheEntry
}

func newCache(max int) *ttlCache { return &ttlCache{max: max, m: make(map[string]cacheEntry)} }

func (c *ttlCache) get(k string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.expires) {
		delete(c.m, k)
		return cacheEntry{}, false
	}
	return e, true
}

func (c *ttlCache) put(k string, e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		c.m = make(map[string]cacheEntry)
	}
	c.m[k] = e
}
