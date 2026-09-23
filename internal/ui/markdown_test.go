//go:build !noai

package ui

import "testing"

func TestMarkdownToPango(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**bold**", "<b>bold</b>"},
		{"- item", "• item"},
		{"plain text", "plain text"},
		{"a & b < c > d", "a &amp; b &lt; c &gt; d"},
		{"`code`", "<tt>code</tt>"},
		{"## Heading", "<b>Heading</b>"},
		{"- **x** and `y`", "• <b>x</b> and <tt>y</tt>"},
		{"line1\nline2", "line1\nline2"},
		{"*italic*", "<i>italic</i>"},
		{"2 * 3 = 6", "2 * 3 = 6"}, // a lone asterisk is literal
		{"**a** then *b*", "<b>a</b> then <i>b</i>"},
		{"unclosed `code", "unclosed `code"}, // no closing marker: left as typed
		{"**", "**"},                         // empty emphasis is not emphasis
		{"`a<b>c`", "<tt>a&lt;b&gt;c</tt>"},  // escaped before markup is added
		{"  - nested", "  • nested"},         // indentation survives
	}
	for _, c := range cases {
		if got := markdownToPango(c.in); got != c.want {
			t.Errorf("markdownToPango(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
