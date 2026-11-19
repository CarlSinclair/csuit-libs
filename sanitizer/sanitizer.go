// Package sanitizer turns an untrusted upload into a file the application
// itself produced. Images are decoded and re-encoded, PDFs are rasterized and
// rebuilt from the page images, and video is probed and transcoded by a queue
// worker (video support is added separately). The detected type, never the file name or the client-supplied
// content type, decides what happens; the extension must agree with it.
package sanitizer

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/filesig"
	"github.com/CarlSinclair/csuit-libs/logger"
)

// Limits bounds the work one upload may cause. Zero fields take the defaults.
type Limits struct {
	MaxInputBytes int64
	MaxImageSide  int
	MaxPDFPages   int
	PDFDPI        int
	PDFTimeout    time.Duration
	JPEGQuality   int
	LowerPriority bool
}

func (l Limits) withDefaults() Limits {
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = 50 << 20
	}
	if l.MaxImageSide <= 0 {
		l.MaxImageSide = 10000
	}
	if l.MaxPDFPages <= 0 {
		l.MaxPDFPages = 500
	}
	if l.PDFDPI <= 0 {
		l.PDFDPI = 150
	}
	if l.PDFTimeout <= 0 {
		l.PDFTimeout = 90 * time.Second
	}
	if l.JPEGQuality <= 0 {
		l.JPEGQuality = 85
	}
	return l
}

// Result is a sanitized file ready to be stored.
type Result struct {
	Data []byte
	Ext  string
	Kind filesig.Kind
}

var (
	ErrUnsupported = errors.New("unsupported or unrecognized file type")
	ErrMismatch    = errors.New("file contents do not match the file extension")
	ErrTooLarge    = errors.New("file is too large")
)

// expectedKind maps a lowercase extension to the kind its contents must have.
func expectedKind(ext string) filesig.Kind {
	switch ext {
	case ".jpg", ".jpeg":
		return filesig.JPEG
	case ".png":
		return filesig.PNG
	case ".gif":
		return filesig.GIF
	case ".webp":
		return filesig.WebP
	case ".pdf":
		return filesig.PDF
	case ".mp4", ".mov":
		return filesig.MP4
	case ".webm":
		return filesig.WebM
	case ".avi":
		return filesig.AVI
	}
	return filesig.Unknown
}

// Identify reads the file head from r and checks it against the extension of
// name. The returned bytes are the whole input read so far, so callers that
// pass a non-seekable reader do not lose them: use Process instead of calling
// this directly for uploads.
func identify(head []byte, name string) (filesig.Kind, error) {
	ext := strings.ToLower(filepath.Ext(name))
	want := expectedKind(ext)
	if want == filesig.Unknown {
		return filesig.Unknown, fmt.Errorf("%w: extension %q", ErrUnsupported, ext)
	}
	got := filesig.Detect(head)
	if got == filesig.Unknown {
		return filesig.Unknown, fmt.Errorf("%w: contents of %q", ErrUnsupported, name)
	}
	if got != want {
		return got, fmt.Errorf("%w: %q is %s", ErrMismatch, name, got)
	}
	return got, nil
}

// Process sanitizes an image or PDF upload held in memory-bounded form. Video
// is not handled here because it is too heavy to run inline; use the video
// queue for it. name is the client file name, used only for its extension.
func Process(r io.Reader, name string, lim Limits) (*Result, error) {
	lim = lim.withDefaults()
	data, err := io.ReadAll(io.LimitReader(r, lim.MaxInputBytes+1))
	if err != nil {
		logger.Error("sanitizer: reading upload %q failed: %v", name, err)
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(data)) > lim.MaxInputBytes {
		logger.Warn("sanitizer: upload %q exceeds %d bytes", name, lim.MaxInputBytes)
		return nil, ErrTooLarge
	}
	head := data
	if len(head) > filesig.HeadSize {
		head = head[:filesig.HeadSize]
	}
	kind, err := identify(head, name)
	if err != nil {
		logger.Warn("sanitizer: rejected upload %q: %v", name, err)
		return nil, err
	}
	switch {
	case kind.IsImage():
		return sanitizeImage(data, kind, name, lim)
	case kind == filesig.PDF:
		return sanitizePDF(data, name, lim)
	}
	logger.Warn("sanitizer: %q is %s, which Process does not handle inline", name, kind)
	return nil, fmt.Errorf("%w: %s must go through the video queue", ErrUnsupported, kind)
}
