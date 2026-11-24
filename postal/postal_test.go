package postal

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		country, in, want string
		err               error
	}{
		{"BR", "01310-100", "01310100", nil},
		{"BR", "1234", "", ErrInvalid},
		{"US", "10001-1234", "10001", nil},
		{"GB", "sw1a 1aa", "SW1A1AA", nil},
		{"GB", "SW1A", "SW1A", nil},
		{"CA", "K1A 0B1", "K1A0B1", nil},
		{"PL", "00-950", "00950", nil},
		{"XX", "a b", "", ErrInvalid},
		{"USA", "10001", "", ErrUnsupported},
	}
	for _, c := range cases {
		got, err := Normalize(c.country, c.in)
		if got != c.want || !errors.Is(err, c.err) && err != c.err {
			t.Errorf("Normalize(%q,%q) = %q,%v want %q,%v", c.country, c.in, got, err, c.want, c.err)
		}
	}
}

func TestGeoLookup(t *testing.T) {
	us := "US\t10001\tNew York\tNew York\tNY\n"
	gb := "GB\tSW1A\tLondon\tEngland\tENG\n"
	data := fstest.MapFS{}
	for name, body := range map[string]string{"US.txt.gz": us, "GB.txt.gz": gb} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		data[name] = &fstest.MapFile{Data: buf.Bytes()}
	}
	r := New(data, "test")
	p, err := r.Lookup(context.Background(), "us", "10001-9999")
	if err != nil || len(p) != 1 || p[0].City != "New York" || p[0].Approx {
		t.Fatalf("US lookup = %+v, %v", p, err)
	}
	p, err = r.Lookup(context.Background(), "GB", "SW1A 1AA")
	if err != nil || len(p) != 1 || p[0].City != "London" || !p[0].Approx {
		t.Fatalf("GB lookup = %+v, %v", p, err)
	}
	if _, err = r.Lookup(context.Background(), "US", "99999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing code err = %v", err)
	}
	if _, err = r.Lookup(context.Background(), "JP", "1000001"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported country err = %v", err)
	}
	if !r.Supported("US") || r.Supported("JP") || !r.Supported("br") {
		t.Fatal("Supported mismatch")
	}
}
