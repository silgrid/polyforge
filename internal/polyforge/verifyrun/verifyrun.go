// Package verifyrun is `pf verify-run`: the verification step's
// mechanical half, run as ONE blocking command at the capsule root.
//
// The verification step stays an agent. Everything that is mechanical —
// merging the latest origin default branch into every task repo, asking
// the release-shipped scope engine (`tools/verify-scope --json`) for each
// touched repo's plan, running the WHOLE plan (never stopping at the
// first failure), parsing failure units, re-running failing units for
// flakiness, proving pre-existing failures on the base, deriving the
// test bar and the untracked/tracked test-file checks — happens here,
// deterministically, and lands in `result.json` plus the report's
// mechanical section `report-mechanical.md`. The agent then only judges
// (lane checks, document-only scenarios, the scope declaration) and
// submits the envelope.
//
// The command never talks to the daemon or to aihub and never writes
// execution.json: a step process may call it. Its command logs live
// under runtime-state/verify/<n>/<repo>/<k>.log, beside a `.gitignore`
// that keeps the whole runtime-state/verify tree out of the capsule's
// git chain (every capsule file is published to aihub, whose pre-receive
// refuses blobs above 16 MiB; a test log is local debugging material,
// never chain content). A red bar is data, not an error: Run answers an
// error only when it could not run at all, and a result whose Incomplete
// reason is set when the merged-tree proof itself was impossible (a
// failed fetch, an unresolvable default branch, a merge that neither
// completed nor aborted cleanly).
package verifyrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// Defaults of the knobs Options leaves zero.
const (
	DefaultCommandTimeout = 20 * time.Minute
	DefaultBudget         = 20 * time.Minute
	// DefaultDeadline is the whole run's deadline when the harness's
	// shell-call maximum is not known (BASH_MAX_TIMEOUT_MS unset).
	DefaultDeadline = 25 * time.Minute
	// deadlineMargin is what a derived deadline leaves below the
	// harness's shell-call maximum.
	deadlineMargin = 2 * time.Minute
	// writeMargin is kept free at the end of the run for the cleanup and
	// the result: no command runs into it.
	writeMargin     = 20 * time.Second
	DefaultReruns   = 2
	DefaultBaseRuns = 2
)

// The tier vocabulary (the driver's own).
const (
	TierFull   = "full"
	TierScoped = "scoped"
)

// Options is one verify-run invocation.
type Options struct {
	// CapsuleDir is the capsule root (capsule.json, repos/, runtime-state/).
	CapsuleDir string
	// Base overrides every touched repo's pre-existing/engine base
	// ("" = merge-base HEAD origin/<default> after the merge, which is
	// the merged-in default tip).
	Base string
	// Tier is the verification tier the step's prompt names (full|scoped;
	// "" = full). The runner never derives it.
	Tier string
	// Engine is the scope engine's path (`<data-dir>/release/current/tools/verify-scope`).
	Engine string
	// OutDir receives result.json and report-mechanical.md ("" = the
	// run's own runtime-state/verify/<n>).
	OutDir string
	// MaxParallelRepos bounds how many repos run their bars at once (<= 0 = 1).
	MaxParallelRepos int
	// CommandTimeout is the per-command timeout a plan command without
	// its own declared timeout gets (0 = DefaultCommandTimeout).
	CommandTimeout time.Duration
	// Budget bounds the wall time of the OPTIONAL phases — flaky re-runs
	// and pre-existing base runs — measured from the start. Plan commands
	// always run; a unit whose re-run or base run the budget no longer
	// allows stays a failure (fail closed). 0 = DefaultBudget.
	Budget time.Duration
	// E2EJobs / GoMaxProcs, when > 0, are exported to every command as
	// E2E_JOBS / GOMAXPROCS (resource governance); 0 = inherited.
	E2EJobs    int
	GoMaxProcs int
	// Deadline bounds the WHOLE run (0 = DefaultDeadlineFor(Env), and a
	// value above that bound is clamped to it): every command's timeout is
	// cut to what is left of it, a command that can no longer start is
	// recorded failed, and result.json and the report are written before it
	// passes — the call that runs verify-run must never outlive the daemon's
	// tool rule. Result.DeadlineClampedFrom records a clamped request.
	Deadline time.Duration
	// Reruns / BaseRuns are the flaky re-run count at HEAD and the
	// pre-existing run count on the base (0 = the defaults).
	Reruns   int
	BaseRuns int
	// Env is the environment commands start from (nil = os.Environ()).
	Env []string
}

