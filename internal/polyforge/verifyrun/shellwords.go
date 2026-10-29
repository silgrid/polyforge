package verifyrun

// shellwords.go: the little shell reading verify-run needs to derive a
// unit's re-run command and the test bar's skip form from a planned
// command — word splitting with quotes, and whether the command is one
// simple command (no pipe, list, redirection, subshell or comment at the
// top level), the only shape a flag may be appended to. An unquoted
// expansion (`$NAME`, `${...}`, `$(...)`, a backtick substitution) stays
// inside its word. Every word also keeps its raw text exactly as written:
// a derived command re-emits the planned command's own words raw (so
// quoting and expansions mean what they meant), and quotes only the new
// words it adds.

import (
	"regexp"
	"strings"
)

// shellWords splits cmd into words (quotes removed); see shellWordsRaw.
func shellWords(cmd string) (words []string, simple bool) {
	words, _, simple = shellWordsRaw(cmd)
	return words, simple
}

// shellWordsRaw splits cmd into words and their raw text. simple is false
// when an unquoted operator (`|`, `&`, `;`, `<`, `>`, `(`, `)`), a comment,
// an expansion inside double quotes or an unterminated quote or
// expansion occurs; the words then cover only the text before it.
func shellWordsRaw(cmd string) (words, raws []string, simple bool) {
	var cur strings.Builder
	inWord, start := false, 0
	begin := func(i int) {
		if !inWord {
			start, inWord = i, true
		}
	}
	flush := func(end int) {
		if inWord {
			words = append(words, cur.String())
			raws = append(raws, cmd[start:end])
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return words, raws, false
			}
			begin(i)
			cur.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			begin(i)
			j := i + 1
			for ; j < len(cmd) && cmd[j] != '"'; j++ {
				if cmd[j] == '$' || cmd[j] == '`' {
					return words, raws, false
				}
				if cmd[j] == '\\' && j+1 < len(cmd) {
					j++
				}
				cur.WriteByte(cmd[j])
			}
			if j >= len(cmd) {
				return words, raws, false
			}
			i = j
		case c == '\\' && i+1 < len(cmd):
			begin(i)
			cur.WriteByte(cmd[i+1])
			i++
		case c == ' ' || c == '\t' || c == '\n':
			flush(i)
		case c == '#' && !inWord:
			// a comment: whatever follows is no part of the command, and
			// nothing may be appended after it
			return words, raws, false
		case c == '$' || c == '`':
			end := expansionEnd(cmd, i)
			if end < 0 {
				return words, raws, false
			}
			begin(i)
			cur.WriteString(cmd[i:end])
			i = end - 1
		case strings.IndexByte("|&;<>()", c) >= 0:
			flush(i)
			return words, raws, false
		default:
			begin(i)
			cur.WriteByte(c)
		}
	}
	flush(len(cmd))
	return words, raws, true
}

// expansionEnd answers the index just past the unquoted expansion that
// starts at cmd[i] (`$NAME`, `${...}`, `$(...)` with its nesting and
// quotes, a backtick substitution), or -1 when it does not close.
func expansionEnd(cmd string, i int) int {
	if cmd[i] == '`' {
		for j := i + 1; j < len(cmd); j++ {
			switch cmd[j] {
			case '\\':
				j++
			case '`':
				return j + 1
			}
		}
		return -1
	}
	if i+1 >= len(cmd) {
		return i + 1
	}
	switch cmd[i+1] {
	case '{':
		if j := strings.IndexByte(cmd[i+2:], '}'); j >= 0 {
			return i + 2 + j + 1
		}
		return -1
	case '(':
		depth := 0
		for j := i + 1; j < len(cmd); j++ {
			switch cmd[j] {
			case '\\':
				j++
			case '\'':
				k := strings.IndexByte(cmd[j+1:], '\'')
				if k < 0 {
					return -1
				}
				j += k + 1
			case '"':
				k := j + 1
				for ; k < len(cmd) && cmd[k] != '"'; k++ {
					if cmd[k] == '\\' {
						k++
					}
				}
				if k >= len(cmd) {
					return -1
				}
				j = k
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return j + 1
				}
			}
		}
		return -1
	}
	j := i + 1
	for j < len(cmd) && (cmd[j] == '_' || cmd[j] >= 'a' && cmd[j] <= 'z' || cmd[j] >= 'A' && cmd[j] <= 'Z' || cmd[j] >= '0' && cmd[j] <= '9') {
		j++
	}
	if j == i+1 && j < len(cmd) && strings.IndexByte("?#@*!$-", cmd[j]) >= 0 {
		j++ // a special parameter
	}
	return j
}

var reAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// leadingAssignments answers the NAME=value words a command starts with
// (after an optional `env`), raw as written — the environment a derived
// re-run must keep (e.g. `GOWORK=off`).
func leadingAssignments(cmd string) []string {
	words, raws, _ := shellWordsRaw(cmd)
	i := 0
	if i < len(words) && words[i] == "env" {
		i++
	}
	var out []string
	for ; i < len(words) && reAssignment.MatchString(words[i]); i++ {
		out = append(out, raws[i])
	}
	return out
}

var reBareWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes one NEW word (a test name, a node id) for bash.
func shellQuote(w string) string {
	if reBareWord.MatchString(w) {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// commandWord answers the command word of a simple command (assignments
// and an `env` prefix skipped) and its index ("" = none).
func commandWord(words []string) (string, int) {
	i := 0
	if i < len(words) && words[i] == "env" {
		i++
	}
	for ; i < len(words); i++ {
		if !reAssignment.MatchString(words[i]) {
			return words[i], i
		}
	}
	return "", -1
}

// hasGoTest reports whether a simple command is a `go test` invocation.
func hasGoTest(words []string) bool {
	w, i := commandWord(words)
	return (w == "go" || strings.HasSuffix(w, "/go")) && i+1 < len(words) && words[i+1] == "test"
}
