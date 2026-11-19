package pdfkit

import (
	"bytes"
	"embed"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

const (
	FamilySans = "Sans"

	StyleRegular = ""
	StyleBold    = "B"
	StyleItalic  = "I"
)

type Options struct {
	Landscape bool
	Title     string
	Author    string
	Warn      func(format string, args ...any)
}

type Doc struct {
	pdf  *fpdf.Fpdf
	warn func(format string, args ...any)
	w, h float64
	imgs map[string]bool
}

func NewDoc(opt Options) (*Doc, error) {
	orient := "P"
	if opt.Landscape {
		orient = "L"
	}
	pdf := fpdf.New(orient, "mm", "A4", "")
	pdf.SetMargins(10, 10, 10)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetTitle(opt.Title, true)
	pdf.SetAuthor(opt.Author, true)
	for style, file := range map[string]string{
		StyleRegular: "fonts/DejaVuSans.ttf",
		StyleBold:    "fonts/DejaVuSans-Bold.ttf",
		StyleItalic:  "fonts/DejaVuSans-Oblique.ttf",
	} {
		data, err := fontFS.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("pdfkit: read embedded font %s: %w", file, err)
		}
		pdf.AddUTF8FontFromBytes(FamilySans, style, data)
	}
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("pdfkit: register fonts: %w", err)
	}
	pdf.AddPage()
	w, h := pdf.GetPageSize()
	return &Doc{pdf: pdf, warn: opt.Warn, w: w, h: h, imgs: map[string]bool{}}, nil
}

func (d *Doc) Size() (w, h float64) { return d.w, d.h }

func (d *Doc) Raw() *fpdf.Fpdf { return d.pdf }

func (d *Doc) warnf(format string, args ...any) {
	if d.warn != nil {
		d.warn(format, args...)
	}
}

func (d *Doc) Stroke(r, g, b int, width float64) {
	d.pdf.SetDrawColor(r, g, b)
	d.pdf.SetLineWidth(width)
}

func (d *Doc) Fill(r, g, b int) { d.pdf.SetFillColor(r, g, b) }

func (d *Doc) StrokeRect(x, y, w, h float64) { d.pdf.Rect(x, y, w, h, "D") }

func (d *Doc) FillRect(x, y, w, h float64) { d.pdf.Rect(x, y, w, h, "F") }

func (d *Doc) Line(x1, y1, x2, y2 float64) { d.pdf.Line(x1, y1, x2, y2) }

func (d *Doc) Alpha(a float64) { d.pdf.SetAlpha(a, "Normal") }

func (d *Doc) Image(name string, data []byte, imageType string, x, y, w, h float64) {
	if len(data) == 0 {
		d.warnf("pdfkit: image %q is empty, skipped", name)
		return
	}
	if !d.imgs[name] {
		d.pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: strings.ToUpper(imageType)}, bytes.NewReader(data))
		d.imgs[name] = true
	}
	d.pdf.ImageOptions(name, x, y, w, h, false, fpdf.ImageOptions{ImageType: strings.ToUpper(imageType)}, 0, "")
	if err := d.pdf.Error(); err != nil {
		d.warnf("pdfkit: image %q: %v", name, err)
		d.pdf.ClearError()
	}
}

func (d *Doc) Text(style string, size float64, r, g, b int, x, y, w float64, align, border, text string) {
	d.pdf.SetTextColor(r, g, b)
	d.pdf.SetFont(FamilySans, style, size)
	d.pdf.SetXY(x, y)
	d.pdf.CellFormat(w, size*0.45, text, border, 0, align, false, 0, "")
}

func (d *Doc) FitText(style string, size, minSize float64, r, g, b int, x, y, w float64, align, border, text string) {
	for size > minSize {
		d.pdf.SetFont(FamilySans, style, size)
		if d.pdf.GetStringWidth(text) <= w-2 {
			break
		}
		size -= 0.5
	}
	d.Text(style, size, r, g, b, x, y, w, align, border, text)
}

func (d *Doc) Paragraph(style string, size float64, r, g, b int, x, y, w, lineH float64, align, text string) {
	d.pdf.SetTextColor(r, g, b)
	d.pdf.SetFont(FamilySans, style, size)
	d.pdf.SetXY(x, y)
	d.pdf.MultiCell(w, lineH, text, "", align, false)
}

func (d *Doc) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := d.pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdfkit: render: %w", err)
	}
	return buf.Bytes(), nil
}
