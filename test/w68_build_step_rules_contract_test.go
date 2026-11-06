package test

// w68_build_step_rules_contract_test.go: the flattened repo-file reader the
// text contracts share. Its own pin (the chore `code` step naming both
// rewind verdict sources) went with the lanes removed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w68Flat reads a repo file and collapses every whitespace run to one
// space, so line wrapping cannot make the text assertions brittle.
func w68Flat(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}
