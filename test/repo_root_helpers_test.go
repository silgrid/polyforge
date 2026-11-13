package test

// Repository-root helpers shared by the acceptance tests that read the
// tree's own files (the merge, CI-gate, blocked-kinds, loc-report and mypy
// acceptance tests). They live in their own file so that every file using
// them compiles wherever the test package does, whichever of those test
// files a copy of this repository carries.

import (
	"os"
	"path/filepath"
	"testing"
)

// e2e344Root is the repository root: the test's cwd when it holds go.mod,
// else its parent (go test runs in the package dir, test/).
func e2e344Root(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
		return wd
	}
	root := filepath.Dir(wd)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found from %s", wd)
	}
	return root
}

// e2e344Read is the content of rel under root; a read error fails the test.
func e2e344Read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
