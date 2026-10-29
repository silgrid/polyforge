package verifyrun

// preexisting.go: proving a failure pre-existing. A unit still failing at
// HEAD after the flaky policy is pre-existing only when ALL of these
// hold, fail closed on anything else:
//
//   - it is located (`<repo-relative test file>: <test name>`) and has a
//     targeted re-run, and it is not a protected test (an authored test,
//     the repro's test_files, the fix's regression test — those pin this
//     change and fail on the base by design);
//   - it EXISTS at the base (its file there declares it) — a test the
//     base does not have is this change's failure;
//   - every one of the BaseRuns runs of the same targeted command, in a
//     throwaway worktree of the base OUTSIDE the capsule (after the
//     conf's base_setup commands), reports that unit failing — a base run
//     that did not get as far as the unit's own failure is environmental,
//     not proof;
//   - with the same failure signature as at HEAD — every assertion line
//     the unit and its subtests printed, source positions and durations
//     normalized, message digits kept — both taken from the SAME targeted
//     re-run command (HEAD's own re-run against the base runs), so the
//     forms match. A base run whose failure shows no signature proves
//     nothing (environmental), it never "fails differently".
//
// The base runs run after every repo's bar, serially, while the budget
// allows.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// baseObservation is one base run of a unit, as classifyBase reads it.
type baseObservation struct {
	Exit      int
	Found     bool // the unit is reported in the run's output
	Failed    bool // reported failing
	Signature string
}

// classifyBase is the pre-existing judgment over the facts: the bar
// run's signature, the HEAD re-run's (they must agree), the existence
// proof and the base runs (want of them).
func classifyBase(barSig, headSig string, exists bool, existsWhy string, runs []baseObservation, want int) (bool, string) {
	if !exists {
		return false, "not pre-existing: the base does not have this test (" + existsWhy + ")"
	}
	if headSig == "" {
		return false, "not pre-existing: no targeted re-run at HEAD shows its failure with an assertion line to compare"
	}
	if barSig != headSig {
		// an order-dependent failure: in the suite it fails one way, alone
		// another — the isolated runs prove nothing about the bar's failure
		return false, fmt.Sprintf("not pre-existing: its failure in the bar (%q) differs from its isolated re-run at HEAD (%q)", barSig, headSig)
	}
	if len(runs) < want {
		return false, fmt.Sprintf("not pre-existing: only %d of %d base runs ran (budget or deadline)", len(runs), want)
	}
	for i, b := range runs {
		switch {
		case b.Failed && b.Signature == "":
			return false, fmt.Sprintf("not pre-existing: base run %d shows the failure without an assertion line — no proof (an environmental failure)", i+1)
		case b.Failed && b.Signature != headSig:
			return false, fmt.Sprintf("not pre-existing: base run %d fails differently (%q, HEAD %q)", i+1, b.Signature, headSig)
		case b.Failed:
		case b.Found && b.Exit == 0:
			return false, fmt.Sprintf("not pre-existing: it passes on the base (run %d)", i+1)
		default:
			return false, fmt.Sprintf("not pre-existing: base run %d did not reach the test's own failure (exit %d, an environmental failure)", i+1, b.Exit)
		}
	}
	return true, fmt.Sprintf("fails the same way on all %d base runs", want)
}

