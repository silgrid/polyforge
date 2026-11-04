package verifyrun

// testbar.go: deriving the test_bar — every planned (and authored)
// command that ran, in run order, repo = the bare repos/ child name, the
// command exactly as run (no leading cd) — with a pre-existing failure
// excluded in the command's own skip form: Go's `-skip '^(A|B)$'`, the
// repo's declared Go harness's `E2E_SKIP='^(A|B)$'` (a command that runs
// its conf's `go_harness=` path directly, `sh|bash <path>`), pytest's
// `--deselect <node id>`. A command without a skip switch keeps its run
// form and is named in the notes (a deliver re-run of it exits
// non-zero, so a moved default branch goes to a human). Fetch, merge,
// engine, re-run and base-run commands are auxiliary: never in the bar.
//
// One exception to "as run": an opted-in repository (smoke_suite=) whose
// full-tier plan ran an e2e selection (carries|smoke) carries the engine's
// full tail (e2e_selection.full_commands) in place of its full-phase
// commands — delivery re-runs the full bar, never the subset — and a note
// says so. A scoped plan, a non-opted repo and a selection that already
// fell back to the full tail keep the commands as run.

import (
	"fmt"
	"strings"
)

// deriveTestBar answers the bar and its notes.
func deriveTestBar(repos []*repoRun) ([]TestBarEntry, []string) {
	bar := []TestBarEntry{}
	var notes []string
	for _, rr := range repos {
		sel := carriedSelection(rr)
		seen := map[string]bool{}
		add := func(cmd string) {
			seen[cmd] = true
			bar = append(bar, TestBarEntry{Repo: rr.name, Command: cmd})
		}
		for _, cr := range rr.runs {
			if sel != nil && cr.phase == TierFull {
				continue // the selection ran; the full tail is carried below
			}
			if cr.skipped != "" || cr.notStarted != "" {
				continue
			}
			cmd, note := skipForm(cr)
			add(cmd)
			if note != "" {
				notes = append(notes, note)
			}
		}
		if sel != nil {
			for _, c := range sel.FullCommands {
				if !seen[c] { // a full command that already ran is not repeated
					add(c)
				}
			}
			notes = append(notes, fmt.Sprintf("repos/%s ran the full-tier e2e selection (mode %s: %s); its full e2e command(s) %s are carried for delivery to re-run, not run here",
				rr.name, sel.Mode, strings.Join(sel.Suites, " "), strings.Join(sel.FullCommands, "; ")))
		}
		for _, cr := range rr.authored {
			if cr.skipped != "" || cr.notStarted != "" {
				continue
			}
			cmd, note := skipForm(cr)
			add(cmd)
			if note != "" {
				notes = append(notes, note)
			}
		}
	}
	return bar, notes
}

// carriedSelection answers rr's engine selection when its full-tier plan
// ran a subset of the full tail (nil otherwise).
func carriedSelection(rr *repoRun) *EngineSelection {
	p := rr.plan
	if p == nil || !p.planned() || p.Mode != TierFull || p.report == nil || !p.report.E2ESelection.subset() {
		return nil
	}
	return p.report.E2ESelection
}

// skipForm answers cr's bar form: the command with its pre-existing
// units excluded, or the run form plus a note when it cannot exclude them.
func skipForm(cr *cmdRun) (string, string) {
	var pre []*unitRun
	for _, u := range cr.units {
		if u.PreExisting {
			pre = append(pre, u)
		}
	}
	if len(pre) == 0 {
		return cr.cmd, ""
	}
	words, simple := shellWords(cr.cmd)
	kind := pre[0].Kind
	for _, u := range pre {
		if u.Kind != kind {
			kind = ""
		}
	}
	names := make([]string, len(pre))
	for i, u := range pre {
		names[i] = u.Name
	}
	switch {
	case kind == KindGo && simple && hasGoTest(words) && !hasFlag(words, "-skip"):
		return cr.cmd + " -skip '^(" + strings.Join(names, "|") + ")$'", ""
	case kind == KindGo && simple && cr.harness && !hasAssignment(cr.cmd, "E2E_SKIP"):
		// the e2e harness protocol: E2E_SKIP excludes top-level tests (an
		// E2E_RUN already there still selects; the two compose)
		return "E2E_SKIP='^(" + strings.Join(names, "|") + ")$' " + cr.cmd, ""
	case kind == KindPytest && simple && tokenIndex(words, "pytest") >= 0:
		cmd := cr.cmd
		for _, n := range names {
			cmd += " --deselect " + shellQuote(n)
		}
		return cmd, ""
	}
	return cr.cmd, fmt.Sprintf("repos/%s `%s` carries the pre-existing failure(s) %s but has no skip switch verify-run can apply: it is listed as run, so a deliver re-run of it exits non-zero and a moved default branch routes the delivery to a human",
		cr.repo, cr.cmd, strings.Join(names, ", "))
}

// hasFlag reports whether a Go flag is already given (-f, --f, -f=v).
func hasFlag(words []string, flag string) bool {
	bare := strings.TrimLeft(flag, "-")
	for _, w := range words {
		t := strings.TrimLeft(w, "-")
		if strings.HasPrefix(w, "-") && (t == bare || strings.HasPrefix(t, bare+"=")) {
			return true
		}
	}
	return false
}

// hasAssignment reports whether a command's leading assignments set name.
func hasAssignment(cmd, name string) bool {
	for _, a := range leadingAssignments(cmd) {
		if strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}
