// Command locreport prints, as a markdown table, the code lines per package
// of two commits — base, head and the change — non-test and test code in
// separate columns. pf CI's loc-report job writes it into every pull
// request's job summary; locally:
//
//	go run ./tools/locreport $(git merge-base origin/main HEAD) HEAD
//
// It reads the two commits' trees from the repository it runs in (any
// directory of it), never the working tree. The counting rule is the
// report's header (rules below), stated once. When the repository's non-test
// net change exceeds defaultThreshold lines either way, it also prints a
// GitHub ::warning annotation to stderr; the exit status is 0 either way
// (non-zero only when it cannot read the commits).
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// defaultThreshold is the one place the warning threshold is configured: a
// report whose repository-wide non-test net change exceeds it (either way)
// prints a ::warning annotation. The exit status stays 0.
const defaultThreshold = 1000

// rules is the report's header: what is counted and how.
const rules = "Counted: Go files (`*.go`) and `workflows/**/scripts/**/*.py`. A line " +
	"counts when it still holds a non-blank character once comments are removed: " +
	"blank lines and comment-only lines do not count, and a Python docstring (a " +
	"statement made only of string literals, f- and b-strings excepted) is a comment. " +
	"Test code: Go `*_test.go` and anything under a `testdata/` directory; Python " +
	"under `scripts/tests/`. Packages: `internal/<x>` is one row (its whole subtree), " +
	"any other Go path is its top-level directory, and a `workflows/**/scripts` " +
	"directory is one row (its `tests/` in the test columns)."

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("locreport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	threshold := fs.Int("threshold", defaultThreshold, "warn when the repository's non-test net change exceeds this many lines")
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: locreport [-threshold N] <base-rev> <head-rev>") }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	base, head := fs.Arg(0), fs.Arg(1)
	before, err := countTree(base)
	if err != nil {
		fmt.Fprintln(stderr, "locreport:", err)
		return 1
	}
	after, err := countTree(head)
	if err != nil {
		fmt.Fprintln(stderr, "locreport:", err)
		return 1
	}
	net := writeTable(stdout, base, head, before, after, *threshold)
	if net > *threshold || -net > *threshold {
		fmt.Fprintf(stderr, "::warning title=Code size::Non-test code lines changed by %s across the repository (threshold %d); the job summary has the per-package table\n", delta(net), *threshold)
	}
	return 0
}

// lines is one package's count: non-test and test code lines.
type lines struct{ code, test int }

// countTree counts the code lines of a commit's tree per package.
func countTree(rev string) (map[string]lines, error) {
	listing, err := gitOutput(nil, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	type file struct {
		path, blob string
	}
	var files []file
	for _, entry := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || fields[0] == "120000" {
			continue
		}
		if _, _, counted := classify(path); counted {
			files = append(files, file{path, fields[2]})
		}
	}
	var ids bytes.Buffer
	for _, f := range files {
		ids.WriteString(f.blob + "\n")
	}
	raw, err := gitOutput(&ids, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	counts := map[string]lines{}
	r := bufio.NewReader(bytes.NewReader(raw))
	for _, f := range files {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %v", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file --batch: unexpected header %q", header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: unexpected header %q", header)
		}
		src := make([]byte, size+1) // the content and its trailing newline
		if _, err := io.ReadFull(r, src); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %v", err)
		}
		pkg, test, _ := classify(f.path)
		c := counts[pkg]
		count := countGo
		if strings.HasSuffix(f.path, ".py") {
			count = countPython
		}
		n := count(src[:size])
		if test {
			c.test += n
		} else {
			c.code += n
		}
		counts[pkg] = c
	}
	return counts, nil
}

func gitOutput(stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// classify names a path's package row and whether it is test code; counted
// is false for a file the report does not count.
func classify(path string) (pkg string, test, counted bool) {
	parts := strings.Split(path, "/")
	if strings.HasSuffix(path, ".py") {
		// workflows/**/scripts/**/*.py: the row is the scripts directory.
		for i := 1; i < len(parts)-1 && parts[0] == "workflows"; i++ {
			if parts[i] == "scripts" {
				return strings.Join(parts[:i+1], "/"), parts[i+1] == "tests", true
			}
		}
		return "", false, false
	}
	if !strings.HasSuffix(path, ".go") {
		return "", false, false
	}
	switch {
	case len(parts) == 1:
		pkg = "."
	case parts[0] == "internal" && len(parts) > 2:
		pkg = "internal/" + parts[1]
	default:
		pkg = parts[0]
	}
	test = strings.HasSuffix(path, "_test.go") ||
		strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/")
	return pkg, test, true
}

// countGo counts the lines of Go source that hold a non-blank character
// once every comment is blanked out.
func countGo(src []byte) int {
	text := append([]byte(nil), src...)
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			blank(text[file.Offset(pos) : file.Offset(pos)+len(lit)])
		}
	}
	return nonBlankLines(text)
}