// preExistingPhase classifies rr's still-failing units against the base.
func (r *runner) preExistingPhase(ctx context.Context, rr *repoRun) {
	var cands []*unitRun
	for _, cr := range rr.runs {
		for _, u := range cr.units {
			if !u.failing() {
				continue
			}
			switch {
			case u.Protected:
				u.Note = joinNote(u.Note, "never pre-existing: it is a protected test that pins this change")
			case u.Kind == KindCommand || u.Kind == KindGoPkg || u.Kind == KindLibtest || u.File == "":
				u.Note = joinNote(u.Note, "never pre-existing: no test file and test name locate it")
			case u.Rerun == "":
			default:
				cands = append(cands, u)
			}
		}
	}
	if len(cands) == 0 {
		return
	}
	base := ""
	if rr.merge != nil {
		base = rr.merge.Base
	}
	markAll := func(why string) {
		for _, u := range cands {
			u.Note = joinNote(u.Note, why)
		}
	}
	if base == "" {
		markAll("not pre-existing: no base commit is known")
		return
	}
	if !r.budgetLeft() {
		markAll(fmt.Sprintf("not pre-existing: no base run started, verify-run's budget (%s) is spent", r.o.Budget))
		return
	}
	tmp, err := os.MkdirTemp("", "pf-verify-base-")
	if err != nil {
		markAll("not pre-existing: no throwaway worktree: " + err.Error())
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	add := r.exec(ctx, rr, execSpec{Argv: []string{"git", "worktree", "add", "--detach", tmp, base}, Dir: rr.dir, Cwd: rr.cwdRel, Timeout: gitStepTimeout})
	if add.Exit != 0 {
		markAll(fmt.Sprintf("not pre-existing: the base worktree could not be checked out (exit %d)", add.Exit))
		return
	}
	defer func() {
		// cleanup runs whatever happened (an interrupt), bounded by the
		// deadline's tail so the result is still written in time
		tctx, cancel := r.tailCtx()
		defer cancel()
		_, _ = gitQuiet(tctx, rr.dir, "worktree", "remove", "--force", tmp)
		_, _ = gitQuiet(tctx, rr.dir, "worktree", "prune")
	}()
	if rr.plan != nil && rr.plan.report != nil {
		for _, setup := range rr.plan.report.BaseSetup {
			res := r.exec(ctx, rr, execSpec{Argv: bashArgv(setup), Dir: tmp, Cwd: tmp, Timeout: r.o.CommandTimeout, PrivateTmp: true})
			if res.Exit != 0 {
				markAll(fmt.Sprintf("not pre-existing: the base_setup command %q failed on the base (exit %d) — an environmental failure", setup, res.Exit))
				return
			}
		}
	}
	for _, u := range cands {
		exists, why := r.existsAtBase(ctx, rr.dir, base, u)
		var runs []baseObservation
		if exists {
			for i := 0; i < r.o.BaseRuns && r.budgetLeft() && ctx.Err() == nil && r.fits(u.rerunDur); i++ {
				res := r.exec(ctx, rr, execSpec{Argv: bashArgv(u.Rerun), Dir: tmp, Cwd: tmp, Timeout: r.o.CommandTimeout, PrivateTmp: true})
				us := parseUnits(normalizeOutput(outputLines(res.LogPath), tmp, res.TmpDir))
				obs := baseObservation{Exit: res.Exit}
				if got := findUnit(us, u.Kind, u.Name); got != nil {
					obs.Found, obs.Failed, obs.Signature = true, got.Failed, got.Signature
				}
				runs = append(runs, obs)
			}
		}
		pre, detail := classifyBase(u.Signature, u.HeadSig, exists, why, runs, r.o.BaseRuns)
		if pre {
			u.PreExisting = true
			u.PreDetail = capText(fmt.Sprintf("fails on base %s too: %s, signature %q", base, detail, u.HeadSig))
			u.BaseSig = u.HeadSig
		} else {
			u.Note = joinNote(u.Note, detail)
		}
	}
}

// existsAtBase is the existence proof: the unit's file at the base
// declares it.
func (r *runner) existsAtBase(ctx context.Context, dir, base string, u *unitRun) (bool, string) {
	if _, err := gitQuiet(ctx, dir, "cat-file", "-e", base+":"+u.File); err != nil {
		return false, u.File + " does not exist at " + short(base)
	}
	var args []string
	switch u.Kind {
	case KindGo:
		args = []string{"grep", "-q", "-E", "^func " + regexp.QuoteMeta(u.Name) + `\(`, base, "--", u.File}
	case KindPytest, KindUnittest:
		fn := u.Name
		if i := strings.IndexByte(fn, '['); i >= 0 {
			fn = fn[:i] // a parametrized id's parameters may carry dots
		}
		if i := strings.LastIndexAny(fn, ":."); i >= 0 {
			fn = fn[i+1:]
		}
		args = []string{"grep", "-q", "-E", `def\s+` + regexp.QuoteMeta(fn) + `\(`, base, "--", u.File}
	case KindMjs:
		args = []string{"grep", "-q", "-F", "-e", u.Name, base, "--", u.File}
	default:
		return false, "its kind has no existence proof"
	}
	if _, err := gitQuiet(ctx, dir, args...); err != nil {
		return false, u.File + " at " + short(base) + " does not declare " + u.Name
	}
	return true, ""
}

// preExistingEntry is the pre_existing_failures entry of a unit.
func preExistingEntry(u *unitRun) string {
	name := u.Name
	if u.Kind == KindPytest {
		if _, rest, ok := strings.Cut(u.Name, "::"); ok {
			name = rest
		}
	}
	return u.File + ": " + name
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
