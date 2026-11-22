// gocommentonly answers whether two Go source files differ only in
// comments (and the whitespace comments cause): the dev channel's one
// exception on the client-server interface paths (polyforge#219). The
// installer builds this tool from the green commit's own tree and calls
// it on the two versions of every modified .go file the diff lands on
// an interface path; this file is that one source, read by the
// repository's tests from the same tree (never a second copy).
//
// Usage: gocommentonly <old> <new>
//
// exit 0 with the line "comment-only" when the two files carry the same
// Go code; any non-zero exit (with a one-line reason on stdout) means
// "not comment-only" — an unparseable version, a changed directive
// comment, or different code tokens. The caller treats every non-zero
// exit as a refusal; this tool never guesses a pass (fail closed).
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gocommentonly <old> <new>")
		os.Exit(2)
	}
	oldSrc, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("not comment-only: cannot read " + os.Args[1] + ": " + err.Error())
		os.Exit(2)
	}
	newSrc, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Println("not comment-only: cannot read " + os.Args[2] + ": " + err.Error())
		os.Exit(2)
	}
	if ok, why := judge(string(oldSrc), string(newSrc)); !ok {
		fmt.Println("not comment-only: " + why)
		os.Exit(1)
	}
	fmt.Println("comment-only")
}

// judge answers whether two Go sources differ only in comments (and
// the whitespace comments cause). Two gates, both settled by the
// owner (2026-11-22), both fail closed:
//
//   - Q2 B, the directive sets: the comment lines that carry a Go
//     toolchain directive — //go:* (build, embed, generate, linkname,
//     …), // +build, //line, /*line and //export — change compilation,
//     so a pair whose sets differ refuses. The set also covers the
//     cgo presence: a file importing "C" enters the comparison with
//     its EVERY comment in the set — a cgo preamble (the comment block
//     above import "C") is compiled code that no prefix can tell
//     apart from an ordinary comment, so a cgo file passes only with
//     its comments unchanged. This is a conservative narrowing of the
//     settled rule: it never passes a pair the bare rule would refuse.
//   - Q3 A, the token streams: both ends must parse (go/parser,
//     either side failing refuses), then the comment-free go/scanner
//     token sequences — kinds and literals — must be equal, positions
//     ignored and a written semicolon equal to an auto-inserted one
//     (the literal is dropped for SEMICOLON). Stricter than the
//     ticket's "same grammar without comments": a reformatted file
//     refuses too, which the fail-closed bias accepts.
func judge(oldSrc, newSrc string) (pass bool, why string) {
	oldF, err := parser.ParseFile(token.NewFileSet(), "old.go", oldSrc, parser.ParseComments)
	if err != nil {
		return false, "the old version does not parse: " + oneLine(err)
	}
	newF, err := parser.ParseFile(token.NewFileSet(), "new.go", newSrc, parser.ParseComments)
	if err != nil {
		return false, "the new version does not parse: " + oneLine(err)
	}
	if oldD, newD := directiveSet(oldF), directiveSet(newF); !equalLines(oldD, newD) {
		return false, "the directive comments differ (//go:*, // +build, //line, //export, cgo)"
	}
	if !equalTokens(oldSrc, newSrc) {
		return false, "the code tokens differ"
	}
	return true, ""
}

// oneLine condenses a parse error to its first line.
func oneLine(err error) string {
	msg := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// directiveSet answers the file's directive-comment inventory: every
// comment line that carries a toolchain directive prefix and, for a
// cgo file (import "C" on either side of the pair — the caller
// compares both sets), every comment line at all (the preamble is
// compiled code indistinguishable from a plain comment by prefix).
func directiveSet(f *ast.File) []string {
	cgo := false
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			cgo = true
			break
		}
	}
	var out []string
	for _, g := range f.Comments {
		for _, c := range g.List {
			for _, ln := range strings.Split(c.Text, "\n") {
				if cgo || isDirective(ln) {
					out = append(out, strings.TrimRight(ln, " \t"))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// isDirective answers whether one raw comment line carries a Go
// toolchain directive. The prefixes are matched on the raw text the
// parser kept (// or /* delimiters included); a false positive — an
// ordinary comment that happens to start like a directive — only
// makes the set larger, i.e. the judgment stricter, never looser.
func isDirective(line string) bool {
	switch {
	case strings.HasPrefix(line, "//go:"),
		strings.HasPrefix(line, "// +build"),
		strings.HasPrefix(line, "//line "),
		strings.HasPrefix(line, "//line\t"),
		strings.HasPrefix(line, "/*line "),
		strings.HasPrefix(line, "/*line\t"),
		line == "//export", strings.HasPrefix(line, "//export "):
		return true
	}
	return false
}

// equalLines answers whether two sorted line inventories are equal.
func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalTokens answers whether the two sources' comment-free token
// streams are equal (kind and literal, positions ignored, semicolon
// literals normalized).
func equalTokens(a, b string) bool {
	ta, tb := tokens(a), tokens(b)
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

// tokens scans one source's comment-free token stream. The parser has
// already accepted both files, so a scan error cannot appear here; an
// ILLEGAL token still simply compares unequal (fail closed).
func tokens(src string) []string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile("src.go", fset.Base(), len(src)), []byte(src), nil, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok == token.SEMICOLON {
			lit = ""
		}
		out = append(out, tok.String()+" "+lit)
	}
}
