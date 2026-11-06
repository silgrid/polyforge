package workflow

// workflow_test: the definitions' frontmatter fence, the shipped lanes' retired keys
// and the attr value domain (the loader's own tests: load_test.go).
// Fixture ids are deliberately provider-qualified placeholders; no real
// model or vendor names appear anywhere in this package.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fmFixture = `---
name: demo
description: a definition
model_candidates: [pi:prov-a/model-a1, pi:prov-a/model-a2, claude:prov-b/model-b1]
---

# body narrative
`

func TestSplitFrontmatter(t *testing.T) {
	front, _, ok := SplitFrontmatter([]byte(fmFixture))
	if !ok {
		t.Fatal("a well-formed fence must split")
	}
	if !strings.Contains(string(front), "model_candidates") {
		t.Fatalf("front block must carry the candidate keys: %q", front)
	}
	if _, _, ok := SplitFrontmatter([]byte("# no frontmatter\n")); ok {
		t.Fatal("a file without a fence must not split")
	}
	if _, _, ok := SplitFrontmatter([]byte("---\nname: torn")); ok {
		t.Fatal("an unterminated fence must not split")
	}
}

// TestShippedBundlesCarryNoKind: the step `kind` concept is retired
// (polyforge#169); no shipped bundle may still declare it.
func TestShippedBundlesCarryNoKind(t *testing.T) {
	paths, _ := filepath.Glob("../../../workflows/*/" + WorkflowFile)
	if len(paths) == 0 {
		t.Fatal("no shipped bundles found")
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "kind:") {
			t.Errorf("%s still declares the retired step kind", p)
		}
	}
}
