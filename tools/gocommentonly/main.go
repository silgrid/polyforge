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
	"os"
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

// judge answers whether two Go sources differ only in comments. The
// stub body lands with the file (the signature first, so the unit's
// red is an assertion and never a compile error); the implementation
// is the unit's impl commit.
func judge(oldSrc, newSrc string) (pass bool, why string) {
	return false, "the judge is not implemented yet"
}
