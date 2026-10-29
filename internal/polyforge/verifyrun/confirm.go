package verifyrun

// confirm.go: a failed command is only explained when ALL of its
// non-zero exit is. Its located failures may all turn out flaky or
// pre-existing while something no parser located (a lint step after the
// tests, a crash after the last marker, a second program the command
// runs) failed too. So a command whose every located failure was
// explained away is run once more as a whole — in its test-bar form when
// a pre-existing failure was excluded, which also proves the bar deliver
// re-runs — and only an exit 0 there clears it. A command that cannot
// exclude its pre-existing failure (no skip switch), a budget or deadline
// too spent to try, or any non-zero exit leaves a residual whole-command
// failure (fail closed): the rest of that command was never shown to pass.

import (
	"context"
	"fmt"
	"path/filepath"
)

// confirmPhase confirms rr's explained-away commands.
func (r *runner) confirmPhase(ctx context.Context, rr *repoRun) {
	for _, cr := range append(append([]*cmdRun(nil), rr.runs...), rr.authored...) {
		if cr.skipped != "" || cr.notStarted != "" || cr.exit == 0 || cr.timedOut || ctx.Err() != nil {
			continue
		}
		explained, pre := true, false
		for _, u := range cr.units {
			switch {
			case u.PreExisting:
				pre = true
			case u.failing():
				explained = false
			}
		}
		if !explained {
			continue // the check fails on its own failures already
		}
		residual := &unitRun{unit: &unit{Kind: KindCommand, Name: cr.check, Failed: true, Marker: fmt.Sprintf("exit %d", cr.exit)}, Runs: 1}
		cmd := cr.cmd
		if pre {
			form, note := skipForm(cr)
			if note != "" {
				residual.Note = "the command has no skip switch for its pre-existing failure(s), so the rest of it cannot be shown to pass: it stays failed — split the bar so each command can exclude its own failures"
				cr.units = append(cr.units, residual)
				continue
			}
			cmd = form
		}
		// only after the skip-form rule: the declared harness's own
		// trailer may prove the suite complete, standing in for the
		// confirming re-run
		if proof := completionProof(cr); proof != "" {
			cr.completion = proof
			continue
		}
		switch {
		case !r.budgetLeft():
			residual.Note = fmt.Sprintf("not confirmed: verify-run's budget (%s) was spent before `%s` could be re-run whole to show nothing else failed", r.o.Budget, cmd)
		case !r.fits(cr.dur):
			residual.Note = fmt.Sprintf("not confirmed: %s leaves less than the command's first-run time (%s) to re-run `%s` whole", r.deadlineNote(), cr.dur, cmd)
		}
		if residual.Note != "" {
			cr.units = append(cr.units, residual)
			continue
		}
		res := r.exec(ctx, rr, execSpec{Argv: bashArgv(cmd), Dir: rr.dir, Cwd: rr.cwdRel, Timeout: cr.timeout, PrivateTmp: true})
		if res.Exit == 0 {
			cr.confirmed = cmd
			continue
		}
		if res.NotStarted {
			residual.Note = fmt.Sprintf("not confirmed: %s had passed before `%s` could be re-run whole", r.deadlineNote(), cmd)
			cr.units = append(cr.units, residual)
			continue
		}
		if res.DeadlineHit {
			residual.Note = fmt.Sprintf("not confirmed: `%s` was killed at %s while being re-run whole", cmd, r.deadlineNote())
			cr.units = append(cr.units, residual)
			continue
		}
		residual.Marker = fmt.Sprintf("exit %d", res.Exit)
		residual.Note = fmt.Sprintf("after its located failures were explained (flaky or pre-existing), `%s` still exits %d: a failure no unit locates (log %s)",
			cmd, res.Exit, filepath.Base(filepath.Dir(res.LogPath))+"/"+filepath.Base(res.LogPath))
		cr.units = append(cr.units, residual)
	}
}
