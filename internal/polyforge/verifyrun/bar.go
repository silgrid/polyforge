package verifyrun

// bar.go: running one repo's bar — every planned command in order, the
// whole list whatever fails (red is data: the plan never stops at the
// first failure), each verbatim through `bash -c` (never a -json or -v
// injected), a command whose declared requires_env / requires_cmd is
// unmet recorded skipped with the missing name (never inferred from its
// output) — then the failing units' flaky re-runs at HEAD, serially, and
// the authored binding commands the plan did not already run.

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// The command origins.
const (
	originPlan     = "plan"
	originAuthored = "authored"
)

// cmdRun is one bar command's run.
type cmdRun struct {
	repo    string
	check   string // "<repo>:<name>"
	name    string
	phase   string
	cmd     string
	origin  string
	timeout time.Duration
	skipped string // the unmet precondition ("" = it ran)

	exit        int
	timedOut    bool
	deadlineHit bool          // killed at the run's deadline
	notStarted  string        // the run's deadline passed before it could start
	dur         time.Duration // its first run's wall time
	logPath     string
	units       []*unitRun
	// confirmed is the command that, re-run whole after every located
	// failure was explained, exited 0 ("" = no such re-run).
	confirmed string
	// completion is how the declared harness's own trailer proved the
	// suite complete, so its explained failures needed no confirming
	// re-run ("" = no proof).
	completion string
	// harness: it runs the repo's declared Go harness directly
	// (`sh|bash <go_harness>`): E2E_SKIP is its skip form and its trailer
	// its completion proof.
	harness bool
}

// unitRun is one parsed unit of a command and its resolution.
type unitRun struct {
	*unit
	File      string // repo-relative test file, when located
	Protected bool   // pins this change: never pre-existing
	Rerun     string // the targeted re-run command ("" = not re-runnable)

	Runs     int    // runs at HEAD (the original included)
	Flaky    string // "flaky n/m" (passes/runs) when a re-run passed
	PassLogs []string
	Note     string // why a failure stays a failure
	// HeadSig is the failure signature of the unit's own targeted re-run
	// at HEAD — the same command form the base runs use, so the two
	// compare like with like ("" = no re-run showed its failure).
	HeadSig  string
	rerunDur time.Duration // the last targeted re-run's wall time

	PreExisting bool
	PreDetail   string
	BaseSig     string
}

// failing reports a unit still failing after the flaky policy.
func (u *unitRun) failing() bool { return u.Failed && u.Flaky == "" }

// runBar runs rr's plan, its flaky re-runs and its authored commands.
func (r *runner) runBar(ctx context.Context, rr *repoRun) {
	if rr.merge == nil {
		return
	}
	rr.untrackedBefore = r.untrackedTestFiles(ctx, rr)
	if rr.touched && rr.plan.planned() {
		for _, c := range rr.plan.Commands {
			if ctx.Err() != nil {
				return
			}
			cr := &cmdRun{repo: rr.name, check: rr.name + ":" + c.Name, name: c.Name, phase: c.Phase,
				cmd: c.Cmd, origin: originPlan, timeout: r.timeoutOf(c)}
			if why := r.unmet(c, rr.dir); why != "" {
				cr.skipped = why
			} else {
				r.runCommand(ctx, rr, cr)
			}
			rr.runs = append(rr.runs, cr)
		}
	}
	// authored binding commands the plan did not carry itself (a planned
	// command skipped for an unmet precondition stays skipped: its binding
	// resolves skipped, never by an unconditioned run)
	seen := map[string]bool{}
	for _, cr := range rr.runs {
		seen[strings.TrimSpace(cr.cmd)] = true
	}
	for _, b := range r.facts.Bindings {
		c := strings.TrimSpace(b.Command)
		if b.Repo != rr.name || c == "" || seen[c] {
			continue
		}
		seen[c] = true
		cr := &cmdRun{repo: rr.name, check: fmt.Sprintf("%s:authored-%d", rr.name, len(rr.authored)+1),
			name: fmt.Sprintf("authored-%d", len(rr.authored)+1), phase: originAuthored, cmd: c,
			origin: originAuthored, timeout: r.o.CommandTimeout}
		r.runCommand(ctx, rr, cr)
		rr.authored = append(rr.authored, cr)
	}
}

