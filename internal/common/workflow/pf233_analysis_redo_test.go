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

func TestPF233R1SecondRedoKeepsBothArchives(t *testing.T) {
	firstID, secondID := "redo_aaaaaaaaaaaaaaaa", "redo_bbbbbbbbbbbbbbbb"
	first, err := PrepareAnalysisRedo(pf233Doc(), firstID, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("first redo: %v", err)
	}
	second, err := PrepareAnalysisRedo(first, secondID, "2026-11-22T01:00:00Z")
	if err != nil {
		t.Fatalf("second redo: %v", err)
	}
	if !strings.Contains(second, pf233Old) {
		t.Fatalf("the first archive must stay byte for byte after a second redo:\n%s", second)
	}
	if !strings.Contains(second, firstID) || !strings.Contains(second, secondID) {
		t.Fatalf("both rounds must be named on the ticket:\n%s", second)
	}
	if n := strings.Count(second, AnalysisArchiveHeading); n != 1 {
		t.Fatalf("a second redo appends to the one archive section, got %d headings:\n%s", n, second)
	}
	active, found := analysisSection(second)
	if !found || strings.Contains(active, firstID) {
		t.Fatalf("the active analysis must carry only the new round's marker, got:\n%s", active)
	}
	if id, ok := AnalysisRedoRequestID(second); !ok || id != secondID {
		t.Fatalf("the active pending request = %q, %v; want %q", id, ok, secondID)
	}
}

func TestPF233R1EnglishAnalysisHeading(t *testing.T) {
	doc := "## Background\n\nx\n\n## Analysis\n\nold english draft\n\n## Acceptance\n\n- AC1 x\n"
	got, err := PrepareAnalysisRedo(doc, pf233Redo, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("PrepareAnalysisRedo: %v", err)
	}
	if !strings.Contains(got, "## Analysis\n\nold english draft") {
		t.Fatalf("the English analysis heading and its text must be archived verbatim:\n%s", got)
	}
	if !AnalysisUnconfirmed(got) {
		t.Fatalf("the English section must gain the unconfirmed marker:\n%s", got)
	}
}

func TestPF233R1ArchiveBeforeActiveRefused(t *testing.T) {
	doc := "## 历次分析\n\n<details><summary>old</summary>\n\n````markdown\n## 分析\n\nold\n````\n</details>\n\n## 分析\n\nactive\n"
	if out, err := PrepareAnalysisRedo(doc, pf233Redo, "2026-11-22T00:00:00Z"); err == nil {
		t.Fatalf("an archive before the active analysis has no clear boundary and must be refused, got:\n%s", out)
	}
}

func TestPF233AnalysisRedoPendingMarker(t *testing.T) {
	got, err := PrepareAnalysisRedo(pf233Doc(), pf233Redo, "2026-11-22T00:00:00Z")
	if err != nil {
		t.Fatalf("PrepareAnalysisRedo: %v", err)
	}
	if !strings.Contains(got, AutoAnalysisMarker(pf233Redo)) {
		t.Fatalf("the active section must carry the pending marker %q:\n%s", AutoAnalysisMarker(pf233Redo), got)
	}
	if id, ok := AnalysisRedoRequestID(got); !ok || id != pf233Redo {
		t.Fatalf("AnalysisRedoRequestID = %q, %v; want %q", id, ok, pf233Redo)
	}
	if _, ok := AnalysisRedoRequestID(pf233Doc()); ok {
		t.Fatal("a ticket without a pending marker must answer none")
	}
}
