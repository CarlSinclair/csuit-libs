package postal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Resolver struct {
	DataDir     string
	HTTP        *http.Client
	UserAgent   string
	TTL         time.Duration
	NegativeTTL time.Duration

	cache *ttlCache
	geo   *geoStore
}

func New(dataDir, userAgent string) *Resolver {
	if userAgent == "" {
		userAgent = "postal-resolver/1.0"
	}
	return &Resolver{
		DataDir:     dataDir,
		HTTP:        &http.Client{Timeout: 5 * time.Second},
		UserAgent:   userAgent,
		TTL:         24 * time.Hour,
		NegativeTTL: time.Hour,
		cache:       newCache(10000),
		geo:         newGeoStore(dataDir),
	}
}

// Supported reports whether lookups for the country can succeed: Brazil
// always, other countries once their GeoNames file has been installed.
func (r *Resolver) Supported(country string) bool {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "BR" {
		return true
	}
	return r.geo.has(country)
}

// Lookup returns the places for a postal code. Errors are ErrInvalid,
// ErrNotFound, ErrUnsupported or an error wrapping ErrUpstream.
func (r *Resolver) Lookup(ctx context.Context, country, code string) ([]Place, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	norm, err := Normalize(country, code)
	if err != nil {
		return nil, err
	}
	key := country + "|" + norm
	if e, ok := r.cache.get(key); ok {
		return e.places, e.err
	}
	var places []Place
	if country == "BR" {
		places, err = r.lookupBR(ctx, norm)
	} else {
		places, err = r.geo.lookup(country, norm)
	}
	switch {
	case err == nil:
		r.cache.put(key, cacheEntry{places: places, expires: time.Now().Add(r.TTL)})
	case errors.Is(err, ErrNotFound):
		r.cache.put(key, cacheEntry{err: err, expires: time.Now().Add(r.NegativeTTL)})
	case errors.Is(err, ErrUpstream):
		return nil, err
	default:
		return nil, fmt.Errorf("postal lookup country=%s code=%s: %w", country, norm, err)
	}
	return places, err
}
