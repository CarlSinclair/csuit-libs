package pdfkit

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	PageWidth  = 595
	PageHeight = 842

	FontRegular = "F1"
	FontBold    = "F2"
	FontMono    = "F3"
)

type Image struct {
	Width  int
	Height int
	Data   []byte
}

func (i *Image) Valid() bool {
	return i != nil && i.Width > 0 && i.Height > 0 && len(i.Data) > 0
}

type Canvas struct {
	content bytes.Buffer
	warn    func(format string, args ...any)
}

func NewCanvas(warn func(format string, args ...any)) *Canvas {
	return &Canvas{warn: warn}
}

func (p *Canvas) warnf(format string, args ...any) {
	if p.warn != nil {
		p.warn(format, args...)
	}
}

func (p *Canvas) Content() []byte { return p.content.Bytes() }

func (p *Canvas) LineWidth(w float64) { fmt.Fprintf(&p.content, "%.2f w\n", w) }

func (p *Canvas) StrokeRGB(r, g, b float64) {
	fmt.Fprintf(&p.content, "%.3f %.3f %.3f RG\n", r, g, b)
}

func (p *Canvas) FillRGB(r, g, b float64) {
	fmt.Fprintf(&p.content, "%.3f %.3f %.3f rg\n", r, g, b)
}

func (p *Canvas) Rect(x, y, w, h float64) {
	fmt.Fprintf(&p.content, "%.2f %.2f %.2f %.2f re S\n", x, y, w, h)
}

func (p *Canvas) FillRect(x, y, w, h float64) {
	fmt.Fprintf(&p.content, "%.2f %.2f %.2f %.2f re f\n", x, y, w, h)
}

func (p *Canvas) HLine(x1, x2, y float64) {
	fmt.Fprintf(&p.content, "%.2f %.2f m %.2f %.2f l S\n", x1, y, x2, y)
}

func (p *Canvas) VLine(x, y1, y2 float64) {
	fmt.Fprintf(&p.content, "%.2f %.2f m %.2f %.2f l S\n", x, y1, x, y2)
}

func (p *Canvas) Image(name string, x, y, w, h float64) {
	name = strings.TrimSpace(name)
	if name == "" || w <= 0 || h <= 0 {
		return
	}
	fmt.Fprintf(&p.content, "q %.2f 0 0 %.2f %.2f %.2f cm /%s Do Q\n", w, h, x, y, name)
}

func (p *Canvas) ImageFit(name string, img *Image, x, y, w, h float64) {
	if !img.Valid() || strings.TrimSpace(name) == "" || w <= 0 || h <= 0 {
		return
	}
	iw, ih := float64(img.Width), float64(img.Height)
	scale := w / iw
	if s := h / ih; s < scale {
		scale = s
	}
	dw, dh := iw*scale, ih*scale
	p.Image(name, x+(w-dw)/2, y+(h-dh)/2, dw, dh)
}

func (p *Canvas) Text(font string, size int, x, y float64, value string) {
	value = NormalizeText(value)
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	fmt.Fprintf(&p.content, "BT /%s %d Tf %.2f %.2f Td (%s) Tj ET\n", font, size, x, y, escape(value))
}

func (p *Canvas) TextFit(font string, size int, x, y, w float64, value string) {
	p.Text(font, size, x, y, Ellipsize(value, CharsForWidth(w, size)))
}

func (p *Canvas) CenterText(font string, size int, x, y, w float64, value string) {
	value = Ellipsize(NormalizeText(value), CharsForWidth(w, size))
	est := float64(len(value)) * float64(size) * FontWidthFactor(font)
	tx := x + (w-est)/2
	if tx < x+2 {
		tx = x + 2
	}
	p.Text(font, size, tx, y, value)
}

func (p *Canvas) SectionTitle(title string, x, y, w, h float64) {
	p.Rect(x, y, w, h)
	p.FillRGB(0.94, 0.94, 0.94)
	p.FillRect(x+0.5, y+h-15, w-1, 14.5)
	p.FillRGB(0, 0, 0)
	p.Text(FontBold, 8, x+5, y+h-10.5, strings.ToUpper(title))
	p.HLine(x, x+w, y+h-15)
}

func (p *Canvas) Field(label, value string, x, y, w, h float64) {
	p.FieldN(label, value, x, y, w, h, 2, 7)
}

func (p *Canvas) FieldN(label, value string, x, y, w, h float64, maxLines, valueSize int) {
	if maxLines <= 0 {
		maxLines = 1
	}
	if valueSize <= 0 {
		valueSize = 7
	}
	p.Rect(x, y, w, h)
	p.TextFit(FontRegular, 5, x+3, y+h-8, w-6, strings.ToUpper(label))
	lineY := y + h - 18
	step := float64(valueSize + 2)
	for i, line := range FitLines(value, CharsForWidth(w-6, valueSize), maxLines) {
		if lineY-float64(i)*step < y+3 {
			break
		}
		p.Text(FontBold, valueSize, x+3, lineY-float64(i)*step, line)
	}
}