// unmet answers the unmet precondition of a planned command ("" = none).
// A required command naming a path (`./bin/tool`) is resolved against the
// repo, where the command runs; a bare name on the command PATH.
func (r *runner) unmet(c EngineCommand, repoDir string) string {
	var missing []string
	for _, v := range c.RequiresEnv {
		if val, ok := r.lookupEnv(v); !ok || val == "" {
			missing = append(missing, "env "+v)
		}
	}
	for _, b := range c.RequiresCmd {
		if !r.lookPath(b, repoDir) {
			missing = append(missing, "command "+b)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "declared precondition unmet: missing " + strings.Join(missing, ", ")
}

// runCommand runs one bar command, parses its units, locates them and
// applies the flaky policy.
func (r *runner) runCommand(ctx context.Context, rr *repoRun, cr *cmdRun) {
	res := r.exec(ctx, rr, execSpec{Argv: bashArgv(cr.cmd), Dir: rr.dir, Cwd: rr.cwdRel, Timeout: cr.timeout, PrivateTmp: true})
	if res.NotStarted {
		cr.exit, cr.notStarted = -1, "not started: "+r.deadlineNote()+" had passed"
		return
	}
	cr.exit, cr.timedOut, cr.deadlineHit, cr.logPath = res.Exit, res.TimedOut, res.DeadlineHit, res.LogPath
	cr.dur = time.Duration(res.Evidence.DurationMs) * time.Millisecond
	lines := normalizeOutput(outputLines(res.LogPath), rr.dir, res.TmpDir)
	words, _ := shellWords(cr.cmd)
	parsed := parseUnitsOpts(lines, parseOpts{failfast: hasGoTest(words) && hasFlag(words, "-failfast")})
	cr.harness = rr.isHarness(cr.cmd)
	raceSeen := false
	for _, l := range lines {
		if strings.Contains(l, "WARNING: DATA RACE") || strings.Contains(l, "race detected during execution of test") {
			raceSeen = true
			break
		}
	}
	for _, u := range parsed {
		ur := &unitRun{unit: u, Runs: 1}
		if res.Exit == 0 {
			// the exit status is the truth: a passing command's output
			// may quote failure markers (a fixture, a log excerpt)
			ur.Failed = false
		}
		cr.units = append(cr.units, ur)
	}
	if res.Exit != 0 && len(failedRuns(cr.units)) == 0 {
		// nothing located: the whole command is the one unit
		cr.units = append(cr.units, &unitRun{unit: &unit{Kind: KindCommand, Name: cr.check, Failed: true,
			Marker: fmt.Sprintf("exit %d", res.Exit)}, Runs: 1})
	}
	for _, u := range cr.units {
		r.locate(ctx, rr, cr, u, raceSeen)
		if cr.origin == originAuthored || (u.File != "" && r.facts.protectedPath(u.File)) {
			u.Protected = true
		}
	}
	for _, u := range failedRuns(cr.units) {
		r.flaky(ctx, rr, cr, u)
	}
}

func failedRuns(us []*unitRun) []*unitRun {
	var out []*unitRun
	for _, u := range us {
		if u.Failed {
			out = append(out, u)
		}
	}
	return out
}

// locate finds a unit's test file and derives its targeted re-run (a
// re-run that would not run the unit the way the plan did is none: the
// unit's Note says why). raceSeen: the command's output reported a data
// race, which only a re-run carrying -race can reproduce.
func (r *runner) locate(ctx context.Context, rr *repoRun, cr *cmdRun, u *unitRun, raceSeen bool) {
	words, simple := shellWords(cr.cmd)
	why := ""
	switch u.Kind {
	case KindGo, KindGoPkg:
		target, run := "", ""
		if u.Kind == KindGo {
			u.File = goTestFile(ctx, rr.dir, u.Name, u.Pkg)
			run = "^" + regexp.QuoteMeta(u.Name) + "$"
			switch {
			case u.File != "" && path.Dir(u.File) == ".":
				target = "."
			case u.File != "":
				target = "./" + path.Dir(u.File) + "/"
			case strings.Contains(u.Pkg, "/") || strings.Contains(u.Pkg, "."):
				target = u.Pkg
			}
		} else if !u.BuildFailed && (strings.Contains(u.Name, "/") || strings.Contains(u.Name, ".")) {
			target = u.Name
		}
		if target != "" {
			u.Rerun, why = goRerun(cr.cmd, run, target, cr.timeout)
		}
		if u.Rerun != "" && raceSeen && !strings.Contains(" "+u.Rerun+" ", " -race ") {
			u.Rerun, why = "", "not re-runnable: the run reported a data race and a re-run without -race cannot prove it flaky"
		}
	case KindPytest:
		u.File, _, _ = strings.Cut(u.Name, "::")
		u.Rerun, why = pyRerun(cr.cmd, "pytest", u.Name, pytestSwitches)
	case KindUnittest:
		parts := strings.Split(u.Name, ".")
		if len(parts) >= 3 {
			u.File = strings.Join(parts[:len(parts)-2], "/") + ".py"
		}
		u.Rerun, why = pyRerun(cr.cmd, "unittest", u.Name, unittestSwitches)
	case KindLibtest:
		if w, i := commandWord(words); simple && w == "cargo" && i+1 < len(words) && words[i+1] == "test" {
			sep := " -- "
			if tokenIndex(words, "--") >= 0 {
				sep = " "
			}
			u.Rerun = cr.cmd + sep + "--exact " + shellQuote(u.Name)
		}
	case KindMjs:
		if w, _ := commandWord(words); simple && w == "node" {
			var files []string
			for _, x := range words {
				if strings.HasSuffix(x, ".mjs") {
					files = append(files, x)
				}
			}
			if len(files) == 1 {
				u.File = cleanRel(files[0])
				u.Rerun = cr.cmd
			}
		}
	}
	u.File = cleanRel(u.File)
	if u.Rerun == "" && why != "" {
		u.Note = why
	}
}

// tokenIndex answers the index of the word that is tok or ends in /tok.
func tokenIndex(words []string, tok string) int {
	for i, w := range words {
		if w == tok || strings.HasSuffix(w, "/"+tok) {
			return i
		}
	}
	return -1
}

// goTestFile locates the _test.go file declaring a top-level test,
// disambiguated by its package when several do ("" = not located).
func goTestFile(ctx context.Context, dir, name, pkg string) string {
	out, err := gitQuiet(ctx, dir, "grep", "-l", "-E", "^func "+regexp.QuoteMeta(name)+`\(`, "--", "*_test.go")
	if err != nil {
		return ""
	}
	files := strings.Fields(out)
	if len(files) == 1 {
		return files[0]
	}
	var match []string
	for _, f := range files {
		d := path.Dir(f)
		if pkg != "" && (strings.HasSuffix(pkg, "/"+d) || pkg == d) {
			match = append(match, f)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	return ""
}

// flaky re-runs one failing unit at HEAD, serially, at most Reruns
// times while the budget allows; one pass records it flaky.
func (r *runner) flaky(ctx context.Context, rr *repoRun, cr *cmdRun, u *unitRun) {
	if u.Rerun == "" {
		if u.Note == "" {
			u.Note = "not re-runnable: its targeted re-run cannot be derived"
		}
		if u.Kind == KindCommand {
			u.Note = "no failure unit could be located in the output: the whole command is the unit"
		}
		if u.BuildFailed {
			u.Note = "the package did not build"
		}
		return
	}
	for i := 1; i <= r.o.Reruns && ctx.Err() == nil; i++ {
		if !r.budgetLeft() {
			u.Note = fmt.Sprintf("re-run %d not started: verify-run's budget (%s) is spent", i, r.o.Budget)
			return
		}
		res := r.exec(ctx, rr, execSpec{Argv: bashArgv(u.Rerun), Dir: rr.dir, Cwd: rr.cwdRel, Timeout: cr.timeout, PrivateTmp: true})
		if res.NotStarted {
			u.Note = fmt.Sprintf("re-run %d not started: %s had passed", i, r.deadlineNote())
			return
		}
		u.Runs++
		u.rerunDur = time.Duration(res.Evidence.DurationMs) * time.Millisecond
		lines := normalizeOutput(outputLines(res.LogPath), rr.dir, res.TmpDir)
		us := parseUnits(lines)
		if unitPassed(u.unit, res.Exit, us, lines) {
			// one passing run ends the re-runs: passes/runs
			u.Flaky = fmt.Sprintf("flaky 1/%d", u.Runs)
			u.PassLogs = append(u.PassLogs, res.LogPath)
			return
		}
		if got := findUnit(us, u.Kind, u.Name); got != nil && got.Failed {
			u.HeadSig = got.Signature
		}
	}
	u.Note = fmt.Sprintf("still failing on %d re-run(s) at HEAD", r.o.Reruns)
}

var (
	rePytestPassed = regexp.MustCompile(`\b[1-9][0-9]* passed\b`)
	reUnittestRan  = regexp.MustCompile(`^Ran [1-9][0-9]* tests?\b`)
)

// unitPassed judges a targeted run as a pass of u: positive evidence of
// the unit passing is required (a run that matched nothing is no pass).
func unitPassed(u *unit, exit int, us []*unit, lines []string) bool {
	got := findUnit(us, u.Kind, u.Name)
	if got != nil && got.Failed {
		return false
	}
	switch u.Kind {
	case KindGo, KindGoPkg, KindLibtest:
		return exit == 0 && got != nil
	case KindPytest:
		if exit != 0 {
			return false
		}
		for _, l := range lines {
			if rePytestPassed.MatchString(l) {
				return true
			}
		}
		return got != nil
	case KindUnittest:
		ran, ok := false, false
		for _, l := range lines {
			ran = ran || reUnittestRan.MatchString(l)
			ok = ok || strings.HasPrefix(l, "OK")
		}
		return exit == 0 && ran && ok
	case KindMjs:
		// the whole suite re-ran: it passes only as a whole (a crash
		// after the title's PASS line is no pass)
		return exit == 0 && got != nil
	}
	return false
}
