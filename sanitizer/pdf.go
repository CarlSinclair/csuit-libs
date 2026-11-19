package sanitizer

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/filesig"
	"github.com/CarlSinclair/csuit-libs/logger"
)

// sanitizePDF rasterizes every page with poppler's pdftoppm and rebuilds a new
// PDF from the page images, so no script, form, link, attachment or font
// program from the original can survive. The rebuilt PDF is written by this
// package (no img2pdf, no ImageMagick).
func sanitizePDF(data []byte, name string, lim Limits) (*Result, error) {
	dir, err := os.MkdirTemp("", "sanitize-pdf-*")
	if err != nil {
		logger.Error("sanitizer: pdf temp dir failed name=%q err=%v", name, err)
		return nil, fmt.Errorf("pdf temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			logger.Error("sanitizer: pdf temp dir %s was not removed: %v", dir, err)
		}
	}()

	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		logger.Error("sanitizer: pdf temp write failed name=%q err=%v", name, err)
		return nil, fmt.Errorf("pdf temp write: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lim.PDFTimeout)
	defer cancel()
	args := []string{"-jpeg", "-jpegopt", "quality=" + strconv.Itoa(lim.JPEGQuality), "-r", strconv.Itoa(lim.PDFDPI),
		"-l", strconv.Itoa(lim.MaxPDFPages + 1), in, filepath.Join(dir, "p")}
	cmd := command(ctx, lim.LowerPriority, "pdftoppm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			logger.Error("sanitizer: pdftoppm timed out after %s name=%q", lim.PDFTimeout, name)
			return nil, fmt.Errorf("pdf rasterize timed out after %s", lim.PDFTimeout)
		}
		logger.Error("sanitizer: pdftoppm failed name=%q err=%v stderr=%q", name, err, msg)
		return nil, fmt.Errorf("pdf rasterize failed: %w", err)
	}

	pages, err := filepath.Glob(filepath.Join(dir, "p-*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("list rendered pages: %w", err)
	}
	if len(pages) == 0 {
		logger.Error("sanitizer: pdftoppm produced no pages name=%q", name)
		return nil, fmt.Errorf("pdf has no renderable pages")
	}
	if len(pages) > lim.MaxPDFPages {
		logger.Warn("sanitizer: pdf %q has more than %d pages", name, lim.MaxPDFPages)
		return nil, fmt.Errorf("pdf has more than %d pages", lim.MaxPDFPages)
	}
	sort.Strings(pages)

	out, err := buildPDF(pages, lim.PDFDPI)
	if err != nil {
		logger.Error("sanitizer: pdf rebuild failed name=%q err=%v", name, err)
		return nil, fmt.Errorf("pdf rebuild: %w", err)
	}
	logger.Info("sanitizer: pdf sanitized name=%q pages=%d bytes=%d", name, len(pages), len(out))
	return &Result{Data: out, Ext: ".pdf", Kind: filesig.PDF}, nil
}

// buildPDF writes a minimal PDF with one full-page JPEG per page.
func buildPDF(pages []string, dpi int) ([]byte, error) {
	var buf bytes.Buffer
	offsets := []int{0}
	obj := func(body string) int {
		offsets = append(offsets, buf.Len())
		id := len(offsets) - 1
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
		return id
	}
	buf.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")

	kids := make([]string, len(pages))
	// Object numbering: 1 catalog, 2 pages tree, then 3 objects per page.
	offsets = append(offsets, 0, 0)
	for i, p := range pages {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read page %d: %w", i+1, err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("page %d is not a valid jpeg: %w", i+1, err)
		}
		space := "/DeviceRGB"
		if cfg.ColorModel == color.GrayModel {
			space = "/DeviceGray"
		}
		w := float64(cfg.Width) * 72 / float64(dpi)
		h := float64(cfg.Height) * 72 / float64(dpi)

		imgID := len(offsets)
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", imgID, cfg.Width, cfg.Height, space, len(raw))
		buf.Write(raw)
		buf.WriteString("\nendstream\nendobj\n")

		content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im0 Do Q", w, h)
		contentID := obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		pageID := obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources << /XObject << /Im0 %d 0 R >> >> >>", w, h, contentID, imgID))
		kids[i] = fmt.Sprintf("%d 0 R", pageID)
	}

	offsets[1] = buf.Len()
	fmt.Fprintf(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = buf.Len()
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), len(pages))

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return buf.Bytes(), nil
}

func command(ctx context.Context, lower bool, name string, args ...string) *exec.Cmd {
	if lower {
		if nice, err := exec.LookPath("nice"); err == nil {
			return exec.CommandContext(ctx, nice, append([]string{"-n", "19", name}, args...)...)
		}
	}
	return exec.CommandContext(ctx, name, args...)
}
