package sanitizer

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return append(b.Bytes(), []byte("TRAILING-PAYLOAD")...)
}

func TestProcessImageStripsTrailingData(t *testing.T) {
	res, err := Process(bytes.NewReader(pngBytes(8, 8)), "a.png", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Ext != ".png" || bytes.Contains(res.Data, []byte("TRAILING-PAYLOAD")) {
		t.Fatalf("not sanitized: ext=%s", res.Ext)
	}
}

func TestProcessRejectsExtensionMismatch(t *testing.T) {
	if _, err := Process(bytes.NewReader(pngBytes(8, 8)), "a.jpg", Limits{}); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestProcessRejectsOversizeDimensions(t *testing.T) {
	if _, err := Process(bytes.NewReader(pngBytes(50, 50)), "a.png", Limits{MaxImageSide: 10}); err == nil {
		t.Fatal("expected dimension error")
	}
}

func TestProcessRejectsUnknown(t *testing.T) {
	if _, err := Process(strings.NewReader("hello world, not a file"), "a.png", Limits{}); err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestBuildPDF(t *testing.T) {
	dir := t.TempDir()
	var pages []string
	for i := 0; i < 2; i++ {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 30, 40)), nil); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "p"+string(rune('a'+i))+".jpg")
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, p)
	}
	out, err := buildPDF(pages, 150)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "%PDF-1.4") || !strings.Contains(s, "/Count 2") || !strings.HasSuffix(s, "%%EOF\n") {
		t.Fatalf("unexpected pdf structure")
	}
}
