// carries: docs/contributing.md

package test

// w460_contributing_acceptance_doc_test.go: doc contract for (AC4).
//
// The contributor guide's filing guidance agrees with the shipped behaviour:
// filing does not grill, `pf submit` refuses a bundled ticket with no
// criterion under `## Acceptance` / `## 验收` unless `--allow-no-acceptance`
// is passed, and the driver reads both headings.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW460ContributingAcceptanceGuidance(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%v", err)
		}
		return string(b)
	}

	contrib := read("docs/contributing.md")
	const lead = "**The develop lane's gates, in brief**"
	i := strings.Index(contrib, lead)
	if i < 0 {
		t.Fatalf("docs/contributing.md lost its %q paragraph", lead)
	}
	para := contrib[i:]
	if end := strings.Index(para, "\n\n"); end >= 0 {
		para = para[:end]
	}
	// the paragraph is hard-wrapped: judge it on one line
	flat := strings.Join(strings.Fields(para), " ")
	for _, want := range []string{
		"`## Acceptance`",
		"`## 验收`",
		"`pf submit` refuses",
		"--allow-no-acceptance",
		"Filing does not grill",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/contributing.md's develop-gates paragraph must carry %q — filing does not grill; `pf submit` refuses a bundled ticket with no criterion under `## Acceptance` or `## 验收` unless `--allow-no-acceptance` is passed", want)
		}
	}

}
