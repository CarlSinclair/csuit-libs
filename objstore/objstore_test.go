package objstore

import (
	"errors"
	"testing"
)

func TestConfigNormalize(t *testing.T) {
	c := Config{AccountID: " acct ", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s", Prefix: "/p/", PublicBase: "https://x.test/"}.Normalize()
	if c.Endpoint != "acct.r2.cloudflarestorage.com" || c.Prefix != "p" || c.PublicBase != "https://x.test" {
		t.Fatalf("normalize: %+v", c)
	}
	if c.Region != "auto" {
		t.Fatalf("region %q", c.Region)
	}
	if h := (Config{Endpoint: "http://minio.local:9000"}).Normalize(); !h.Insecure || h.Endpoint != "minio.local:9000" || h.Region != "us-east-1" {
		t.Fatalf("http endpoint: %+v", h)
	}
	if !c.Configured() || c.Partial() {
		t.Fatal("expected configured")
	}
	if !(Config{Bucket: "b"}).Partial() {
		t.Fatal("expected partial")
	}
	if (Config{}).Partial() || (Config{}).Configured() {
		t.Fatal("empty is neither")
	}
}

func TestNewNotConfigured(t *testing.T) {
	_, err := New(Config{Bucket: "b"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"", "/a", "a//b", "a/../b", "..", "a\\b", "a/./b"} {
		if ValidKey(k) == nil {
			t.Errorf("%q should be invalid", k)
		}
	}
	for _, k := range []string{"a", "a/b.png", "tenant/content/x y.pdf"} {
		if err := ValidKey(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}
}

func TestPublicURL(t *testing.T) {
	s, err := New(Config{Endpoint: "e.test", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s", Prefix: "pre", PublicBase: "https://cdn.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PublicURL("a/b c.png"); got != "https://cdn.test/pre/a/b%20c.png" {
		t.Fatalf("got %q", got)
	}
	if s.PublicURL("../x") != "" {
		t.Fatal("invalid key must give empty url")
	}
}

func TestContentTypeFor(t *testing.T) {
	if ContentTypeFor("a.PNG") != "image/png" || ContentTypeFor("x.zzz") != "application/octet-stream" {
		t.Fatal("content type")
	}
}
