package postal

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"time"
)

type geoCountry struct {
	byCode  map[string][]Place
	modTime time.Time
}

type geoStore struct {
	fsys fs.FS
	mu   sync.RWMutex
	c    map[string]*geoCountry
}

func newGeoStore(fsys fs.FS) *geoStore { return &geoStore{fsys: fsys, c: make(map[string]*geoCountry)} }

func (g *geoStore) path(country string) string { return country + ".txt.gz" }

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
	if !validCountry(country) || g.fsys == nil {
		return false
	}
	_, err := fs.Stat(g.fsys, g.path(country))
	return err == nil
}

func (g *geoStore) load(country string) (*geoCountry, error) {
	if !validCountry(country) || g.fsys == nil {
		return nil, ErrUnsupported
	}
	g.mu.RLock()
	cur := g.c[country]
	g.mu.RUnlock()
	if cur != nil {
		return cur, nil
	}
	f, err := g.fsys.Open(g.path(country))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("open %s: %w", g.path(country), err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gunzip %s: %w", g.path(country), err)
	}
	defer zr.Close()
	gc, err := parseGeo(zr, time.Time{})
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
		if len(f) < 5 {
			continue
		}
		key := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(f[1]))
		gc.byCode[key] = append(gc.byCode[key], Place{Country: f[0], Postal: f[1], City: f[2], Region: f[3], RegionCode: f[4], Source: "geonames"})
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
