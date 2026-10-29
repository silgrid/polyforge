package cli

// verbs_verifyrun.go: `pf verify-run` — the verification step's
// mechanical half as one blocking local command (internal/polyforge/
// verifyrun). It runs at the capsule root (the working directory), reads
// capsule.json and runtime-state/execution.json, and writes result.json
// plus report-mechanical.md; it never talks to the daemon or to aihub, so
// a step process may call it (step mode does not refuse an exec-group
// verb, and the invocation carries no aihub key there). NoConfigLoad: it
// needs no cli.toml at all. Exit 0 whenever a result was produced — a
// red bar is data, not an error; exit 1 when it could not run, or —
// deliberately, so a step cannot read it as a verdict — when the
// merged-tree proof was impossible (stderr `incomplete: <reason>`, which
// the step returns as an incomplete outcome).

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/silgrid/polyforge/internal/polyforge/verifyrun"
)

// verifyEngineRel is the scope engine's place in the data dir's current
// release tree.
const verifyEngineRel = "release/current/tools/verify-scope"

var cmdVerifyRun = &Command{
	Name:  "verify-run",
	Group: GroupExec,
	Summary: "Run a verification step's mechanical bar once, at the capsule root (inside a step: merge the default branch, " +
		"plan via the scope engine, run the whole plan, re-run flaky units, prove pre-existing failures; writes result.json)",
	Usage: "pf verify-run [--tier full|scoped] [--deadline D] [--base <rev>] [--engine <path>] [--out <dir>] " +
		"[--max-parallel-repos N] [--timeout D] [--budget D] [--e2e-jobs N] [--gomaxprocs N]",
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.String("tier", verifyrun.TierFull, "the verification tier the step prompt names: full or scoped (never guessed)")
		fs.String("base", "", "override every repo's base (default merge-base HEAD origin/<default> after the merge)")
		fs.String("engine", "", "the scope engine (default <data-dir>/"+verifyEngineRel+")")
		fs.String("out", "", "directory for result.json and report-mechanical.md (default the run's runtime-state/verify/<n>)")
		fs.Int("max-parallel-repos", 1, "how many repos run their bars at once")
		fs.Duration("timeout", verifyrun.DefaultCommandTimeout, "per-command timeout when the plan declares none")
		fs.Duration("deadline", 0, "the whole run's deadline: nothing runs past it and the result is written before it (default: BASH_MAX_TIMEOUT_MS less 2m when set, else 25m)")
		fs.Duration("budget", verifyrun.DefaultBudget, "wall budget for starting flaky re-runs, base runs and confirming re-runs")
		fs.Int("e2e-jobs", 0, "export E2E_JOBS=N to every command (0 = inherited)")
		fs.Int("gomaxprocs", 0, "export GOMAXPROCS=N to every command (0 = inherited)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("verify-run: unexpected arguments %v (see `pf help verify-run`)", args)
		}
		root, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("verify-run: %w", err)
		}
		engine := stringFlag(fs, "engine")
		if engine == "" {
			home, _ := os.UserHomeDir()
			engine = filepath.Join(DataDir(home), filepath.FromSlash(verifyEngineRel))
		}
		opts := verifyrun.Options{
			CapsuleDir:       root,
			Base:             stringFlag(fs, "base"),
			Tier:             stringFlag(fs, "tier"),
			Engine:           engine,
			OutDir:           stringFlag(fs, "out"),
			MaxParallelRepos: intFlag(fs, "max-parallel-repos"),
			CommandTimeout:   durationFlag(fs, "timeout"),
			Budget:           durationFlag(fs, "budget"),
			Deadline:         durationFlag(fs, "deadline"),
			E2EJobs:          intFlag(fs, "e2e-jobs"),
			GoMaxProcs:       intFlag(fs, "gomaxprocs"),
		}
		// an interrupt stops the run cleanly: the running command's group is
		// killed, the base worktree removed, a partial record written
		runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		res, err := verifyrun.Run(runCtx, opts)
		if err != nil {
			return fmt.Errorf("verify-run: %w", err)
		}
		resultPath := filepath.ToSlash(filepath.Join(res.OutDir, "result.json"))
		reportPath := filepath.ToSlash(filepath.Join(res.OutDir, "report-mechanical.md"))
		if ctx.JSON {
			doc := map[string]any{"result": resultPath, "report": reportPath,
				"mechanical_verdict": res.MechanicalVerdict, "incomplete": res.Incomplete}
			raw, _ := json.Marshal(doc)
			fmt.Fprintln(ctx.Stdout, string(raw))
		} else {
			counts := map[string]int{}
			for _, c := range res.Checks {
				counts[c.Status]++
			}
			fmt.Fprintf(ctx.Stdout, "verify-run: result %s\nverify-run: report %s\n", resultPath, reportPath)
			if res.Incomplete == "" {
				fmt.Fprintf(ctx.Stdout, "verify-run: mechanical verdict %s (%d checks: %d pass, %d fail, %d pre_existing, %d skipped); unplanned repos: %d\n",
					res.MechanicalVerdict, len(res.Checks), counts["pass"], counts["fail"], counts["pre_existing"], counts["skipped"], len(res.UnplannedRepos))
			}
		}
		if res.Incomplete != "" {
			return fmt.Errorf("verify-run: incomplete: %s", res.Incomplete)
		}
		return nil
	},
}
