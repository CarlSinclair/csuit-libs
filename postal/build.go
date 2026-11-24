package postal

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const geoBase = "https://download.geonames.org/export/zip/"

// BuildFile downloads the GeoNames postal-code archive for a country,
// validates its contents and returns the <CC>.txt payload gzipped, ready to
// be written to data/<CC>.txt.gz. GeoNames data is licensed CC BY 4.0 and must
// be credited by the product that shows it.
func BuildFile(ctx context.Context, client *http.Client, userAgent, country string) ([]byte, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if !validCountry(country) {
		return nil, fmt.Errorf("postal build: bad country %q", country)
	}
	if userAgent == "" {
		userAgent = "postal-resolver/1.0"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geoBase+country+".zip", nil)
	if err != nil {
		return nil, fmt.Errorf("postal build %s: %w", country, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("postal build %s: download: %w", country, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("postal build %s: download status %d", country, resp.StatusCode)
	}
	zb, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("postal build %s: read: %w", country, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		return nil, fmt.Errorf("postal build %s: zip: %w", country, err)
	}
	for _, f := range zr.File {
		if f.Name != country+".txt" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("postal build %s: open entry: %w", country, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, 256<<20))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("postal build %s: read entry: %w", country, err)
		}
		data = trimColumns(data)
		if _, err := parseGeo(bytes.NewReader(data), time.Time{}); err != nil {
			return nil, fmt.Errorf("postal build %s: downloaded file rejected: %w", country, err)
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return nil, fmt.Errorf("postal build %s: gzip: %w", country, err)
		}
		if _, err := zw.Write(data); err != nil {
			return nil, fmt.Errorf("postal build %s: gzip write: %w", country, err)
		}
		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("postal build %s: gzip close: %w", country, err)
		}
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("postal build %s: %s.txt not in archive", country, country)
}

func trimColumns(raw []byte) []byte {
	var out bytes.Buffer
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 5 {
			continue
		}
		out.WriteString(strings.Join(f[:5], "\t"))
		out.WriteByte('\n')
	}
	return out.Bytes()
}
