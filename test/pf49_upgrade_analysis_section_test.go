package test

// carries: docs/upgrade.md, test/w254_docs_set_acceptance_test.go, test/pf49_upgrade_analysis_section_test.go

// pf49_upgrade_analysis_section_test.go: docs/upgrade.md's appended
// second v0.2.1 section (polyforge#49, AC4). The file is append-only and
// byte-pinned (test/w254_docs_set_acceptance_test.go moves the pin in the
// same commit), so the section landed at the file's end and rewrote no
// earlier byte. A later change may append newer sections after it
// (polyforge#50 appended the scope engine's Python rewrite of the same
// v0.2.1 release), so the section is located by its heading, never
// assumed to be the file's last; the file's last `## ` section still has
// to name the release it belongs to. It states the pre-submit analysis check's upgrade impact
// and operation (polyforge#27: an execution ticket on develop or micro
// needs `## 分析` content before `pf submit` takes it or the auto-claim
// pulls it — older queued tickets have none), and corrects the first
// v0.2.1 section's migration claim: the server runs no startup check for
// the `projects.status` migration — until it has run, the requests that
// touch the missing column fail with SQLite's own errors.

import (
	"strings"
	"testing"
)

// pf49UpgradeSections answers docs/upgrade.md's `## `-level sections in
// file order: the heading text, then the section's body (whitespace-
// flattened).
func pf49UpgradeSections(t *testing.T) [][2]string {
	t.Helper()
	raw := w254Read(t, "docs/upgrade.md")
	var out [][2]string
	var cur *string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimSpace(line[3:])
			out = append(out, [2]string{heading, ""})
			cur = &out[len(out)-1][1]
			continue
		}
		if cur != nil {
			*cur += " " + line
		}
	}
	for i := range out {
		out[i][1] = strings.Join(strings.Fields(out[i][1]), " ")
	}
	return out
}

func TestPF49UpgradeAnalysisSection(t *testing.T) {
	sections := pf49UpgradeSections(t)
	// whatever was appended last, the file's last `## ` section names the
	// release it belongs to (`v<version>, …`).
	last := sections[len(sections)-1]
	if !strings.HasPrefix(last[0], "v") || !strings.Contains(last[0], ".") {
		t.Fatalf("docs/upgrade.md's last `## ` section must name the release it belongs to (a `v<version>, …` heading), got %q", last[0])
	}
	// the appended analysis section, located by its heading — a later
	// change may append newer sections of the same release after it.
	analysis := -1
	for i, s := range sections {
		if strings.Contains(s[0], "the pre-submit analysis check") {
			analysis = i
		}
	}
	if analysis < 0 || !strings.Contains(sections[analysis][0], "v0.2.1") {
		t.Fatalf("docs/upgrade.md's appended analysis section (its heading names v0.2.1) must stay, whole: %v", sections)
	}
	body := sections[analysis][1]
	for _, want := range []string{
		// the check itself: an execution ticket on develop or micro
		// needs `## 分析` content; submit refuses, the auto-claim skips
		"`## 分析`",
		"`pf submit` refuses",
		"auto-claim",
		// the operation: queue first, then the analysis or the owner's
		// dated 免分析, before turning the auto-claim back on
		"`pf queue`",
		"免分析",
		"`pf ops auto on`",
		// the correction of the first v0.2.1 section's claim: no
		// startup check — the requests touching the missing column
		// fail with SQLite's own errors
		"no startup check",
		"SQLite",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs/upgrade.md's appended v0.2.1 section must carry %q — the pre-submit analysis check's upgrade impact and operation (queue, analysis or dated 免分析, then auto on), and the correction: no startup check for the projects.status migration, the requests touching the missing column fail with SQLite's own errors (polyforge#49 AC4)", want)
		}
	}
	// and it follows the deactivation section (the append rewrote no
	// earlier byte, so the first v0.2.1 section is still there, whole).
	deactivation := -1
	for i, s := range sections {
		if strings.Contains(s[0], "the project deactivation status") {
			deactivation = i
		}
	}
	if deactivation < 0 || analysis < deactivation {
		t.Fatalf("the appended analysis section must follow the first v0.2.1 section (the project deactivation status): sections %v", sections)
	}
}
