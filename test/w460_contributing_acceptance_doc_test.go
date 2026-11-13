// carries: docs/contributing.md, docs/upgrade.md

package test

// w460_contributing_acceptance_doc_test.go: a doc contract.
//
// The contributor guide's filing guidance agrees with the shipped behaviour:
// filing does not grill, `pf submit` refuses a bundled ticket with no
// criterion under `## Acceptance` / `## 验收` unless `--allow-no-acceptance`
// is passed, and the driver reads both headings. docs/upgrade.md records the
// changed submit exit in a section appended at its end.

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

	up := read("docs/upgrade.md")
	// find the section by its own heading, not by position: upgrade.md is
	// append-only, so later tickets' sections follow this one (review finding B1).
	// The heading's own words are the anchor: a ticket reference in the
	// heading is not part of every copy of this file.
	const title = "pf submit refuses an execution ticket without acceptance"
	section := ""
	for _, part := range strings.Split(up, "\n## ")[1:] {
		if strings.HasPrefix(part, title) {
			section = part
			break
		}
	}
	if section == "" {
		t.Fatalf("docs/upgrade.md has no `## ` section headed %q", title)
	}
	for _, want := range []string{"--allow-no-acceptance", "## 验收"} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/upgrade.md's section must name %q (the submit refusal's switch; the driver and the CLI read both acceptance headings)", want)
		}
	}
}
