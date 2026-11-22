package test

// w320_release_boundary_contract_test.go (polyforge#113, polyforge#154):
// one audit, one command — the release's dependency audit is the scope
// engine's own gate. release.sh takes its default audit command from the
// engine's --json --full report and runs it verbatim after the binaries
// build, unconditionally: the release runs no test gate (the green
// integrate batch's own full-gate run is the test evidence), so nothing
// can skip the audit. No hardcoded component audit
// (`cd webui && npm audit …`) remains, and RELEASE_AUDIT_CMD still
// replaces the whole gate. The old boundary wordings ("Two audits, two
// scopes", "a reused green tag skips the test gate") are red.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW320ReleaseBoundary' -count=1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func w320rbScript(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), ".polyforge", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// w320rbComments answers the script's comment text with the `#` prefixes and
// line breaks folded to single spaces, so a pinned sentence may wrap.
func w320rbComments(script string) string {
	var b strings.Builder
	for _, ln := range strings.Split(script, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "#") {
			b.WriteString(strings.TrimSpace(strings.TrimLeft(ln, "#")))
			b.WriteString(" ")
		}
	}
	return regexp.MustCompile(`\s+`).ReplaceAllString(b.String(), " ")
}

// TestW320ReleaseBoundaryComments pins the one-audit boundary text in the
// gate's own comment (and its header pointer).
func TestW320ReleaseBoundaryComments(t *testing.T) {
	c := w320rbComments(w320rbScript(t))
	for _, want := range []string{
		"one audit, one command - the release runs the scope engine's own dependency-audit gate, the same `audit` command the verification bar and main's CI run",
		"over the same trees: the Go module and every shipped npm tree, pi-web and its demo included - never the one hardcoded tree of the old gate",
		"unconditionally: the release runs no test gate (the green integrate batch's own full-gate run is the test evidence), so nothing can skip the audit - the advisory feed moves, a green batch says nothing about today's advisories",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("release.sh must state the audit boundary %q", want)
		}
	}
	for _, old := range []string{
		"Two audits, two scopes",
		"the release audits production dependencies only",
		"npm audit --omit=dev",
		"a reused green tag skips the test gate",
	} {
		if strings.Contains(c, old) {
			t.Fatalf("release.sh still carries the old audit boundary %q", old)
		}
	}
}

// TestW320ReleaseBoundaryRunsTheEngineAudit pins the gate's behaviour: the
// default audit command is the engine's own `audit` command, taken from its
// --json --full report and run verbatim, placed after the binaries build
// and before anything is published, and never skipped by the test gate's
// branches (RELEASE_TEST_CMD, a reused green tag).
func TestW320ReleaseBoundaryRunsTheEngineAudit(t *testing.T) {
	script := w320rbScript(t)
	if strings.Contains(script, "cd webui && npm audit") {
		t.Fatalf("release.sh must not hardcode a component audit: `cd webui && npm audit` is gone")
	}
	for i, ln := range strings.Split(script, "\n") {
		if tl := strings.TrimSpace(ln); strings.HasPrefix(tl, "#") {
			continue
		} else if strings.Contains(ln, "npm audit") {
			t.Fatalf("release.sh line %d runs npm audit itself - the audit command comes from the engine: %q", i+1, ln)
		}
	}
	const head = `echo "release: running dependency-audit gate"`
	idxAudit := strings.Index(script, head)
	idxBuild := strings.Index(script, "# build binaries: the full matrix")
	idxImage := strings.Index(script, "# build image (context:")
	if idxAudit < 0 || idxBuild < 0 || idxImage < 0 || !(idxBuild < idxAudit && idxAudit < idxImage) {
		t.Fatalf("the audit gate must sit between the binaries build and the image build (audit %d, build %d, image %d)", idxAudit, idxBuild, idxImage)
	}
	block := script[idxAudit:idxImage]
	for _, want := range []string{
		`python3 "$ENGINE" --json --full`,
		`== "audit"`,
		`sh -c "$AUDIT_CMD"`,
		`[ -n "${RELEASE_AUDIT_CMD:-}" ]`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("the audit gate must contain %s:\n%s", want, block)
		}
	}
	for _, banned := range []string{"GREEN", "RELEASE_TEST_CMD"} {
		if strings.Contains(block, banned) {
			t.Fatalf("the audit gate must be unconditional - %s must not reach it (a reused green tag skips the test gate, never the audit)", banned)
		}
	}
}
