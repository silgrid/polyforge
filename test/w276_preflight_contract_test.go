package test

// w276_preflight_contract_test.go: text contract for the e2e-author
// executability preflight (w276).
//
// The feature-full lane's e2e-author step records, per kept baseline row, a
// four-key preflight (data_source, async_settle, timing_window,
// selector_unique) read from the same targeted baseline run, classifies a
// structural failure (an assertion no correct implementation could pass) and
// fixes it in place or asks via needs_input. The driver half (the completed
// result rejection) is pinned by the driver package's own tests; this file
// pins the shipped contract text and its schema row, which are the only
// mechanical face of the prompt half. Red on the base tree (none of the
// markers existed there).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w276ContractMarkers are the e2e-author contract markers: the section, the
// four fixed keys and their evidence shapes, the verdict pair, the
// structural-failure disposition, the one-run rule, the per-mode rules and
// the schema row.
var w276ContractMarkers = []string{
	"## Executability preflight (the four self-checks)",
	"data_source",
	"async_settle",
	"timing_window",
	"selector_unique",
	"verdict",
	"n/a",
	"structural failure",
	"needs_input",
	"existing targeted baseline run",
	"grep",
	"runtime count",
	"bounded wait",
	"window",
	"append-only",
	"skipped-with-rationale",
	"all-green",
	"## Baseline run",
	"e2e-scenarios.md",
	"required: [title, status, evidence, preflight]",
	"required: [data_source, async_settle, timing_window, selector_unique]",
	"additionalProperties: false",
	"enum: [ok, n/a]",
}

func w276Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func w276Require(t *testing.T, rel, text string, markers []string) {
	t.Helper()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			t.Errorf("%s lacks the marker %q", rel, m)
		}
	}
}

func TestW276PreflightContract(t *testing.T) {
	rel := filepath.Join("workflows", "feature-full", "steps", "e2e-author.md")
	text := w276Read(t, rel)
	w276Require(t, rel, text, w276ContractMarkers)
	// structural is a classification of a baseline red, never a verdict.
	if strings.Contains(text, "enum: [ok, n/a, structural]") || strings.Contains(text, "verdict: structural") {
		t.Errorf("%s offers structural as a preflight verdict", rel)
	}
	guide := filepath.Join("docs", "contributing.md")
	w276Require(t, guide, w276Read(t, guide), []string{"executability preflight", "Executability preflight (the four self-checks)"})
}

// TestW276PreflightCarriers pins the untouched bug-fix repro contract (no
// preflight); the e2e-author markers are TestW276PreflightContract's.
func TestW276PreflightCarriers(t *testing.T) {
	repro := filepath.Join("workflows", "bug-fix", "steps", "repro.md")
	if strings.Contains(w276Read(t, repro), "preflight") {
		t.Errorf("%s gained a preflight: the bug-fix repro contract is untouched", repro)
	}
}