// blank overwrites b with spaces, keeping its newlines.
func blank(b []byte) {
	for i, c := range b {
		if c != '\n' {
			b[i] = ' '
		}
	}
}

func nonBlankLines(text []byte) int {
	n := 0
	for _, line := range bytes.Split(text, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// writeTable writes the report and returns the repository's non-test net
// change.
func writeTable(w io.Writer, base, head string, before, after map[string]lines, threshold int) int {
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var total0, total1 lines
	for _, name := range sorted {
		total0.code += before[name].code
		total0.test += before[name].test
		total1.code += after[name].code
		total1.test += after[name].test
	}
	net := total1.code - total0.code
	fmt.Fprintf(w, "## Code lines per package: `%s` → `%s`\n\n%s\n\n", base, head, rules)
	fmt.Fprintf(w, "Repository non-test net change: **%s** lines (warning threshold: ±%d).\n\n", delta(net), threshold)
	fmt.Fprintln(w, "| package | non-test base | non-test head | non-test Δ | test base | test head | test Δ |")
	fmt.Fprintln(w, "|---|--:|--:|--:|--:|--:|--:|")
	for _, name := range sorted {
		writeRow(w, name, before[name], after[name])
	}
	writeRow(w, "total", total0, total1)
	return net
}

func writeRow(w io.Writer, name string, b, h lines) {
	fmt.Fprintf(w, "| %s | %d | %d | %s | %d | %d | %s |\n", name, b.code, h.code, delta(h.code-b.code), b.test, h.test, delta(h.test-b.test))
}

func delta(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// countPython counts the lines of Python source that hold a non-blank
// character once every comment and docstring is blanked out. A docstring is
// a logical line made only of string literals (implicit concatenation
// included) none of which is an f- or b-string; a logical line ends at a
// newline outside brackets, strings and backslash continuations.
func countPython(src []byte) int {
	text := append([]byte(nil), src...)
	var strs [][2]int   // the current logical line's string literals
	onlyStrings := true // the current logical line holds nothing else
	depth := 0
	endLine := func() {
		if onlyStrings {
			for _, s := range strs {
				blank(text[s[0]:s[1]])
			}
		}
		strs, onlyStrings = strs[:0], true
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '#':
			j := i
			for j < len(src) && src[j] != '\n' {
				j++
			}
			blank(text[i:j])
			i = j
		case c == '\n':
			if depth == 0 {
				endLine()
			}
			i++
		case c == '\\' && i+1 < len(src) && src[i+1] == '\n':
			i += 2
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
		case c == '\'' || c == '"':
			end := scanPyString(src, i)
			strs = append(strs, [2]int{i, end})
			i = end
		case c == '_' || c >= 0x80 || (c|0x20) >= 'a' && (c|0x20) <= 'z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= 0x80 || (src[j]|0x20) >= 'a' && (src[j]|0x20) <= 'z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			prefix := strings.ToLower(string(src[i:j]))
			if j < len(src) && (src[j] == '\'' || src[j] == '"') && len(prefix) <= 2 && strings.Trim(prefix, "rbuf") == "" {
				end := scanPyString(src, j)
				if strings.ContainsAny(prefix, "fb") {
					onlyStrings = false
				} else {
					strs = append(strs, [2]int{i, end})
				}
				i = end
				continue
			}
			onlyStrings = false
			i = j
		default:
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
			}
			onlyStrings = false
			i++
		}
	}
	endLine()
	return nonBlankLines(text)
}

// scanPyString returns the offset just past the string literal whose opening
// quote is at src[q]. A backslash always takes the next byte with it (in a
// raw string it still keeps a quote from ending the literal); an
// unterminated single-quoted literal ends at its line's end.
func scanPyString(src []byte, q int) int {
	quote := src[q]
	triple := q+2 < len(src) && src[q+1] == quote && src[q+2] == quote
	i := q + 1
	if triple {
		i = q + 3
	}
	for i < len(src) {
		switch {
		case src[i] == '\\':
			i += 2
		case triple && src[i] == quote && i+2 < len(src) && src[i+1] == quote && src[i+2] == quote:
			return i + 3
		case !triple && src[i] == quote:
			return i + 1
		case !triple && src[i] == '\n':
			return i
		default:
			i++
		}
	}
	return len(src)
}
