package verifyrun

// engine.go: asking the release-shipped scope engine for one touched
// repo's plan — `tools/verify-scope --json --base <base>` with the repo
// clone as its working directory, plus `--full` on the full tier (the
// engine otherwise decides its mode from the diff and would answer a
// narrow diff with its scoped selection) — and choosing the commands the
// tier runs. A repo whose plan cannot be had is reported unplanned and its
// bar stays the step's own duty.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EngineReport is the engine's `--json` report (schema 1).
type EngineReport struct {
	Schema            int             `json:"schema"`
	Root              string          `json:"root"`
	Stack             string          `json:"stack"`
	Analyzer          *string         `json:"analyzer"`
	Conf              string          `json:"conf"`
	ConfChangedInDiff bool            `json:"conf_changed_in_diff"`
	Base              string          `json:"base"`
	Head              string          `json:"head"`
	DefaultBranch     string          `json:"default_branch"`
	Mode              string          `json:"mode"`
	Paths             []string        `json:"paths"`
	Commands          []EngineCommand `json:"commands"`
	TestGlob          []string        `json:"test_glob"`
	BaseSetup         []string        `json:"base_setup"`
	Runner            *string         `json:"runner"`
	CIEvidence        []string        `json:"ci_evidence"`
	Diagnostics       []string        `json:"diagnostics"`
}

// EngineCommand is one planned command.
type EngineCommand struct {
	Name        string   `json:"name"`
	Phase       string   `json:"phase"`
	Cmd         string   `json:"cmd"`
	RequiresEnv []string `json:"requires_env"`
	RequiresCmd []string `json:"requires_cmd"`
	Timeout     string   `json:"timeout"`
	Source      string   `json:"source"`
}

// The plan statuses.
const (
	PlanPlanned      = "planned"
	PlanEmpty        = "empty"         // the engine planned no command (an unsupported stack without a conf)
	PlanEngineFailed = "engine-failed" // the engine is missing, failed or answered an unreadable report
	PlanNoFullPlan   = "no-full-plan"  // the tier is full but the engine answered --full with a scoped report
)

// repoPlan is one touched repo's chosen plan.
type repoPlan struct {
	Status    string
	Reason    string
	Mode      string // the mode the chosen commands belong to (full|scoped)
	Commands  []EngineCommand
	EngineRef string
	report    *EngineReport // the first (real-base) report
}

// planned reports whether the plan carries commands to run.
func (p *repoPlan) planned() bool { return p != nil && p.Status == PlanPlanned }

// planRepo asks the engine and chooses rr's commands for the tier.
func (r *runner) planRepo(ctx context.Context, rr *repoRun) {
	base := ""
	if rr.merge != nil {
		base = rr.merge.Base
	}
	rep, ref, why := r.askEngine(ctx, rr, base, r.o.Tier == TierFull)
	p := &repoPlan{EngineRef: ref, report: rep}
	rr.plan = p
	if rep == nil {
		p.Status, p.Reason = PlanEngineFailed, why
		return
	}
	if r.o.Tier == TierFull && rep.Mode != TierFull {
		p.Status, p.Reason = PlanNoFullPlan, fmt.Sprintf("the tier is full but the engine answered --full with mode %q", rep.Mode)
		return
	}
	chosen, mode := choose(rep, r.o.Tier)
	p.Mode = mode
	if len(chosen) == 0 {
		p.Status = PlanEmpty
		p.Reason = fmt.Sprintf("the engine planned no command (stack %q, conf %s)", rep.Stack, rep.Conf)
		return
	}
	p.Status, p.Commands = PlanPlanned, chosen
}

// choose answers the commands one report contributes for the tier and
// the mode they belong to: the fast gates plus the full tail on a full
// report, the fast gates plus the scoped selection on a scoped report
// (only under the scoped tier; the full tier never runs a scoped plan).
func choose(rep *EngineReport, tier string) ([]EngineCommand, string) {
	mode := TierFull
	if rep.Mode == TierScoped && tier == TierScoped {
		mode = TierScoped
	}
	var out []EngineCommand
	for _, c := range rep.Commands {
		switch c.Phase {
		case "fast", mode:
			out = append(out, c)
		}
	}
	return out, mode
}

// askEngine runs the engine once in rr and decodes its report; a nil
// report comes with the reason.
func (r *runner) askEngine(ctx context.Context, rr *repoRun, base string, full bool) (*EngineReport, string, string) {
	argv := []string{r.o.Engine, "--json"}
	if full {
		argv = append(argv, "--full")
	}
	if base != "" {
		argv = append(argv, "--base", base)
	}
	ref := strings.Join(argv, " ")
	var stdout bytes.Buffer
	res := r.exec(ctx, rr, execSpec{Argv: argv, Dir: rr.dir, Cwd: rr.cwdRel, Timeout: gitStepTimeout, Capture: &stdout})
	if res.Err != nil {
		return nil, ref, fmt.Sprintf("the engine %s could not start: %v", r.o.Engine, res.Err)
	}
	if res.Exit != 0 {
		return nil, ref, fmt.Sprintf("the engine exited %d: %s", res.Exit, logTail(res.LogPath))
	}
	rep, err := decodeReport(stdout.Bytes())
	if err != nil {
		return nil, ref, "the engine's report is unreadable: " + err.Error()
	}
	return rep, ref, ""
}

// decodeReport parses and checks one engine report against the contract.
func decodeReport(raw []byte) (*EngineReport, error) {
	var rep EngineReport
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&rep); err != nil {
		return nil, err
	}
	if rep.Schema != 1 {
		return nil, fmt.Errorf("schema %d, want 1", rep.Schema)
	}
	if rep.Mode != TierFull && rep.Mode != TierScoped {
		return nil, fmt.Errorf("mode %q, want full or scoped", rep.Mode)
	}
	for i, c := range rep.Commands {
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Cmd) == "" {
			return nil, fmt.Errorf("commands[%d] lacks a name or a cmd", i)
		}
		switch c.Phase {
		case "fast", "full", "scoped":
		default:
			return nil, fmt.Errorf("commands[%d] (%s) has phase %q", i, c.Name, c.Phase)
		}
	}
	return &rep, nil
}

// timeoutOf answers a command's declared timeout (the default when none
// or an unreadable one is declared).
func (r *runner) timeoutOf(c EngineCommand) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(c.Timeout)); err == nil && d > 0 {
		return d
	}
	return r.o.CommandTimeout
}
