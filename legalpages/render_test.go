package legalpages

import "testing"

func TestSafeHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs", "a\n\nb", "<p>a</p>\n<p>b</p>\n"},
		{"line break", "a\nb", "<p>a<br>\nb</p>\n"},
		{"heading", "## Hi", "<h2>Hi</h2>\n"},
		{"bold italic strike", "**b** _i_ *j* ~~s~~", "<p><strong>b</strong> <em>i</em> <em>j</em> <del>s</del></p>\n"},
		{"snake case untouched", "my_var_name", "<p>my_var_name</p>\n"},
		{"inline code shielded", "`a_b_c **x**`", "<p><code>a_b_c **x**</code></p>\n"},
		{"lists", "- a\n- b\n\n1. c\n2. d", "<ul>\n<li>a</li>\n<li>b</li>\n</ul>\n<ol>\n<li>c</li>\n<li>d</li>\n</ol>\n"},
		{"quote merged", "> a\n> b", "<blockquote>a<br>\nb</blockquote>\n"},
		{"rule", "a\n\n---\n\nb", "<p>a</p>\n<hr>\n<p>b</p>\n"},
		{"fence", "```\n<b>x</b>\n\n_y_\n```", "<pre><code>&lt;b&gt;x&lt;/b&gt;\n\n_y_</code></pre>\n"},
		{"unterminated fence", "```\nx", "<pre><code>x</code></pre>\n"},
		{"link", "[t](https://e.com/a_b_c)", "<p><a href=\"https://e.com/a_b_c\" rel=\"noopener nofollow\">t</a></p>\n"},
		{"js link dropped", "[t](javascript:alert)", "<p>t</p>\n"},
		{"image", "![alt](https://e.com/i.png)", "<p><img src=\"https://e.com/i.png\" alt=\"alt\" loading=\"lazy\"></p>\n"},
		{"js image dropped", "![alt](javascript:x)", "<p>alt</p>\n"},
		{"raw html escaped", "<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>\n"},
		{"table", "| a | b |\n|:--|--:|\n| 1 | 2 |", "<table>\n<thead><tr><th align=\"left\">a</th><th align=\"right\">b</th></tr></thead>\n<tbody>\n<tr><td align=\"left\">1</td><td align=\"right\">2</td></tr>\n</tbody>\n</table>\n"},
		{"pipe without separator", "a | b", "<p>a | b</p>\n"},
	}
	for _, c := range cases {
		if got := string(SafeHTML(c.in)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
