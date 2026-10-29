package verifyrun

// result.go: the result.json shape and its assembly from the run state —
// the envelope-ready checks, pre_existing_failures, test_bar and
// command_evidence, plus the per-repo plans, the units and the authored
// scenarios the bindings resolve.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Result is result.json.
type Result struct {
	Schema int    `json:"schema"`
	Run    int    `json:"run"`
	OutDir string `json:"out_dir"`
	LogDir string `json:"log_dir"`
	// Incomplete, when set, is why no merged-tree proof could be made:
	// the step returns outcome incomplete with it (a human fixes the
	// machine and resumes). Nothing below is a verdict then.
	Incomplete string `json:"incomplete,omitempty"`
	// MechanicalVerdict is the verdict over the mechanical checks alone
	// (pass|fail); the step's own judgment checks still count.
	MechanicalVerdict string `json:"mechanical_verdict"`
	// FailReason names every failing mechanical check (a fail_reason
	// draft: the exact command, the new failures, the log).
	FailReason string `json:"fail_reason,omitempty"`
	// Deadline is the whole run's deadline it ran under.
	Deadline    string         `json:"deadline"`
	Tier        TierInfo       `json:"tier"`
	Checks      []Check        `json:"checks"`
	PreExisting []string       `json:"pre_existing_failures"`
	TestBar     []TestBarEntry `json:"test_bar"`
	// TestBarNotes name the bar commands that carry a pre-existing
	// failure but offer no skip switch (recorded as run).
	TestBarNotes    []string                 `json:"test_bar_notes,omitempty"`
	CommandEvidence []Evidence               `json:"command_evidence"`
	MergeOutcomes   map[string]*MergeOutcome `json:"merge_outcomes"`
	Repos           []RepoReport             `json:"repos"`
	// UnplannedRepos are touched repos the engine gave no plan for: the
	// step derives and runs their bar itself.
	UnplannedRepos []string         `json:"unplanned_repos"`
	Units          []UnitReport     `json:"units"`
	Authored       bool             `json:"authored"`
	AuthoredTitles []string         `json:"authored_titles"`
	BoundScenarios []ScenarioResult `json:"bound_scenarios"`
	UnboundTitles  []string         `json:"unbound_titles"`
	Scope          ScopeInfo        `json:"scope"`
	ConfChanged    map[string]bool  `json:"conf_changed_in_diff"`
}

// TierInfo is the tier asked for and the tier the run actually proved.
type TierInfo struct {
	Requested string `json:"requested"`
	// Effective is scoped only when the scoped tier was asked for and
	// every touched repo's engine report was scoped (at least one); any
	// engine failure, full report or unplanned repo makes it full.
	Effective string `json:"effective"`
}

// Check is one envelope check entry.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// TestBarEntry is one test_bar entry.
type TestBarEntry struct {
	Repo    string `json:"repo"`
	Command string `json:"command"`
}

// RepoReport is one repo's plan and its commands.
type RepoReport struct {
	Name       string   `json:"name"`
	Touched    bool     `json:"touched"`
	Plan       string   `json:"plan,omitempty"`
	PlanReason string   `json:"plan_reason,omitempty"`
	Stack      string   `json:"stack,omitempty"`
	Conf       string   `json:"conf,omitempty"`
	Mode       string   `json:"mode,omitempty"`
	EngineRef  string   `json:"engine_ref,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	// Diagnostics are the engine's own diagnostics for this repo.
	Diagnostics []string        `json:"diagnostics,omitempty"`
	Commands    []CommandReport `json:"commands"`
}

// CommandReport is one bar command's run.
type CommandReport struct {
	Check    string `json:"check"`
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Origin   string `json:"origin"`
	Command  string `json:"command"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out,omitempty"`
	Skipped  string `json:"skipped,omitempty"`
	// NotStarted: the run's deadline passed before the command could start.
	NotStarted string `json:"not_started,omitempty"`
	LogPath    string `json:"log_path,omitempty"`
}

