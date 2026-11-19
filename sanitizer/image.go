package sanitizer

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/CarlSinclair/csuit-libs/filesig"
	"github.com/CarlSinclair/csuit-libs/logger"
)

// sanitizeImage decodes and re-encodes so no metadata, trailing data or
// embedded payload survives. WebP input needs golang.org/x/image/webp to be
// registered by the application (a blank import); without it the decode fails
// and the upload is rejected with a logged error.
func sanitizeImage(data []byte, kind filesig.Kind, name string, lim Limits) (*Result, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		logger.Error("sanitizer: image config decode failed name=%q kind=%s err=%v", name, kind, err)
		return nil, fmt.Errorf("read image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > lim.MaxImageSide || cfg.Height > lim.MaxImageSide {
		logger.Warn("sanitizer: image %q is %dx%d, limit is %d per side", name, cfg.Width, cfg.Height, lim.MaxImageSide)
		return nil, fmt.Errorf("image dimensions %dx%d are outside the allowed range", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		logger.Error("sanitizer: image decode failed name=%q kind=%s err=%v", name, kind, err)
		return nil, fmt.Errorf("decode image: %w", err)
	}
	var buf bytes.Buffer
	ext := ".png"
	switch kind {
	case filesig.JPEG:
		ext = ".jpg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: lim.JPEGQuality})
	case filesig.GIF:
		ext = ".gif"
		err = gif.Encode(&buf, img, nil)
	default:
		err = png.Encode(&buf, img)
	}
	if err != nil {
		logger.Error("sanitizer: image re-encode failed name=%q kind=%s err=%v", name, kind, err)
		return nil, fmt.Errorf("encode image: %w", err)
	}
	logger.Info("sanitizer: image sanitized name=%q %s -> %s %dx%d", name, kind, ext, cfg.Width, cfg.Height)
	return &Result{Data: buf.Bytes(), Ext: ext, Kind: kind}, nil
}
