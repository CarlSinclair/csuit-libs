package legalpages

import "testing"

func TestPlainText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"heading", "## Delivery\nWe deliver.", "Delivery\nWe deliver."},
		{"bold and italic", "We are **open** from *6pm* to __midnight__.", "We are open from 6pm to midnight."},
		{"link", "See [our map](https://example.com/map \"Map\") today.", "See our map (https://example.com/map) today."},
		{"image keeps its alt text", "![The shop](https://example.com/a.png)", "The shop"},
		{"bullets", "- one\n* two\n  + three", "• one\n• two\n  • three"},
		{"code", "Use `PIX` here", "Use PIX here"},
		{"blank lines collapse", "a\n\n\n\n\nb", "a\n\nb"},
		{"windows line endings", "a\r\n\r\nb", "a\n\nb"},
		{"underscores inside words stay", "snake_case_name", "snake_case_name"},
		{"empty", "  \n ", ""},
	}
	for _, c := range cases {
		if got := PlainText(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