func (p *Canvas) BarcodeCode128C(value string, x, y, w, h float64) {
	value = digitsOnly(value)
	if value == "" || len(value)%2 != 0 || w <= 0 || h <= 0 {
		p.warnf("pdfkit barcode skipped: value must be numeric and even length len=%d", len(value))
		return
	}
	codes := []int{105}
	checksum := 105
	for i, pos := 0, 1; i < len(value); i, pos = i+2, pos+1 {
		pair, err := strconv.Atoi(value[i : i+2])
		if err != nil || pair < 0 || pair > 99 {
			p.warnf("pdfkit barcode skipped: invalid numeric pair=%q err=%v", value[i:i+2], err)
			return
		}
		codes = append(codes, pair)
		checksum += pair * pos
	}
	codes = append(codes, checksum%103, 106)
	total := 0
	for _, c := range codes {
		for _, ch := range code128Patterns[c] {
			total += int(ch - '0')
		}
	}
	quiet := 6.0
	usable := w - quiet*2
	if usable <= 0 {
		usable, quiet = w, 0
	}
	module := usable / float64(total)
	cursor := x + quiet
	p.FillRGB(0, 0, 0)
	for _, c := range codes {
		bar := true
		for _, ch := range code128Patterns[c] {
			seg := float64(ch-'0') * module
			if bar && seg > 0 {
				p.FillRect(cursor, y, seg, h)
			}
			cursor += seg
			bar = !bar
		}
	}
}

func Build(content []byte, images map[string]*Image) ([]byte, error) {
	names := make([]string, 0, len(images))
	for n, img := range images {
		if img.Valid() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	objects := [][]byte{
		[]byte(`<< /Type /Catalog /Pages 2 0 R >>`),
		[]byte(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`),
		nil,
		[]byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`),
		[]byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>`),
		[]byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>`),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)),
	}
	var xobj strings.Builder
	for _, n := range names {
		img := images[n]
		var o bytes.Buffer
		fmt.Fprintf(&o, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", img.Width, img.Height, len(img.Data))
		o.Write(img.Data)
		o.WriteString("\nendstream")
		objects = append(objects, o.Bytes())
		fmt.Fprintf(&xobj, "/%s %d 0 R ", n, len(objects))
	}
	res := `<< /Font << /F1 4 0 R /F2 5 0 R /F3 6 0 R >>`
	if xobj.Len() > 0 {
		res += ` /XObject << ` + xobj.String() + `>>`
	}
	res += ` >>`
	objects[2] = []byte(fmt.Sprintf(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources %s /Contents 7 0 R >>`, PageWidth, PageHeight, res))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(obj)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes(), nil
}

func IsPDF(b []byte) bool { return len(b) >= 5 && string(b[:5]) == "%PDF-" }

func FontWidthFactor(font string) float64 {
	switch strings.ToUpper(strings.TrimSpace(font)) {
	case FontMono:
		return 0.62
	case FontBold:
		return 0.60
	default:
		return 0.56
	}
}

func CharsForWidth(width float64, size int) int {
	if size <= 0 {
		size = 7
	}
	n := int(width / (float64(size) * 0.68))
	if n < 3 {
		n = 3
	}
	return n
}

var normalizer = strings.NewReplacer(
	"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c",
	"Á", "A", "À", "A", "Ã", "A", "Â", "A", "Ä", "A",
	"É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
	"Ó", "O", "Ò", "O", "Õ", "O", "Ô", "O", "Ö", "O",
	"Ú", "U", "Ù", "U", "Û", "U", "Ü", "U",
	"Ç", "C",
	"–", "-", "—", "-", "“", `"`, "”", `"`, "’", "'",
)

func NormalizeText(v string) string { return normalizer.Replace(strings.TrimSpace(v)) }

func escape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, "\r", " ", "\n", " ").Replace(NormalizeText(v))
}

func Ellipsize(value string, max int) string {
	value = strings.TrimSpace(NormalizeText(value))
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return strings.TrimSpace(value[:max-3]) + "..."
}

func FitLines(line string, width, maxLines int) []string {
	line = strings.TrimSpace(NormalizeText(line))
	if line == "" {
		return []string{"-"}
	}
	if width <= 0 {
		width = 40
	}
	if maxLines <= 0 {
		maxLines = 1
	}
	out := []string{}
	for len(line) > width && len(out) < maxLines {
		cut := width
		if idx := strings.LastIndex(line[:width], " "); idx > 8 {
			cut = idx
		}
		part := strings.TrimSpace(line[:cut])
		line = strings.TrimSpace(line[cut:])
		if len(out) == maxLines-1 && line != "" {
			part = Ellipsize(part+" "+line, width)
			line = ""
		}
		out = append(out, part)
	}
	if line != "" && len(out) < maxLines {
		out = append(out, Ellipsize(line, width))
	}
	if len(out) == 0 {
		return []string{"-"}
	}
	return out
}

func digitsOnly(v string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.TrimSpace(v))
}

var code128Patterns = []string{
	"212222", "222122", "222221", "121223", "121322", "131222", "122213", "122312", "132212", "221213",
	"221312", "231212", "112232", "122132", "122231", "113222", "123122", "123221", "223211", "221132",
	"221231", "213212", "223112", "312131", "311222", "321122", "321221", "312212", "322112", "322211",
	"212123", "212321", "232121", "111323", "131123", "131321", "112313", "132113", "132311", "211313",
	"231113", "231311", "112133", "112331", "132131", "113123", "113321", "133121", "313121", "211331",
	"231131", "213113", "213311", "213131", "311123", "311321", "331121", "312113", "312311", "332111",
	"314111", "221411", "431111", "111224", "111422", "121124", "121421", "141122", "141221", "112214",
	"112412", "122114", "122411", "142112", "142211", "241211", "221114", "413111", "241112", "134111",
	"111242", "121142", "121241", "114212", "124112", "124211", "411212", "421112", "421211", "212141",
	"214121", "412121", "111143", "111341", "131141", "114113", "114311", "411113", "411311", "113141",
	"114131", "311141", "411131", "211412", "211214", "211232", "2331112",
}
