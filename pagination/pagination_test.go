package pagination

import "testing"

func TestVisibleWindow(t *testing.T) {
	cases := []struct {
		name                   string
		current, total, window int
		wantStart, wantEnd     int
	}{
		{"fewer pages than window", 1, 3, 5, 1, 3},
		{"current at start", 1, 20, 5, 1, 5},
		{"current in middle", 10, 20, 5, 8, 12},
		{"current at end", 20, 20, 5, 16, 20},
		{"current near end, odd window", 19, 20, 5, 16, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end := visibleWindow(c.current, c.total, c.window)
			if start != c.wantStart || end != c.wantEnd {
				t.Errorf("visibleWindow(%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.current, c.total, c.window, start, end, c.wantStart, c.wantEnd)
			}
		})
	}
}
