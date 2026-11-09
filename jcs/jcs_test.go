package jcs

import "testing"

func TestMarshal_SortsKeysAndFormatsNumbers(t *testing.T) {
	in := map[string]interface{}{
		"b":     1,
		"a":     "x",
		"c":     []interface{}{3, 2, 1},
		"z":     map[string]interface{}{"n": 5000, "m": true},
		"total": 5400,
	}
	got, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"a":"x","b":1,"c":[3,2,1],"total":5400,"z":{"m":true,"n":5000}}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestMarshal_NoHTMLEscaping(t *testing.T) {
	got, err := Marshal(map[string]interface{}{"u": "a<b>&c"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{"u":"a<b>&c"}` {
		t.Fatalf("HTML chars were escaped: %s", got)
	}
}

func TestMarshal_ControlCharEscaping(t *testing.T) {
	got, err := Marshal(map[string]interface{}{"s": "line1\nline2\ttab"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{"s":"line1\nline2\ttab"}` {
		t.Fatalf("got %s", got)
	}
}

func TestMarshal_RejectsNonIntegerBigFloat(t *testing.T) {
	if _, err := Marshal(map[string]interface{}{"x": 1.5}); err == nil {
		t.Fatal("expected error for non-integer number")
	}
}

func TestMarshal_Deterministic(t *testing.T) {
	a, _ := Marshal(map[string]interface{}{"x": 1, "y": 2})
	b, _ := Marshal(map[string]interface{}{"y": 2, "x": 1})
	if string(a) != string(b) {
		t.Fatalf("not deterministic: %s vs %s", a, b)
	}
}
