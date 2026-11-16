package discountpresets

import "testing"

func TestApply(t *testing.T) {
	cases := []struct {
		name  string
		price int64
		kind  string
		value int64
		want  int64
	}{
		{"amount", 10000, KindAmount, 1500, 8500},
		{"amount over price", 1000, KindAmount, 5000, 0},
		{"percentage", 20000, KindPercentage, 10, 18000},
		{"percentage capped", 20000, KindPercentage, 150, 0},
		{"zero value", 5000, KindAmount, 0, 5000},
		{"unknown kind", 5000, "x", 10, 5000},
		{"zero price", 0, KindAmount, 10, 0},
	}
	for _, c := range cases {
		if got := Apply(c.price, c.kind, c.value); got != c.want {
			t.Errorf("%s: Apply(%d,%s,%d)=%d want %d", c.name, c.price, c.kind, c.value, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	good := Preset{Label: " Student ", Kind: KindPercentage, Value: 15}
	if err := good.Validate(); err != nil || good.Label != "Student" {
		t.Fatalf("good preset rejected: %v label=%q", err, good.Label)
	}
	bad := []Preset{
		{Label: "", Kind: KindAmount, Value: 100},
		{Label: "x", Kind: "other", Value: 100},
		{Label: "x", Kind: KindAmount, Value: 0},
		{Label: "x", Kind: KindPercentage, Value: 101},
		{Label: "x", Kind: KindPercentage, Value: 0},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("bad[%d] accepted: %+v", i, p)
		}
	}
}

func TestStoreRejectsBadTable(t *testing.T) {
	if _, err := (Store{Table: "x; DROP TABLE y"}).List(false); err != ErrBadTable {
		t.Fatalf("expected ErrBadTable, got %v", err)
	}
}
