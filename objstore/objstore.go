package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrNotConfigured = errors.New("objstore: not configured")
	ErrNotFound      = errors.New("objstore: object not found")
	ErrInvalidKey    = errors.New("objstore: invalid key")
)

type Config struct {
	Endpoint        string
	AccountID       string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
	PublicBase      string
	Region          string
	Insecure        bool
}

func (c Config) Normalize() Config {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.AccountID = strings.TrimSpace(c.AccountID)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.AccessKeyID = strings.TrimSpace(c.AccessKeyID)
	c.SecretAccessKey = strings.TrimSpace(c.SecretAccessKey)
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	c.PublicBase = strings.TrimRight(strings.TrimSpace(c.PublicBase), "/")
	if c.Endpoint == "" && c.AccountID != "" {
		c.Endpoint = c.AccountID + ".r2.cloudflarestorage.com"
	}
	if strings.HasPrefix(c.Endpoint, "http://") {
		c.Insecure = true
	}
	c.Endpoint = strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	c.Region = strings.TrimSpace(c.Region)
	if c.Region == "" {
		if strings.Contains(c.Endpoint, "r2.cloudflarestorage.com") {
			c.Region = "auto"
		} else {
			c.Region = "us-east-1"
		}
	}
	return c
}

func (c Config) Configured() bool {
	n := c.Normalize()
	return n.Endpoint != "" && n.Bucket != "" && n.AccessKeyID != "" && n.SecretAccessKey != ""
}

func (c Config) Partial() bool {
	n := c.Normalize()
	any := n.Endpoint != "" || n.Bucket != "" || n.AccessKeyID != "" || n.SecretAccessKey != ""
	return any && !n.Configured()
}

func (c Config) Missing() []string {
	n := c.Normalize()
	var m []string
	if n.Endpoint == "" {
		m = append(m, "endpoint or account id")
	}
	if n.Bucket == "" {
		m = append(m, "bucket")
	}
	if n.AccessKeyID == "" {
		m = append(m, "access key id")
	}
	if n.SecretAccessKey == "" {
		m = append(m, "secret access key")
	}
	return m
}

type Info struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

type Store struct {
	cl         *minio.Client
	bucket     string
	prefix     string
	publicBase string
}

func New(cfg Config) (*Store, error) {
	c := cfg.Normalize()
	if !c.Configured() {
		return nil, fmt.Errorf("%w: missing %s", ErrNotConfigured, strings.Join(c.Missing(), ", "))
	}
	cl, err := minio.New(c.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKeyID, c.SecretAccessKey, ""),
		Secure: !c.Insecure,
		Region: c.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("objstore: client for bucket %q: %w", c.Bucket, err)
	}
	p := c.Prefix
	if p != "" {
		p += "/"
	}
	return &Store{cl: cl, bucket: c.Bucket, prefix: p, publicBase: c.PublicBase}, nil
}

func ValidKey(key string) error {
	if key == "" || len(key) > 900 {
		return fmt.Errorf("%w: empty or too long", ErrInvalidKey)
	}
	if strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\x00\\") {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %q", ErrInvalidKey, key)
		}
	}
	return nil
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) full(key string) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

func (s *Store) PublicURL(key string) string {
	if s.publicBase == "" || ValidKey(key) != nil {
		return ""
	}
	return s.publicBase + "/" + s.prefix + (&url.URL{Path: key}).EscapedPath()
}

func notFound(err error) bool {
	if err == nil {
		return false
	}
	r := minio.ToErrorResponse(err)
	return r.Code == "NoSuchKey" || r.StatusCode == 404
}

type PutOption func(*minio.PutObjectOptions)

func CacheControl(v string) PutOption {
	return func(o *minio.PutObjectOptions) { o.CacheControl = v }
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string, extra ...PutOption) error {
	fk, err := s.full(key)
	if err != nil {
		return err
	}
	opts := minio.PutObjectOptions{ContentType: contentType}
	if size < 0 {
		opts.PartSize = 8 << 20
	}
	for _, o := range extra {
		o(&opts)
	}
	if _, err := s.cl.PutObject(ctx, s.bucket, fk, r, size, opts); err != nil {
		return fmt.Errorf("objstore: put %q in bucket %q: %w", key, s.bucket, err)
	}
	return nil
}

func (s *Store) Stat(ctx context.Context, key string) (Info, bool, error) {
	fk, err := s.full(key)
	if err != nil {
		return Info{}, false, err
	}
	st, err := s.cl.StatObject(ctx, s.bucket, fk, minio.StatObjectOptions{})
	if err != nil {
		if notFound(err) {
			return Info{}, false, nil
		}
		return Info{}, false, fmt.Errorf("objstore: stat %q in bucket %q: %w", key, s.bucket, err)
	}
	return Info{Key: key, Size: st.Size, ContentType: st.ContentType, ETag: st.ETag, LastModified: st.LastModified}, true, nil
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	fk, err := s.full(key)
	if err != nil {
		return nil, Info{}, err
	}
	obj, err := s.cl.GetObject(ctx, s.bucket, fk, minio.GetObjectOptions{})
	if err != nil {
		return nil, Info{}, fmt.Errorf("objstore: get %q in bucket %q: %w", key, s.bucket, err)
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		if notFound(err) {
			return nil, Info{}, fmt.Errorf("%w: %q", ErrNotFound, key)
		}
		return nil, Info{}, fmt.Errorf("objstore: get %q in bucket %q: %w", key, s.bucket, err)
	}
	return obj, Info{Key: key, Size: st.Size, ContentType: st.ContentType, ETag: st.ETag, LastModified: st.LastModified}, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	fk, err := s.full(key)
	if err != nil {
		return err
	}
	if err := s.cl.RemoveObject(ctx, s.bucket, fk, minio.RemoveObjectOptions{}); err != nil && !notFound(err) {
		return fmt.Errorf("objstore: delete %q in bucket %q: %w", key, s.bucket, err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, keyPrefix string) ([]Info, error) {
	if keyPrefix != "" {
		kp := strings.TrimSuffix(keyPrefix, "/")
		if err := ValidKey(kp); err != nil {
			return nil, err
		}
		keyPrefix = kp + "/"
	}
	var out []Info
	for o := range s.cl.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix + keyPrefix, Recursive: true}) {
		if o.Err != nil {
			return nil, fmt.Errorf("objstore: list %q in bucket %q: %w", keyPrefix, s.bucket, o.Err)
		}
		k := strings.TrimPrefix(o.Key, s.prefix)
		if strings.HasSuffix(k, "/") {
			continue
		}
		out = append(out, Info{Key: k, Size: o.Size, ContentType: o.ContentType, ETag: o.ETag, LastModified: o.LastModified})
	}
	return out, nil
}

func (s *Store) DeletePrefix(ctx context.Context, keyPrefix string) (int, error) {
	items, err := s.List(ctx, keyPrefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if err := s.Delete(ctx, it.Key); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Store) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	fk, err := s.full(key)
	if err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = time.Hour
	}
	u, err := s.cl.PresignedGetObject(ctx, s.bucket, fk, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("objstore: presign %q in bucket %q: %w", key, s.bucket, err)
	}
	return u.String(), nil
}

var contentTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml",
	".pdf": "application/pdf", ".mp4": "video/mp4", ".webm": "video/webm",
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".json": "application/json",
	".txt": "text/plain; charset=utf-8", ".csv": "text/csv; charset=utf-8",
}

func ContentTypeFor(name string) string {
	if ct, ok := contentTypes[strings.ToLower(path.Ext(name))]; ok {
		return ct
	}
	return "application/octet-stream"
}
