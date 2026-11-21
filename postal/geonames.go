package postal

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const geoBase = "https://download.geonames.org/export/zip/"

type geoCountry struct {
	byCode  map[string][]Place
	modTime time.Time
}

type geoStore struct {
	dir string
	mu  sync.RWMutex
	c   map[string]*geoCountry
}

func newGeoStore(dir string) *geoStore { return &geoStore{dir: dir, c: make(map[string]*geoCountry)} }

func (g *geoStore) path(country string) string { return filepath.Join(g.dir, country+".txt") }

func validCountry(c string) bool {
	if len(c) != 2 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func (g *geoStore) has(country string) bool {
	if !validCountry(country) || g.dir == "" {
		return false
	}
	_, err := os.Stat(g.path(country))
	return err == nil
}

func (g *geoStore) load(country string) (*geoCountry, error) {
	if !validCountry(country) || g.dir == "" {
		return nil, ErrUnsupported
	}
	st, err := os.Stat(g.path(country))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("stat %s: %w", g.path(country), err)
	}
	g.mu.RLock()
	cur := g.c[country]
	g.mu.RUnlock()
	if cur != nil && cur.modTime.Equal(st.ModTime()) {
		return cur, nil
	}
	f, err := os.Open(g.path(country))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", g.path(country), err)
	}
	defer f.Close()
	gc, err := parseGeo(f, st.ModTime())
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", g.path(country), err)
	}
	g.mu.Lock()
	g.c[country] = gc
	g.mu.Unlock()
	return gc, nil
}

func parseGeo(r io.Reader, mod time.Time) (*geoCountry, error) {
	gc := &geoCountry{byCode: make(map[string][]Place), modTime: mod}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	rows := 0
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 11 {
			continue
		}
		key := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(f[1]))
		lat, _ := strconv.ParseFloat(f[9], 64)
		lon, _ := strconv.ParseFloat(f[10], 64)
		gc.byCode[key] = append(gc.byCode[key], Place{Country: f[0], Postal: f[1], City: f[2], Region: f[3], RegionCode: f[4], Lat: lat, Lon: lon, Source: "geonames"})
		rows++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, fmt.Errorf("no rows")
	}
	return gc, nil
}

func (g *geoStore) lookup(country, norm string) ([]Place, error) {
	gc, err := g.load(country)
	if err != nil {
		return nil, err
	}
	if p, ok := gc.byCode[norm]; ok {
		return append([]Place(nil), p...), nil
	}
	min, ok := minPrefix[country]
	if !ok {
		return nil, ErrNotFound
	}
	for n := len(norm) - 1; n >= min; n-- {
		if p, ok := gc.byCode[norm[:n]]; ok {
			out := append([]Place(nil), p...)
			for i := range out {
				out[i].Approx = true
			}
			return out, nil
		}
	}
	return nil, ErrNotFound
}

// Refresh downloads the GeoNames postal-code file of each country into
// DataDir, replacing the previous file atomically. GeoNames data is licensed
// CC BY 4.0 and must be credited by the product that shows it. It attempts
// every country and returns the first error.
func (r *Resolver) Refresh(ctx context.Context, countries ...string) error {
	if r.DataDir == "" {
		return fmt.Errorf("postal refresh: DataDir not set")
	}
	if err := os.MkdirAll(r.DataDir, 0o770); err != nil {
		return fmt.Errorf("postal refresh: create %s: %w", r.DataDir, err)
	}
	var first error
	for _, c := range countries {
		c = strings.ToUpper(strings.TrimSpace(c))
		if err := r.refreshOne(ctx, c); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *Resolver) refreshOne(ctx context.Context, c string) error {
	if !validCountry(c) {
		return fmt.Errorf("postal refresh: bad country %q", c)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geoBase+c+".zip", nil)
	if err != nil {
		return fmt.Errorf("postal refresh %s: %w", c, err)
	}
	req.Header.Set("User-Agent", r.UserAgent)
	client := *r.HTTP
	client.Timeout = 2 * time.Minute
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("postal refresh %s: download: %w", c, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("postal refresh %s: download status %d", c, resp.StatusCode)
	}
	zb, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("postal refresh %s: read: %w", c, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		return fmt.Errorf("postal refresh %s: zip: %w", c, err)
	}
	for _, f := range zr.File {
		if f.Name != c+".txt" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("postal refresh %s: open entry: %w", c, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, 256<<20))
		rc.Close()
		if err != nil {
			return fmt.Errorf("postal refresh %s: read entry: %w", c, err)
		}
		if _, err := parseGeo(bytes.NewReader(data), time.Time{}); err != nil {
			return fmt.Errorf("postal refresh %s: downloaded file rejected: %w", c, err)
		}
		tmp := r.geo.path(c) + ".tmp"
		if err := os.WriteFile(tmp, data, 0o660); err != nil {
			return fmt.Errorf("postal refresh %s: write: %w", c, err)
		}
		if err := os.Rename(tmp, r.geo.path(c)); err != nil {
			return fmt.Errorf("postal refresh %s: install: %w", c, err)
		}
		return nil
	}
	return fmt.Errorf("postal refresh %s: %s.txt not in archive", c, c)
}
