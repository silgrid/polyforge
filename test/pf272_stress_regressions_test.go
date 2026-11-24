// carries: test/stress-regressions.sh, test/pf272_stress_regressions_test.go
package test

// pf272_stress_regressions_test.go (polyforge#272 U3, AC2): the stress
// script's phase verdicts. test/stress-regressions.sh runs each item's
// rounds J at a time, each round a fresh process in its own HOME, TMPDIR
// and worker directory; `before` stops dispatching an item once a round
// shows the item's target signature (and is red when no round does),
// `after` runs exactly N rounds and is green only when every round printed
// the item's pass line with no target signature (and, unless the item
// counts its lines only, exited 0). These scenarios feed it sh-only items
// through --items-file, so every expected verdict is computed by hand from
// the item's definition.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestPF272StressRegressionsVerdicts$' -count=1 -v

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pf272Items writes the fake items' TSV (name, prepare, cmd, pass regexp,
// target-signature regexp, optional count_lines_only) and answers its path.
// Every round records `<start> <end> <worker> <workdir> <HOME> <TMPDIR>` in
// ev/<item>-<round>, written before the round ends.
//
//	flaky    round 3 prints the target signature and exits 1; every other
//	         round sleeps, then prints its pass line (round 3 ends first)
//	steady   sleeps 1 s, then prints its pass line
//	other    exits 1 printing an unrelated failure (not the signature)
//	nomarker exits 0 without a pass line
//	suite    count_lines_only: prints the target's pass line beside another
//	         scenario's failure and exits 1
func pf272Items(t *testing.T, ev string) string {
	t.Helper()
	body := func(item, work string) string {
		return `s=$(date +%s%N); ` + work + `; printf '%s %s %s %s %s %s\n' "$s" "$(date +%s%N)" "$STRESS_WORKER" "$STRESS_WORKDIR" "$HOME" "$TMPDIR" > ` + ev + `/` + item + `-$STRESS_ROUND`
	}
	rows := [][]string{
		{"flaky", "true", `if [ "$STRESS_ROUND" = 3 ]; then ` + body("flaky", "true") + `; echo 'TARGET-SIGNATURE: the race was lost'; exit 1; fi; ` + body("flaky", "sleep 0.6") + `; echo 'PASS flaky-ok'`, `^PASS flaky-ok$`, `TARGET-SIGNATURE: the race`},
		{"steady", "true", body("steady", "sleep 1") + `; echo 'PASS steady-ok'`, `^PASS steady-ok$`, `TARGET-SIGNATURE: the race`},
		{"other", "true", body("other", "true") + `; echo 'some unrelated failure'; exit 1`, `^PASS other-ok$`, `TARGET-SIGNATURE: the race`},
		{"nomarker", "true", body("nomarker", "true") + `; echo 'all good but no marker'; exit 0`, `^PASS nomarker-ok$`, `TARGET-SIGNATURE: the race`},
		{"suite", "true", body("suite", "true") + `; echo 'PASS S12 (1s)'; echo 'FAIL S99 another scenario broke'; exit 1`, `^PASS S12 \(`, `FAIL S12 the one-shot login terminal attaches`, "count_lines_only"},
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t") + "\n")
	}
	p := filepath.Join(t.TempDir(), "items.tsv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// pf272Run is one script run's outcome.
type pf272Run struct {
	exit     int
	out      string // the run's output directory
	ev       string // the items' own round records
	stdout   string
	summary  string
	manifest [][]string // data rows (the header dropped)
}

// pf272Rec is one round's own record.
type pf272Rec struct {
	round              int
	start, end         int64
	worker             string
	workdir, home, tmp string
}

func (r pf272Run) records(t *testing.T, item string) []pf272Rec {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(r.ev, item+"-*"))
	var recs []pf272Rec
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		f := strings.Fields(string(b))
		if len(f) != 6 {
			t.Fatalf("%s: a round record is 6 fields, got %q", m, b)
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(m), item+"-"))
		s, _ := strconv.ParseInt(f[0], 10, 64)
		e, _ := strconv.ParseInt(f[1], 10, 64)
		recs = append(recs, pf272Rec{round: n, start: s, end: e, worker: f[2], workdir: f[3], home: f[4], tmp: f[5]})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].round < recs[j].round })
	return recs
}

