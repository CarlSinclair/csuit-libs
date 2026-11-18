package pdfkit

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildStructure(t *testing.T) {
	c := NewCanvas(nil)
	c.Rect(10, 10, 100, 50)
	c.Text(FontBold, 10, 20, 20, "Olá (mundo)")
	c.BarcodeCode128C("12345678", 10, 100, 200, 30)
	img := &Image{Width: 1, Height: 1, Data: []byte{1, 2, 3}}
	out, err := Build(c.Content(), map[string]*Image{"Im1": img})
	if err != nil {
		t.Fatal(err)
	}
	if !IsPDF(out) || !bytes.HasSuffix(out, []byte("%%EOF\n")) {
		t.Fatal("not a pdf")
	}
	s := string(out)
	if !strings.Contains(s, "/XObject << /Im1 8 0 R") || !strings.Contains(s, `Ola \(mundo\)`) {
		t.Fatalf("unexpected body: %s", s)
	}
	if got := strings.Count(s, " 0 obj"); got != 8 {
		t.Fatalf("objects: %d", got)
	}
}

func TestBarcodeRejectsOddLength(t *testing.T) {
	var warned bool
	c := NewCanvas(func(string, ...any) { warned = true })
	c.BarcodeCode128C("123", 0, 0, 100, 10)
	if !warned || len(c.Content()) != 0 {
		t.Fatal("expected warning and no output")
	}
}

func TestFitLines(t *testing.T) {
	got := FitLines("uma descricao bem longa que nao cabe numa linha so", 20, 2)
	if len(got) != 2 || !strings.HasSuffix(got[1], "...") {
		t.Fatalf("%q", got)
	}
}
