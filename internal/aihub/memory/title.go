package memory

// title.go: the row title. A memory written without a title gets one
// derived from its body at create time, so recall listings carry a
// readable label for every entry. The stored document's frontmatter keeps
// the title as requested; a derived title lives on the row only.

import (
	"strings"
	"unicode/utf8"
)

// titleMaxRunes bounds a derived title, the trailing "…" included.
const titleMaxRunes = 80

// rowTitle answers the title a memories row carries for a document: the
// explicit title when it has non-space content (kept verbatim), else the
// title derived from the body (nil when the body has no usable line).
func rowTitle(explicit *string, body string) *string {
	if explicit != nil && strings.TrimSpace(*explicit) != "" {
		return explicit
	}
	return deriveTitle(body)
}

// deriveTitle answers the body's first non-empty line with its leading
// markdown markers (heading #, quote >, list - * +, task box, numbered
// 1. / 1)) stripped, trimmed and cut on a rune boundary to titleMaxRunes
// ("…" appended when cut). Code-fence delimiters and thematic breaks /
// setext underlines carry no text and are skipped. nil when no line
// survives.
func deriveTitle(body string) *string {
	for _, line := range strings.Split(body, "\n") {
		t := stripLineMarkers(line)
		if t == "" || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || isRuleLine(t) {
			continue
		}
		if utf8.RuneCountInString(t) > titleMaxRunes {
			rs := []rune(t)
			t = strings.TrimSpace(string(rs[:titleMaxRunes-1])) + "…"
		}
		return &t
	}
	return nil
}

// stripLineMarkers trims a line and removes its leading block markers
// until none is left ("> - # x" → "x").
func stripLineMarkers(line string) string {
	t := strings.TrimSpace(line)
	for {
		prev := t
		switch {
		case strings.HasPrefix(t, "#"):
			n := len(t) - len(strings.TrimLeft(t, "#"))
			if n == len(t) || t[n] == ' ' || t[n] == '\t' {
				t = t[n:]
			}
		case strings.HasPrefix(t, ">"):
			t = t[1:]
		case t == "-" || t == "*" || t == "+" || t == "[ ]" || t == "[x]" || t == "[X]":
			t = ""
		case len(t) >= 2 && strings.ContainsRune("-*+", rune(t[0])) && (t[1] == ' ' || t[1] == '\t'):
			t = t[2:]
		case strings.HasPrefix(t, "[ ] ") || strings.HasPrefix(t, "[x] ") || strings.HasPrefix(t, "[X] "):
			t = t[4:]
		default:
			n := len(t) - len(strings.TrimLeft(t, "0123456789"))
			if n > 0 && n <= 9 && n < len(t) && (t[n] == '.' || t[n] == ')') {
				switch {
				case n+1 == len(t): // a bare marker ("1.")
					t = ""
				case t[n+1] == ' ' || t[n+1] == '\t':
					t = t[n+2:]
				}
			}
		}
		t = strings.TrimSpace(t)
		if t == prev {
			return t
		}
	}
}

// isRuleLine reports a line made only of one of - * _ = (three or more,
// spaces allowed): a thematic break or setext underline.
func isRuleLine(t string) bool {
	s := strings.ReplaceAll(t, " ", "")
	if len(s) < 3 {
		return false
	}
	return strings.ContainsRune("-*_=", rune(s[0])) && strings.Trim(s, s[:1]) == ""
}