// pf272Script runs test/stress-regressions.sh with the fake items.
func pf272Script(t *testing.T, phase string, rounds, jobs int, items ...string) pf272Run {
	t.Helper()
	root := w25e2eRoot(t)
	tmp := t.TempDir()
	ev := filepath.Join(tmp, "ev")
	if err := os.MkdirAll(ev, 0o755); err != nil {
		t.Fatal(err)
	}
	r := pf272Run{out: filepath.Join(tmp, "out"), ev: ev}
	args := []string{filepath.Join(root, "test", "stress-regressions.sh"),
		"--repo", root, "--phase", phase, "--rounds", strconv.Itoa(rounds), "--jobs", strconv.Itoa(jobs),
		"--out", r.out, "--items-file", pf272Items(t, ev), "--round-timeout", "60"}
	for _, it := range items {
		args = append(args, "--item", it)
	}
	cmd := exec.Command("bash", args...)
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	r.stdout = string(b)
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("bash %v: %v", args, err)
		}
		r.exit = ee.ExitCode()
	}
	if s, err := os.ReadFile(filepath.Join(r.out, "summary.txt")); err == nil {
		r.summary = string(s)
	}
	if m, err := os.ReadFile(filepath.Join(r.out, "manifest.tsv")); err == nil {
		for i, ln := range strings.Split(strings.TrimRight(string(m), "\n"), "\n") {
			if i == 0 || ln == "" {
				continue
			}
			r.manifest = append(r.manifest, strings.Split(ln, "\t"))
		}
	}
	return r
}

// pf272Col is the manifest's column index by its header name.
var pf272Col = map[string]int{"phase": 0, "item": 1, "round": 2, "worker": 3, "start": 4, "end": 5, "exit": 6, "pass": 7, "signature": 8, "other": 9, "loadavg": 10, "log": 11}

// pf272FinalOK reports whether the run's summary ends with the phase's ok
// marker, and whether its stdout printed it anywhere.
func (r pf272Run) finalOK(phase string) (summaryEnds, printed bool) {
	marker := "stress-regressions: " + phase + " ok"
	lines := strings.Split(strings.TrimRight(r.summary, "\n"), "\n")
	return lines[len(lines)-1] == marker, strings.Contains(r.stdout, marker)
}

func (r pf272Run) String() string {
	return fmt.Sprintf("exit %d\n--- stdout\n%s\n--- summary\n%s", r.exit, r.stdout, r.summary)
}