func (o Options) withDefaults() Options {
	if o.Tier == "" {
		o.Tier = TierFull
	}
	if o.MaxParallelRepos <= 0 {
		o.MaxParallelRepos = 1
	}
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = DefaultCommandTimeout
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	if o.Reruns <= 0 {
		o.Reruns = DefaultReruns
	}
	if o.BaseRuns <= 0 {
		o.BaseRuns = DefaultBaseRuns
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	bound := DefaultDeadlineFor(o.Env)
	if o.Deadline <= 0 || o.Deadline > bound {
		o.Deadline = bound
	}
	return o
}

// DefaultDeadlineFor derives the whole-run deadline from the tightest bound
// the step environment declares: BASH_MAX_TIMEOUT_MS (a claude step's
// shell-call maximum) and PF_TOOL_WINDOW_MS (the daemon's supervision tool
// window, carried by every harness). It leaves deadlineMargin below that
// bound, or half the bound when that is smaller, so the run always finishes
// before the daemon judges the call hung; without either value it falls back
// to DefaultDeadline.
func DefaultDeadlineFor(env []string) time.Duration {
	bound := time.Duration(0)
	consider := func(name string) {
		raw, ok := envLast(env, name)
		if !ok {
			return
		}
		ms, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || ms <= 0 {
			return
		}
		if d := time.Duration(ms) * time.Millisecond; bound == 0 || d < bound {
			bound = d
		}
	}
	consider("BASH_MAX_TIMEOUT_MS")
	consider(spawn.EnvToolWindow)
	if bound <= 0 {
		return DefaultDeadline
	}
	if d := bound - min(deadlineMargin, bound/2); d > 0 {
		return d
	}
	return time.Millisecond
}

// envLast answers the last value a variable carries in env, as the process
// environment resolves duplicates.
func envLast(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// Validate rejects an unusable invocation before anything runs.
func (o Options) Validate() error {
	switch o.Tier {
	case "", TierFull, TierScoped:
	default:
		return fmt.Errorf("--tier must be %q or %q (got %q)", TierFull, TierScoped, o.Tier)
	}
	if strings.TrimSpace(o.CapsuleDir) == "" {
		return errors.New("no capsule root")
	}
	if strings.TrimSpace(o.Engine) == "" {
		return errors.New("no scope engine path")
	}
	return nil
}

// verifyDir is the capsule-relative root of every run's logs and outputs.
const verifyDir = "runtime-state/verify"

// verifyGitignore keeps the whole runtime-state/verify tree (itself
// included) out of the capsule's git chain.
const verifyGitignore = "# pf verify-run: command logs are local, never capsule chain content\n*\n"

// Run executes one verify-run over the capsule and writes result.json and
// report-mechanical.md. A non-nil error means nothing could run (no
// capsule, no writable run directory); a red bar or an impossible merge
// is reported inside the Result.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	requested := opts.Deadline
	sweepStaleTmp()
	o := opts.withDefaults()
	abs, err := filepath.Abs(o.CapsuleDir)
	if err != nil {
		return nil, err
	}
	o.CapsuleDir = abs
	facts, err := readCapsuleFacts(o.CapsuleDir)
	if err != nil {
		return nil, err
	}
	n, runDir, err := newRunDir(o.CapsuleDir)
	if err != nil {
		return nil, err
	}
	outDir := runDir
	if o.OutDir != "" {
		outDir = o.OutDir
		if !filepath.IsAbs(outDir) {
			outDir = filepath.Join(o.CapsuleDir, outDir)
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, fmt.Errorf("--out: %w", err)
		}
	}
	r := &runner{
		o:        o,
		facts:    facts,
		runDir:   runDir,
		deadline: time.Now().Add(o.Budget),
		hard:     time.Now().Add(o.Deadline - writeMargin),
	}
	res := r.run(ctx)
	res.Run = n
	res.Deadline = o.Deadline.String()
	if requested > o.Deadline {
		res.DeadlineClampedFrom = requested.String()
	}
	res.OutDir = r.rel(outDir)
	res.LogDir = r.rel(runDir)
	if err := writeOutputs(outDir, res); err != nil {
		return res, err
	}
	return res, nil
}

// newRunDir allocates runtime-state/verify/<n> (n = one past the highest
// existing run) and makes sure the tree's .gitignore exists.
func newRunDir(capsuleDir string) (int, string, error) {
	root := filepath.Join(capsuleDir, filepath.FromSlash(verifyDir))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, "", fmt.Errorf("create %s: %w", verifyDir, err)
	}
	gi := filepath.Join(root, ".gitignore")
	if raw, err := os.ReadFile(gi); err != nil || string(raw) != verifyGitignore {
		if err := os.WriteFile(gi, []byte(verifyGitignore), 0o644); err != nil {
			return 0, "", fmt.Errorf("write %s/.gitignore: %w", verifyDir, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, "", err
	}
	n := 0
	for _, e := range entries {
		if v, err := strconv.Atoi(e.Name()); err == nil && v > n {
			n = v
		}
	}
	for {
		n++
		dir := filepath.Join(root, strconv.Itoa(n))
		if err := os.Mkdir(dir, 0o755); err == nil {
			return n, dir, nil
		} else if !os.IsExist(err) {
			return 0, "", err
		}
	}
}

// runner carries one run's state.
type runner struct {
	o        Options
	facts    *capsuleFacts
	runDir   string
	deadline time.Time // the optional phases' budget
	hard     time.Time // no command runs past it (the run's deadline less writeMargin)
}

// rel answers p relative to the capsule root (slash form), or p itself
// when it lies outside.
func (r *runner) rel(p string) string {
	rel, err := filepath.Rel(r.o.CapsuleDir, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(rel)
}

// budgetLeft reports whether the optional phases may still start work.
func (r *runner) budgetLeft() bool { return time.Now().Before(r.deadline) }

// remaining answers the time left before the run's hard deadline.
func (r *runner) remaining() time.Duration { return time.Until(r.hard) }

// tailCtx bounds the run's closing git work (cleanup, the after-bar
// scans) by the deadline, keeping half the write margin for the result.
func (r *runner) tailCtx() (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), r.hard.Add(writeMargin/2))
}

// fits reports whether a run expected to take about d still fits before
// the run's deadline.
func (r *runner) fits(d time.Duration) bool { return r.remaining() > d }

// deadlineNote names the deadline a skipped or cut command ran into.
func (r *runner) deadlineNote() string {
	return fmt.Sprintf("verify-run's deadline (%s)", r.o.Deadline)
}

// run is the orchestration: the merge phase for every repo (serial: a
// failure there makes the whole result incomplete before any bar runs),
// the bars (parallel up to MaxParallelRepos, each repo's commands in
// order, flaky re-runs serial inside the repo), the pre-existing base
// runs (serial, after every bar), then the authored bindings and the
// test-file checks.
func (r *runner) run(ctx context.Context) *Result {
	res := &Result{
		Schema:          1,
		Tier:            TierInfo{Requested: r.o.Tier},
		MergeOutcomes:   map[string]*MergeOutcome{},
		ConfChanged:     map[string]bool{},
		AuthoredTitles:  r.facts.ScenarioTitles,
		Authored:        r.facts.Authored,
		CommandEvidence: []Evidence{},
		Checks:          []Check{},
		PreExisting:     []string{},
		TestBar:         []TestBarEntry{},
		Units:           []UnitReport{},
	}
	repos := make([]*repoRun, 0, len(r.facts.Repos))
	for _, name := range r.facts.Repos {
		repos = append(repos, r.newRepoRun(name))
	}

	// 1. merge phase (serial).
	for _, rr := range repos {
		if reason := r.mergePhase(ctx, rr); reason != "" {
			res.Incomplete = reason
			break
		}
	}
	if res.Incomplete == "" {
		// 2. plan per touched repo (engine), serial — it is quick.
		for _, rr := range repos {
			if rr.merge != nil && rr.touched {
				r.planRepo(ctx, rr)
			}
		}
		// 3. bars (+ flaky re-runs, + authored binding commands).
		sem := make(chan struct{}, r.o.MaxParallelRepos)
		var wg sync.WaitGroup
		for _, rr := range repos {
			wg.Add(1)
			go func(rr *repoRun) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				r.runBar(ctx, rr)
			}(rr)
		}
		wg.Wait()
		// 4. pre-existing base runs (serial, after every bar), then the
		// confirming re-run of every command whose failures were all
		// explained away.
		for _, rr := range repos {
			r.preExistingPhase(ctx, rr)
			r.confirmPhase(ctx, rr)
		}
		// 5. the untracked-test-file scan after the bar (runBar took the
		// one before it).
		tctx, cancel := r.tailCtx()
		for _, rr := range repos {
			rr.untrackedAfter = r.untrackedTestFiles(tctx, rr)
		}
		cancel()
	}
	if err := ctx.Err(); err != nil && res.Incomplete == "" {
		res.Incomplete = fmt.Sprintf("verify-run was interrupted (%v): its record is partial and proves nothing", err)
	}
	r.assemble(res, repos)
	return res
}

// newRepoRun prepares one repo's run state.
func (r *runner) newRepoRun(name string) *repoRun {
	dir := filepath.Join(r.o.CapsuleDir, "repos", name)
	return &repoRun{
		name:      name,
		dir:       dir,
		cwdRel:    "repos/" + name,
		logDir:    filepath.Join(r.runDir, name),
		goHarness: readGoHarness(dir),
	}
}

// writeOutputs writes result.json and report-mechanical.md into dir.
func writeOutputs(dir string, res *Result) error {
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report-mechanical.md"), []byte(renderReport(res)), 0o644)
}

// sortedKeys answers a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
