package workflow

// pf233_analysis_redo_test.go (polyforge#233, u1 — AC1/AC2): the redo's
// archive is lossless. Before a redo schedules a new analysis, the active
// `## 分析` section's exact bytes (heading included, CRLF, trailing spaces
// and a missing final newline alike) move to the `## 历次分析` section
// appended after it, and the active section gains the pending redo marker
// the narrow write path reads as unconfirmed. Archiving the same request
// twice changes nothing.

import (
	"strings"
	"testing"
)

// pf233Old is the old analysis exactly as it stood: CRLF line endings, a
// trailing run of spaces and no final newline — the shapes
// analysisSection trims and an archive must not.
const pf233Old = "## 分析\r\n\r\n### 1. 改动范围  \r\n\r\n- a\r\n- b  "

const pf233Redo = "redo_0123456789abcdef"

func pf233Doc() string {
	return "## 背景\n\nx\n\n" + pf233Old + "\n\n## Acceptance\n\n- AC1 x — carried by build\n"
}

func TestPF233PrepareAnalysisRedoArchivesVerbatim(t *testing.T) {
	doc := pf233Doc()
	got, err := PrepareAnalysisRedo(doc, pf233Redo, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("PrepareAnalysisRedo: %v", err)
	}
	if !strings.Contains(got, pf233Old) {
		t.Fatalf("the old analysis must survive byte for byte (heading, CRLF, trailing spaces, no final newline); the archived ticket lost it:\n%s", got)
	}
	if !strings.Contains(got, AnalysisArchiveHeading) {
		t.Fatalf("the ticket must carry the %q section after the active analysis:\n%s", AnalysisArchiveHeading, got)
	}
	if !strings.Contains(got, pf233Redo) {
		t.Fatalf("the archive must name the redo request id %q:\n%s", pf233Redo, got)
	}
	if !strings.Contains(got, "## Acceptance\n\n- AC1 x — carried by build\n") {
		t.Fatalf("the sections outside the analysis and the archive must not change:\n%s", got)
	}
	if !AnalysisUnconfirmed(got) {
		t.Fatalf("the active analysis must carry the unconfirmed marker after the redo:\n%s", got)
	}
	if id, ok := AnalysisRedoRequestID(got); !ok || id != pf233Redo {
		t.Fatalf("the pending redo request id = %q, %v; want %q, true", id, ok, pf233Redo)
	}
	if AnalysisUnconfirmed(doc) {
		t.Fatalf("the input was already marked; the fixture is wrong")
	}

	again, err := PrepareAnalysisRedo(got, pf233Redo, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("a repeated request must be idempotent, got %v", err)
	}
	if again != got {
		t.Fatalf("archiving the same request id twice must change nothing:\nfirst:\n%s\nsecond:\n%s", got, again)
	}

	// A ticket that carried no analysis at all gets one carrying the
	// pending marker, so the owner sees 分析待确认 and the redo task can
	// replace the section through the narrow write path.
	bare := "## 背景\n\nx\n"
	fresh, err := PrepareAnalysisRedo(bare, pf233Redo, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("PrepareAnalysisRedo on a ticket without analysis: %v", err)
	}
	if !HasAnalysis(fresh) || !AnalysisUnconfirmed(fresh) {
		t.Fatalf("a ticket without analysis must gain a marked analysis section, got:\n%s", fresh)
	}
	if strings.Contains(fresh, AnalysisArchiveHeading) {
		t.Fatalf("there was nothing to archive; no %q section may appear:\n%s", AnalysisArchiveHeading, fresh)
	}
	if !strings.Contains(fresh, "## 背景\n\nx\n") {
		t.Fatalf("the existing body must survive:\n%s", fresh)
	}

	// Two active analysis sections make the old text's boundary
	// ambiguous; the redo refuses and the ticket is unchanged instead of
	// guessing which section to replace (owner ruling Q1=B).
	ambiguous := "## 分析\n\na\n\n## 分析\n\nb\n"
	if out, err := PrepareAnalysisRedo(ambiguous, pf233Redo, "2026-11-22T00:00:00Z"); err == nil {
		t.Fatalf("two active analysis sections must be refused, got:\n%s", out)
	}
}