// UnitReport is one located unit and its resolution.
type UnitReport struct {
	Repo      string `json:"repo"`
	Check     string `json:"check"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file,omitempty"`
	Status    string `json:"status"` // pass | fail | flaky | pre_existing
	Protected bool   `json:"protected,omitempty"`
	Marker    string `json:"marker,omitempty"`
	Signature string `json:"signature,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ScenarioResult is one authored scenario a binding resolves.
type ScenarioResult struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pass | fail | skipped
	Detail string `json:"detail"`
}

// ScopeInfo carries the scope facts the step's scope declaration uses.
type ScopeInfo struct {
	// FastGatesRef names the engine invocation and, per repo, whether a
	// repo conf applied (the scope object's fast_gates_ref).
	FastGatesRef string `json:"fast_gates_ref"`
	// Paths are the engine's changed paths per touched repo.
	Paths map[string][]string `json:"paths,omitempty"`
}

// assemble folds the run state into res.
func (r *runner) assemble(res *Result, repos []*repoRun) {
	ctx, cancel := r.tailCtx()
	defer cancel()
	for _, rr := range repos {
		res.CommandEvidence = append(res.CommandEvidence, rr.evidence...)
		if rr.merge != nil {
			res.MergeOutcomes[rr.name] = rr.merge
		}
	}
	res.Tier.Effective = TierFull
	if res.Incomplete != "" {
		res.UnplannedRepos = []string{}
		return
	}
	effectiveScoped := r.o.Tier == TierScoped
	scopedRepos := 0
	var refs []string
	paths := map[string][]string{}
	for _, rr := range repos {
		rep := RepoReport{Name: rr.name, Touched: rr.touched, Commands: []CommandReport{}}
		if !rr.touched {
			res.Checks = append(res.Checks, Check{Name: rr.name + ":untouched", Status: "skipped",
				Detail: fmt.Sprintf("no commit beyond origin/%s after the merge: nothing of this change lives here, so no bar runs in repos/%s", rr.merge.DefaultBranch, rr.name)})
		} else if p := rr.plan; p != nil {
			rep.Plan, rep.PlanReason, rep.Mode, rep.EngineRef = p.Status, p.Reason, p.Mode, p.EngineRef
			if p.report != nil {
				rep.Stack, rep.Conf, rep.Paths, rep.Diagnostics = p.report.Stack, p.report.Conf, p.report.Paths, p.report.Diagnostics
				res.ConfChanged[rr.name] = p.report.ConfChangedInDiff
				paths[rr.name] = p.report.Paths
				refs = append(refs, fmt.Sprintf("repos/%s: --base %s, conf %s, mode %s", rr.name, short(rr.merge.Base), p.report.Conf, p.report.Mode))
				if p.report.ConfChangedInDiff {
					res.Checks = append(res.Checks, Check{Name: rr.name + ":conf-changed", Status: "skipped",
						Detail: "the repo's .polyforge/verify-scope.conf changed in this diff: the plan was read from HEAD's conf — deliver and the owner see it"})
				}
				if len(p.report.Diagnostics) > 0 {
					res.Checks = append(res.Checks, Check{Name: rr.name + ":engine-diagnostics", Status: "skipped",
						Detail: capText("the scope engine reported: " + strings.Join(p.report.Diagnostics, " | "))})
				}
			} else {
				refs = append(refs, fmt.Sprintf("repos/%s: engine failed", rr.name))
			}
			if !p.planned() {
				res.UnplannedRepos = append(res.UnplannedRepos, rr.name)
				res.Checks = append(res.Checks, Check{Name: rr.name + ":plan", Status: "skipped",
					Detail: p.Reason + " — the step derives and runs this repo's build/lint/test bar itself"})
			}
			if !p.planned() || p.Mode != TierScoped {
				effectiveScoped = false
			} else {
				scopedRepos++
			}
		}
		for _, cr := range append(append([]*cmdRun(nil), rr.runs...), rr.authored...) {
			ck := commandCheck(cr)
			res.Checks = append(res.Checks, ck)
			rep.Commands = append(rep.Commands, CommandReport{Check: cr.check, Name: cr.name, Phase: cr.phase,
				Origin: cr.origin, Command: cr.cmd, Status: ck.Status, ExitCode: cr.exit, TimedOut: cr.timedOut,
				Skipped: cr.skipped, NotStarted: cr.notStarted, LogPath: r.relIf(cr.logPath)})
			for _, u := range cr.units {
				res.Units = append(res.Units, unitReport(rr.name, cr, u))
				if u.PreExisting {
					res.PreExisting = append(res.PreExisting, preExistingEntry(u))
				}
			}
		}
		res.Checks = append(res.Checks, r.testFileChecks(ctx, rr)...)
		res.Repos = append(res.Repos, rep)
	}
	res.Checks = append(res.Checks, r.testFilesPresent(repos)...)
	if res.UnplannedRepos == nil {
		res.UnplannedRepos = []string{}
	}
	if effectiveScoped && scopedRepos > 0 {
		res.Tier.Effective = TierScoped
	}
	res.Scope = ScopeInfo{Paths: paths}
	if len(refs) > 0 {
		res.Scope.FastGatesRef = r.o.Engine + " --json (" + strings.Join(refs, "; ") + ")"
	}
	res.TestBar, res.TestBarNotes = deriveTestBar(repos)
	r.resolveScenarios(res, repos)
	res.MechanicalVerdict = "pass"
	var fails []string
	for _, c := range res.Checks {
		if c.Status == "fail" {
			res.MechanicalVerdict = "fail"
			fails = append(fails, c.Name+": "+c.Detail)
		}
	}
	res.FailReason = strings.Join(fails, "\n")
}

// relIf is rel for a non-empty path.
func (r *runner) relIf(p string) string {
	if p == "" {
		return ""
	}
	return r.rel(p)
}

// commandCheck is one bar command's check entry.
func commandCheck(cr *cmdRun) Check {
	c := Check{Name: cr.check}
	if cr.skipped != "" {
		c.Status, c.Detail = "skipped", fmt.Sprintf("`%s` not run: %s", cr.cmd, cr.skipped)
		return c
	}
	if cr.notStarted != "" {
		// the bar was not run in full: never a pass
		c.Status, c.Detail = "fail", fmt.Sprintf("`%s` %s", cr.cmd, cr.notStarted)
		return c
	}
	log := filepath.Base(filepath.Dir(cr.logPath)) + "/" + filepath.Base(cr.logPath)
	if cr.exit == 0 {
		c.Status, c.Detail = "pass", fmt.Sprintf("`%s` exit 0 (log %s)", cr.cmd, log)
		return c
	}
	var newFails, pre, flaky []string
	for _, u := range cr.units {
		switch {
		case u.Flaky != "":
			flaky = append(flaky, fmt.Sprintf("%s (%s)", u.Name, u.Flaky))
		case u.PreExisting:
			pre = append(pre, preExistingEntry(u)+" — "+u.PreDetail)
		case u.Failed:
			s := u.Name
			if u.Signature != "" {
				s += ": " + u.Signature
			}
			if u.Note != "" {
				s += " [" + u.Note + "]"
			}
			newFails = append(newFails, s)
		}
	}
	head := fmt.Sprintf("`%s` exit %d", cr.cmd, cr.exit)
	switch {
	case cr.deadlineHit:
		head += " (killed at verify-run's deadline)"
	case cr.timedOut:
		head += " (killed at its timeout)"
	}
	var parts []string
	switch {
	case len(newFails) > 0 || cr.timedOut:
		c.Status = "fail"
		if len(newFails) > 0 {
			parts = append(parts, "new failures: "+strings.Join(newFails, "; "))
		}
	case len(pre) > 0:
		c.Status = "pre_existing"
	default:
		c.Status = "pass"
	}
	if len(pre) > 0 {
		parts = append(parts, "pre-existing: "+strings.Join(pre, "; "))
	}
	if len(flaky) > 0 {
		parts = append(parts, "passed on re-run: "+strings.Join(flaky, "; "))
	}
	if cr.confirmed != "" {
		parts = append(parts, fmt.Sprintf("re-run whole as `%s`: exit 0", cr.confirmed))
	}
	if cr.completion != "" {
		parts = append(parts, "complete: "+cr.completion)
	}
	c.Detail = capText(head + "; " + strings.Join(parts, "; ") + " (log " + log + ")")
	return c
}

// unitReport is one unit's report entry.
func unitReport(repo string, cr *cmdRun, u *unitRun) UnitReport {
	ur := UnitReport{Repo: repo, Check: cr.check, Kind: u.Kind, Name: u.Name, File: u.File,
		Protected: u.Protected, Marker: u.Marker, Signature: u.Signature}
	switch {
	case u.Flaky != "":
		ur.Status, ur.Detail = "flaky", u.Flaky+": passed on a re-run at HEAD"
	case u.PreExisting:
		ur.Status, ur.Detail = "pre_existing", u.PreDetail
	case u.Failed:
		ur.Status, ur.Detail = "fail", u.Note
	default:
		ur.Status = "pass"
	}
	ur.Detail = capText(ur.Detail)
	ur.Marker = capText(ur.Marker)
	return ur
}

// testFileChecks is rr's no-untracked-test-files check (before and after
// the bar, plus the declared test_files tracked where they exist) and the
// authored-tests-unchanged check.
func (r *runner) testFileChecks(ctx context.Context, rr *repoRun) []Check {
	var out []Check
	var problems []string
	for _, s := range []struct {
		when string
		scan untrackedScan
	}{{"before the bar", rr.untrackedBefore}, {"after the bar", rr.untrackedAfter}} {
		if s.scan.Err != "" {
			problems = append(problems, "git status failed "+s.when+": "+s.scan.Err)
		}
		if len(s.scan.Files) > 0 {
			problems = append(problems, "untracked test files "+s.when+": "+strings.Join(s.scan.Files, ", "))
		}
	}
	var tracked []string
	for _, tf := range r.facts.TestFiles {
		if _, err := os.Stat(filepath.Join(rr.dir, filepath.FromSlash(tf.Path))); err != nil {
			continue
		}
		if _, err := gitQuiet(ctx, rr.dir, "ls-files", "--error-unmatch", "--", tf.Path); err != nil {
			problems = append(problems, fmt.Sprintf("%s's test file %s is not tracked", tf.Step, tf.Path))
			continue
		}
		tracked = append(tracked, tf.Path)
	}
	c := Check{Name: rr.name + ":no-untracked-test-files", Status: "pass"}
	if len(problems) > 0 {
		c.Status, c.Detail = "fail", strings.Join(problems, "; ")
	} else {
		c.Detail = "git status --porcelain --untracked-files=all lists no untracked test file before or after the bar"
		if len(tracked) > 0 {
			c.Detail += "; git ls-files --error-unmatch holds for " + strings.Join(tracked, ", ")
		}
	}
	out = append(out, c)
	if stamp, ok := r.facts.AuthoredStamp[rr.name]; ok {
		var files []string
		for _, tf := range r.facts.TestFiles {
			if tf.Step == e2eAuthorStep {
				files = append(files, tf.Path)
			}
		}
		if len(files) > 0 {
			args := append([]string{"log", "--first-parent", "--no-merges", "--format=%h", stamp + "..HEAD", "--"}, files...)
			c := Check{Name: rr.name + ":authored-tests-unchanged"}
			switch outp, err := gitQuiet(ctx, rr.dir, args...); {
			case err != nil:
				c.Status, c.Detail = "fail", fmt.Sprintf("the authored tests' history since %s is unreadable: %v", short(stamp), err)
			case strings.TrimSpace(outp) != "":
				c.Status, c.Detail = "fail", fmt.Sprintf("the authored test files changed since their commit %s in %s (the driver's authored-tests gate rejects this too)", short(stamp), strings.Join(strings.Fields(outp), " "))
			default:
				c.Status, c.Detail = "pass", fmt.Sprintf("no commit of this branch touched the authored test files since %s", short(stamp))
			}
			out = append(out, c)
		}
	}
	return out
}

// testFilesPresent fails a declared test_files path no task repo has.
func (r *runner) testFilesPresent(repos []*repoRun) []Check {
	if len(r.facts.TestFiles) == 0 {
		return nil
	}
	var missing []string
	for _, tf := range r.facts.TestFiles {
		found := false
		for _, rr := range repos {
			if _, err := os.Stat(filepath.Join(rr.dir, filepath.FromSlash(tf.Path))); err == nil {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, tf.Step+": "+tf.Path)
		}
	}
	if len(missing) > 0 {
		return []Check{{Name: "test-files-present", Status: "fail", Detail: "declared test_files exist in no task repo: " + strings.Join(missing, ", ")}}
	}
	return []Check{{Name: "test-files-present", Status: "pass", Detail: fmt.Sprintf("every declared test_files path (%d) exists in a task repo", len(r.facts.TestFiles))}}
}
