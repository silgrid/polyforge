package test

// w320_baseline_coverage_test.go: every high/critical finding of this
// repository's recorded baseline readings has a valid row in the committed
// exemption ledger (.polyforge/audit-exemptions.conf). It is a subset check:
// a later owner-ruled row for a new advisory, or a row removed after its fix
// (with the recording then no longer carrying the finding), never turns it
// red, so the ledger's recovery path needs no edit of the recordings. The
// readings are the tools' own output, recorded under
// test/testdata/audit/baseline/:
//
//	npm-webui.json  — (cd webui && npm audit --json --audit-level=high --package-lock-only --include=dev),
//	                  recorded 2026-11-04
//	go-module.json  — GOWORK=off govulncheck -json ./... (govulncheck@v1.9.0, go1.26.11,
//	                  vuln.go.dev as of 2026-11-11T02:51Z, in the verify image of
//	                  .polyforge/Dockerfile.verify): the config event and the first
//	                  symbol-level (reachable) finding event per advisory and module,
//	                  each verbatim. Re-recorded 2026-11-11 when the toolchain pin moved
//	                  to go1.26.11: the 2026-11-04 reading (go1.26.4) also carried 16
//	                  standard-library findings, all fixed by that release, whose rows
//	                  were removed with it
//
// The judge is the gate helper itself (workflows/_shared/scripts/audit-deps), replaying the
// recordings through the stub tools, so the rows are judged by exactly the
// rules the bar applies. The test is date-independent: it replays a copy of
// the ledger whose every row keeps its own window length (from..expires, so
// the 90-day cap is still judged) re-anchored at today. Expiry is the live
// gate's business on the real tree, never this test's.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW320Baseline' -count=1

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w320bAnchored answers the ledger text with every well-formed row's window
// re-anchored at today (UTC), its length kept; any other line is copied as is,
// so a malformed row still reaches the helper as written.
func w320bAnchored(raw string) string {
	lines := strings.Split(raw, "\n")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i, ln := range lines {
		s := strings.TrimSpace(ln)
		if !strings.HasPrefix(s, "exempt=") {
			continue
		}
		f := strings.Split(strings.TrimPrefix(s, "exempt="), "|")
		if len(f) != 6 {
			continue
		}
		from, err1 := time.Parse("2006-01-02", strings.TrimSpace(f[4]))
		expires, err2 := time.Parse("2006-01-02", strings.TrimSpace(f[5]))
		if err1 != nil || err2 != nil {
			continue
		}
		f[4] = today.Format("2006-01-02")
		f[5] = today.Add(expires.Sub(from)).Format("2006-01-02")
		lines[i] = "exempt=" + strings.Join(f, "|")
	}
	return strings.Join(lines, "\n")
}

func TestW320BaselineCoverage(t *testing.T) {
	root := w320aRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "audit-exemptions.conf"))
	if err != nil {
		t.Fatalf("the repository commits its exemption ledger: %v", err)
	}
	ledger := filepath.Join(t.TempDir(), "audit-exemptions.conf")
	if err := os.WriteFile(ledger, []byte(w320bAnchored(string(raw))), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "webui"), 0o755); err != nil {
		t.Fatal(err)
	}
	errb, code, _ := w320aExec(t, dir, w320aRun{
		npmOut: w320aFixture(t, filepath.Join("baseline", "npm-webui.json")), npmRC: "1",
		govulnOut: w320aFixture(t, filepath.Join("baseline", "go-module.json")),
		args:      []string{"--go", "--npm", "webui", "--ledger", ledger},
	})
	// Every recorded finding is covered by a valid row (no finding:), and
	// every row is well formed (no ledger-invalid:); a row the recordings do
	// not need (stale:) is allowed.
	w320aWant(t, "baseline", errb, code, 0,
		[]string{"surface go:.: audited", "surface npm:webui: audited"},
		[]string{"finding:", "ledger-invalid:", "ledger-expired:"})
	findings := 0
	for _, m := range regexp.MustCompile(`audited, (\d+) finding\(s\)`).FindAllStringSubmatch(errb, -1) {
		n, _ := strconv.Atoi(m[1])
		findings += n
	}
	if findings == 0 {
		t.Fatalf("the recorded readings must carry the baseline findings:\n%s", errb)
	}
}
