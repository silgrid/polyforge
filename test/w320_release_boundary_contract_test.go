package test

// w320_release_boundary_contract_test.go: `.polyforge/release.sh`'s audit
// boundary comments describe both audits — the verification bar's
// dependency-audit gate over the whole tree (dev dependencies included) and
// the release's production-only gate — and the release's audit behaviour is
// unchanged: it still runs npm audit over production dependencies at the high
// threshold. The old boundary wording ("the test bar never runs it") is red.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW320ReleaseBoundary' -count=1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func w320rbScript(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", ".polyforge", "release.sh"))
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

// TestW320ReleaseBoundaryComments pins the two-audit boundary text in both
// places (the release walkthrough and the gate's own comment).
func TestW320ReleaseBoundaryComments(t *testing.T) {
	script := w320rbScript(t)
	c := w320rbComments(script)
	for _, want := range []string{
		"Two audits, two scopes: the verification bar's dependency-audit gate audits the whole tree, dev dependencies included, on every full-tier run; the release audits production dependencies only;",
		"Two audits, two scopes: the verification bar's dependency-audit gate (workflows/_shared/scripts/audit-deps, every full-tier run) audits the whole tree, dev dependencies included; this release gate audits production dependencies only.",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("release.sh must state the audit boundary %q", want)
		}
	}
	for _, old := range []string{"the test bar never runs it", "The test bar never runs this; only a release does."} {
		if strings.Contains(c, old) {
			t.Fatalf("release.sh still carries the old audit boundary %q", old)
		}
	}
}

// TestW320ReleaseBoundaryBehaviourUnchanged: the release still audits
// production dependencies only, at high, and refuses on a hit.
func TestW320ReleaseBoundaryBehaviourUnchanged(t *testing.T) {
	script := w320rbScript(t)
	want := `(cd webui && npm audit --omit=dev --audit-level=high) || die "production-dependency audit failed`
	if strings.Count(script, want) != 1 {
		t.Fatalf("release.sh must keep exactly one production-only high audit refusal: %q", want)
	}
	for _, ln := range strings.Split(script, "\n") {
		if tl := strings.TrimSpace(ln); strings.HasPrefix(tl, "#") {
			continue
		}
		if strings.Contains(ln, "npm audit") && !strings.Contains(ln, "--omit=dev --audit-level=high") {
			t.Fatalf("every release npm audit stays production-only at high: %q", ln)
		}
	}
}