// TestPF272StressRegressionsVerdicts: before stops at the first round that
// shows the target signature and counts no other failure as a
// reproduction; after runs every round, J at a time in distinct workers,
// and is green only when every round passed — a failed round or one with no
// pass line is never offset; a count_lines_only item is judged by its target
// lines alone; every round has its own HOME and TMPDIR under the output.
func TestPF272StressRegressionsVerdicts(t *testing.T) {
	// before, flaky: reproduced at round 3, no new round after the hit.
	r := pf272Script(t, "before", 5, 2, "flaky")
	if r.exit != 0 {
		t.Fatalf("before only flaky: want exit 0, got %d\n%s", r.exit, r)
	}
	if !strings.Contains(r.summary, "flaky reproduced round 3") {
		t.Fatalf("before only flaky: summary.txt must record `flaky reproduced round 3`\n%s", r)
	}
	if n := len(r.records(t, "flaky")); n > 4 {
		t.Fatalf("before only flaky: a hit at round 3 with --jobs 2 starts at most 4 rounds, %d started\n%s", n, r)
	}
	if ends, printed := r.finalOK("before"); !ends || !printed {
		t.Fatalf("before only flaky: a reproduced phase ends with `stress-regressions: before ok`\n%s", r)
	}

	// before, steady and other: neither reproduces (an unrelated failure is
	// no reproduction).
	for _, item := range []string{"steady", "other"} {
		r := pf272Script(t, "before", 5, 2, item)
		if r.exit != 1 {
			t.Fatalf("before only %s: want exit 1 (not reproduced), got %d\n%s", item, r.exit, r)
		}
		if !strings.Contains(r.summary, item+" not reproduced") {
			t.Fatalf("before only %s: summary.txt must record `%s not reproduced`\n%s", item, item, r)
		}
		if _, printed := r.finalOK("before"); printed || strings.Contains(r.summary, "stress-regressions: before ok") {
			t.Fatalf("before only %s: the ok marker must not appear on a red phase\n%s", item, r)
		}
		if n := len(r.records(t, item)); n != 5 {
			t.Fatalf("before only %s: an item that never hits runs all 5 rounds, %d ran\n%s", item, n, r)
		}
	}

	// after, steady: exactly 6 rounds, all pass, 3 at a time, one workdir per
	// worker, a HOME and TMPDIR of its own per round under the output.
	r = pf272Script(t, "after", 6, 3, "steady")
	if r.exit != 0 {
		t.Fatalf("after only steady: want exit 0, got %d\n%s", r.exit, r)
	}
	if ends, printed := r.finalOK("after"); !ends || !printed {
		t.Fatalf("after only steady: a green phase ends with `stress-regressions: after ok`\n%s", r)
	}
	if len(r.manifest) != 6 {
		t.Fatalf("after only steady: manifest.tsv must hold 6 rounds, got %d\n%v", len(r.manifest), r.manifest)
	}
	for _, row := range r.manifest {
		if len(row) != len(pf272Col) || row[pf272Col["phase"]] != "after" || row[pf272Col["item"]] != "steady" ||
			row[pf272Col["exit"]] != "0" || row[pf272Col["pass"]] != "1" || row[pf272Col["signature"]] != "0" {
			t.Fatalf("after only steady: every manifest row is a passing steady round: %q", row)
		}
		if _, err := os.Stat(row[pf272Col["log"]]); err != nil {
			t.Fatalf("after only steady: the row's log must exist: %v", err)
		}
	}
	recs := r.records(t, "steady")
	if len(recs) != 6 {
		t.Fatalf("after only steady: 6 rounds must run, %d did", len(recs))
	}
	maxLive := 0
	for _, a := range recs {
		live := 0
		for _, b := range recs {
			if b.start <= a.start && a.start < b.end {
				live++
			}
		}
		maxLive = max(maxLive, live)
	}
	if maxLive != 3 {
		t.Fatalf("after only steady: --jobs 3 runs exactly 3 rounds at once at its peak, saw %d\n%+v", maxLive, recs)
	}
	byWorker, byDir := map[string]string{}, map[string]string{}
	homes, tmps := map[string]bool{}, map[string]bool{}
	for _, rec := range recs {
		if d, ok := byWorker[rec.worker]; ok && d != rec.workdir {
			t.Fatalf("after only steady: worker %s used two workdirs: %s and %s", rec.worker, d, rec.workdir)
		}
		if w, ok := byDir[rec.workdir]; ok && w != rec.worker {
			t.Fatalf("after only steady: workers %s and %s share workdir %s", w, rec.worker, rec.workdir)
		}
		byWorker[rec.worker], byDir[rec.workdir] = rec.workdir, rec.worker
		for _, p := range []string{rec.home, rec.tmp} {
			if !strings.HasPrefix(p, r.out+string(filepath.Separator)) {
				t.Fatalf("after only steady: round %d's HOME/TMPDIR %s must be under the output %s", rec.round, p, r.out)
			}
		}
		if homes[rec.home] || tmps[rec.tmp] || rec.home == rec.tmp {
			t.Fatalf("after only steady: round %d reuses a HOME or TMPDIR: %+v", rec.round, rec)
		}
		homes[rec.home], tmps[rec.tmp] = true, true
	}
	if len(byWorker) != 3 {
		t.Fatalf("after only steady: --jobs 3 uses 3 workers, saw %d: %v", len(byWorker), byWorker)
	}

	// after, flaky and nomarker: one failed round, or rounds with no pass
	// line, make the phase red — nothing offsets them.
	for _, item := range []string{"flaky", "nomarker"} {
		r := pf272Script(t, "after", 6, 3, item)
		if r.exit != 1 {
			t.Fatalf("after only %s: want exit 1, got %d\n%s", item, r.exit, r)
		}
		if n := len(r.manifest); n != 6 {
			t.Fatalf("after only %s: after runs exactly 6 rounds, the manifest holds %d", item, n)
		}
		if _, printed := r.finalOK("after"); printed || strings.Contains(r.summary, "stress-regressions: after ok") {
			t.Fatalf("after only %s: the ok marker must not appear on a red phase\n%s", item, r)
		}
	}

	// after, suite (count_lines_only): another scenario's failure and the
	// suite's exit code are recorded, not judged.
	r = pf272Script(t, "after", 3, 2, "suite")
	if r.exit != 0 {
		t.Fatalf("after only suite: only the target's lines count, want exit 0, got %d\n%s", r.exit, r)
	}
	if ends, _ := r.finalOK("after"); !ends {
		t.Fatalf("after only suite: a green phase ends with `stress-regressions: after ok`\n%s", r)
	}
	if len(r.manifest) != 3 {
		t.Fatalf("after only suite: manifest.tsv must hold 3 rounds, got %d", len(r.manifest))
	}
	for _, row := range r.manifest {
		if len(row) != len(pf272Col) || row[pf272Col["exit"]] != "1" || row[pf272Col["pass"]] != "1" || !strings.Contains(row[pf272Col["other"]], "fail_lines=1") {
			t.Fatalf("after only suite: each row records exit 1, the pass line and the other scenario's failure in the other column: %q", row)
		}
	}
}
