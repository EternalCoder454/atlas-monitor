//go:build !noai

package ui

import "strings"

// pangoEscape escapes the characters that are special in Pango markup.
func pangoEscape(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// markdownToPango converts the small Markdown subset the assistant emits — bold,
// italics, inline code, bullet lists and headings — into Pango markup suitable
// for GtkLabel.SetMarkup. All literal text is escaped first, so the only markup
// in the result is the tags this function adds.
func markdownToPango(md string) string {
	lines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	for i, line := range lines {
		lines[i] = mdLine(line)
	}
	return strings.Join(lines, "\n")
}

func mdLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	bullet := ""
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		bullet = "• "
		trimmed = trimmed[2:]
	}

	heading := false
	for strings.HasPrefix(trimmed, "#") {
		heading = true
		trimmed = trimmed[1:]
	}
	if heading {
		trimmed = strings.TrimLeft(trimmed, " ")
	}

	content := mdInline(pangoEscape(trimmed))
	if heading {
		content = "<b>" + content + "</b>"
	}
	return indent + bullet + content
}

// mdInline rewrites `code`, **bold** and *italics* in a single left-to-right
// pass. This used to be three compiled regexps; one hand-written scan does the
// same job, keeps the regexp engine out of the binary, and leaves a line with
// no markers untouched (the usual case) without copying it.
func mdInline(s string) string {
	if !strings.ContainsAny(s, "`*") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 {
				b.WriteString("<tt>")
				b.WriteString(s[i+1 : i+1+end])
				b.WriteString("</tt>")
				i += end + 2
				continue
			}
		}
		if strings.HasPrefix(s[i:], "**") {
			if body, next, ok := span(s, i+2, "**"); ok {
				b.WriteString("<b>")
				b.WriteString(body)
				b.WriteString("</b>")
				i = next
				continue
			}
		}
		if s[i] == '*' {
			if body, next, ok := span(s, i+1, "*"); ok {
				b.WriteString("<i>")
				b.WriteString(body)
				b.WriteString("</i>")
				i = next
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// span reads a non-empty run of asterisk-free text starting at from and closed
// by close, returning the text and the index just past the closing marker.
func span(s string, from int, close string) (body string, next int, ok bool) {
	end := from
	for end < len(s) && s[end] != '*' {
		end++
	}
	if end == from || !strings.HasPrefix(s[end:], close) {
		return "", 0, false
	}
	return s[from:end], end + len(close), true
}
