package workflow

// w72_analysis_mechanics_test.go (polyforge#72, u1 — AC4/AC5/AC6's shared
// base): the analysis-section mechanics the daemon's narrow write path,
// the failure comment and the unconfirmed gate consume — the
// machine-readable unconfirmed marker, the section-only replacement, and
// the 1–7 item count. Expected values are hand-derived from the ticket's
// settled shape (ruling 9: `<!-- pf:auto-analysis <task id> unconfirmed
// -->`; ruling 17: count the top-level 1–7 numbers), never copied from
// the implementation.

import (
	"strings"
	"testing"
)

// w72Ticket is a ticket body whose analysis section a task has partly
// written (two of the 7 items) under an unconfirmed auto-analysis marker.
// The sections after `## 分析` must survive a replacement untouched.
var w72Ticket = "## 背景\n\none line\n\n## 分析\n\n" +
	AutoAnalysisMarker("task_0123456789abcdef") + "\n\n" +
	"### 1. 改动范围\n\n- internal/x\n\n### 2. 证明命令\n\n- go test\n\n## Acceptance\n\n- AC1 passes\n"

func TestW72AutoAnalysisMarker(t *testing.T) {
	got := AutoAnalysisMarker("task_0123456789abcdef")
	if want := "<!-- pf:auto-analysis task_0123456789abcdef unconfirmed -->"; got != want {
		t.Fatalf("AutoAnalysisMarker = %q, want %q", got, want)
	}
}

func TestW72AnalysisUnconfirmed(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want bool
	}{
		{"unconfirmed marker inside the section", w72Ticket, true},
		{"marker confirmed away", strings.Replace(w72Ticket, AutoAnalysisMarker("task_0123456789abcdef")+"\n", "", 1), false},
		{"marker outside the section is not the section's", "## 分析\n\nsome text\n\n## 背景\n\n" + AutoAnalysisMarker("task_0123456789abcdef") + "\n", false},
		{"no marker at all", "## 分析\n\n免分析（2026-11-16，owner）\n", false},
		{"no section", "## 背景\n\nx\n", false},
	}
	for _, c := range cases {
		if got := AnalysisUnconfirmed(c.doc); got != c.want {
			t.Errorf("%s: AnalysisUnconfirmed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestW72ReplaceAnalysisSection(t *testing.T) {
	// Replace the section of an existing ticket: only the analysis
	// section's body changes; heading spelling, the sections before and
	// after and their bytes stay as they were (AC4: owner edits elsewhere
	// survive a daemon write).
	const body = "<!-- pf:auto-analysis task_ffffffffffffffff unconfirmed -->\n\n### 1. 改动范围\n\n- new draft\n"
	got, found := ReplaceAnalysisSection(w72Ticket, body)
	if !found {
		t.Fatalf("ReplaceAnalysisSection found = false, want true (the ticket has a section)")
	}
	if strings.Contains(got, "### 2. 证明命令") {
		t.Errorf("the old section body must be gone, still carries the second item:\n%s", got)
	}
	if !strings.Contains(got, "## 分析\n") {
		t.Errorf("the analysis heading must survive the replacement:\n%s", got)
	}
	if !strings.Contains(got, "- new draft") || !strings.Contains(got, AutoAnalysisMarker("task_ffffffffffffffff")) {
		t.Errorf("the new body must be in the section:\n%s", got)
	}
	if !strings.Contains(got, "## 背景\n\none line\n") {
		t.Errorf("the section before must be untouched:\n%s", got)
	}
	if !strings.Contains(got, "## Acceptance\n\n- AC1 passes\n") {
		t.Errorf("the section after must be untouched:\n%s", got)
	}
	// HasAnalysis of the replaced doc still passes, and the old task's
	// marker no longer marks it unconfirmed for the old task id... the
	// presence check is id-agnostic, so the restamped marker still reads
	// unconfirmed (the daemon stamps the CURRENT task's marker).
	if !HasAnalysis(got) || !AnalysisUnconfirmed(got) {
		t.Errorf("a replaced section with a marker must still count as analysis and unconfirmed:\n%s", got)
	}

	// A ticket with no section: a new `## 分析` section is appended at
	// the end and nothing before it changes.
	fresh := "## 背景\n\nan idea\n"
	got, found = ReplaceAnalysisSection(fresh, "### 1. 改动范围\n\n- draft\n")
	if found {
		t.Fatalf("ReplaceAnalysisSection found = true, want false (no section existed)")
	}
	if !strings.HasPrefix(got, fresh) {
		t.Errorf("the existing body must be untouched before the appended section:\n%s", got)
	}
	if i := strings.Index(got, "## 分析"); i < 0 || !strings.Contains(got[i:], "### 1. 改动范围") {
		t.Errorf("a new analysis section must carry the body at the end:\n%s", got)
	}
	if !HasAnalysis(got) {
		t.Errorf("the appended section must satisfy HasAnalysis:\n%s", got)
	}
}

func TestW72AnalysisItemCount(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want int
	}{
		{"two of seven items", w72Ticket, 2},
		{"all seven", "## 分析\n\n1. a\n\n2. b\n\n3. c\n\n4. d\n\n5. e\n\n6. f\n\n7. g\n", 7},
		{"plain numbered items at column zero", "## 分析\n\n1. one\n2. two\n", 2},
		{"nested and out-of-range numbers do not count", "## 分析\n\n### 2. x\n\n  8. deep\n\n9. nine\n", 2},
		{"numbers outside the section do not count", "## 分析\n\nfree text\n\n## Acceptance\n\n3. not an analysis item\n", 0},
		{"empty section", "## 分析\n\n", 0},
	}
	for _, c := range cases {
		if got := AnalysisItemCount(c.doc); got != c.want {
			t.Errorf("%s: AnalysisItemCount = %d, want %d", c.name, got, c.want)
		}
	}
}
