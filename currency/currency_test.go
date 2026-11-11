package currency

import (
	"errors"
	"testing"
	"time"
)

func TestCleanStep(t *testing.T) {
	cases := []struct {
		cents int64
		cur   string
		want  int64
	}{
		{3, "USD", 5},
		{1234, "USD", 1199},
		{1234, "BRL", 1190},
		{250000, "USD", 250000},
		{0, "USD", 0},
	}
	for _, c := range cases {
		if got := CleanStep(c.cents, c.cur); got != c.want {
			t.Errorf("CleanStep(%d,%s)=%d want %d", c.cents, c.cur, got, c.want)
		}
	}
}

func TestStrictRateAndDerive(t *testing.T) {
	Reset()
	oldFetch := Fetch
	t.Cleanup(func() { Fetch = oldFetch; Reset() })
	calls := 0
	Fetch = func(base string) (map[string]float64, error) {
		calls++
		return map[string]float64{"BRL": 5, "EUR": 0.8}, nil
	}
	r, err := StrictRate("USD", "BRL")
	if err != nil || r != 5 {
		t.Fatalf("USD->BRL=%v %v", r, err)
	}
	r, ok := Rate("BRL", "EUR")
	if !ok || r < 0.159 || r > 0.161 {
		t.Fatalf("derived BRL->EUR=%v %v", r, ok)
	}
	if calls != 1 {
		t.Fatalf("expected one fetch, got %d", calls)
	}
}

func TestStrictRateUnavailable(t *testing.T) {
	Reset()
	oldFetch := Fetch
	t.Cleanup(func() { Fetch = oldFetch; Reset() })
	Fetch = func(string) (map[string]float64, error) { return nil, errors.New("down") }
	if _, err := StrictRate("USD", "EUR"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	cacheMu.Lock()
	cache["USD"] = snapshot{Base: "USD", Rates: map[string]float64{"USD": 1, "EUR": 0.9}, UpdatedAt: time.Now().Add(-100 * time.Hour)}
	cacheMu.Unlock()
	if _, err := StrictRate("USD", "EUR"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale beyond MaxStale must fail, got %v", err)
	}
}
