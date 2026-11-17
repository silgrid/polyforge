package cli

// verbs_override.go: `pf override` (polyforge#25) — the step-overlay
// face: view a work item's per-step overlay (the model chains, the skip
// switches, the param overrides, the effective chain and its source), or
// apply one change to one step — set a model chain, clear it, switch the
// skip of a step the lane declares skippable, or set or clear one param
// the lane declares on the step (polyforge#78). A QUEUED work item takes
// the param changes only: they ride the reserved step_params attrs key
// until the claim merges them into its capsule (polyforge#79). One action per call; the write goes
// through the daemon (a step agent is refused there, DAEMON-403) and
// governs the work item's next dispatch of that step. The overlay
// outranks every other candidate source (`pf submit --models`, the
// workspace yaml, the bundle) and travels with the capsule chain.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

var cmdOverride = &Command{
	Name:    "override",
	Group:   GroupExec,
	Summary: "View or change a work item's per-step overlay: the model chain, the skip switch and the params of one step — a queued work item's params included (they ride its attrs until the claim)",
	Usage:   "pf override <id> [--step <step-id> [--models <h:m[:e],...] | --clear-models | --skip | --unskip | --param <name>=<value> | --clear-param <name>] [--by user]]",
	Setup: func(fs *flag.FlagSet) {
		fs.String("step", "", "the step id the change applies to (required for every action; without it the command is the view)")
		fs.String("models", "", "set the step's overlay model chain, comma-separated in order; each is <harness>:<model>[:<effort>] with harness pi or claude (it outranks every other source: --models, the workspace yaml, the bundle)")
		fs.Bool("clear-models", false, "clear the step's overlay model chain (the step composes from its usual sources again)")
		fs.Bool("skip", false, "switch the step off (only a step the lane declares skippable; it is never dispatched and records skipped)")
		fs.Bool("unskip", false, "clear the step's skip switch (it dispatches again)")
		fs.String("param", "", "set one param the lane declares on the step, <name>=<value> (the value is one line of at most 256 bytes, empty allowed); it outranks the workspace yaml and the lane default from the step's next dispatch")
		fs.String("clear-param", "", "clear the step's override of the named param (the workspace yaml or the lane default governs again)")
		fs.String("by", "", "operator user id recorded on the change (default: config user_id / PF_USER_ID)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("override: expected exactly one work item id")
		}
		name := args[0]
		step := stringFlag(fs, "step")
		setModels := stringFlag(fs, "models") != ""
		clearModels := boolFlag(fs, "clear-models")
		skip := boolFlag(fs, "skip")
		unskip := boolFlag(fs, "unskip")
		param := stringFlag(fs, "param")
		clearParam := stringFlag(fs, "clear-param")
		actions := 0
		for _, on := range []bool{setModels, clearModels, skip, unskip, param != "", clearParam != ""} {
			if on {
				actions++
			}
		}
		if actions > 0 && step == "" {
			return fmt.Errorf("override: --step is required for every action")
		}
		if actions > 1 {
			return fmt.Errorf("override: exactly one action per call (--models, --clear-models, --skip, --unskip, --param or --clear-param)")
		}
		who := stringFlag(fs, "by")
		if who == "" {
			who = ctx.Cfg.UserID
		}
		if actions > 0 {
			req := protocol.OverrideWriteRequest{Step: step, Operator: who}
			switch {
			case setModels:
				m, merr := workspace.NormalizeModels(strings.Split(stringFlag(fs, "models"), ","))
				if merr != nil {
					return fmt.Errorf("override: --models: %w", merr)
				}
				req.Models = m
			case clearModels:
				req.ClearModels = true
			case skip:
				s := true
				req.Skip = &s
			case unskip:
				s := false
				req.Skip = &s
			case param != "":
				name, value, ok := strings.Cut(param, "=")
				if !ok || name == "" {
					return fmt.Errorf("override: --param takes <name>=<value>, got %q", param)
				}
				req.Param = &protocol.OverrideParam{Name: name, Value: value}
			case clearParam != "":
				req.ClearParam = clearParam
			}
			raw, err := ctx.daemon().OverrideWrite(name, req)
			if err != nil {
				return err
			}
			finishOrRaw(ctx, raw, func(res *protocol.OverrideWriteResult) bool {
				renderOverrideWrite(ctx.Stdout, res, req)
				return true
			})
			return nil
		}
		raw, err := ctx.daemon().OverrideView(name)
		if err != nil {
			return err
		}
		finishOrRaw(ctx, raw, func(v *protocol.OverrideView) bool {
			renderOverrideView(ctx.Stdout, v)
			return true
		})
		return nil
	},
}

// renderOverrideView prints the view: one line per step, the overlay
// aspects and the effective chain with its source.
func renderOverrideView(w io.Writer, v *protocol.OverrideView) {
	fmt.Fprintf(w, "work item:  %s\n", v.WorkItem)
	fmt.Fprintf(w, "posture:    %s\n", v.Posture)
	for _, s := range v.Steps {
		status := s.Status
		if status == "" {
			status = "not started"
		}
		fmt.Fprintf(w, "step %-14s status=%s", s.ID, status)
		if s.Skippable {
			fmt.Fprintf(w, " skippable")
		}
		if s.Models != nil {
			fmt.Fprintf(w, " overlay-models=%s (by %s)", strings.Join(s.Models, ","), s.ModelsBy)
		}
		if s.Skip != nil {
			fmt.Fprintf(w, " overlay-skip=%v (by %s)", *s.Skip, s.SkipBy)
		}
		for _, p := range s.Params {
			fmt.Fprintf(w, " param %s=%q (by %s)", p.Name, p.Value, p.By)
		}
		if s.Pending {
			fmt.Fprintf(w, " pending")
		}
		if len(s.Effective) > 0 {
			fmt.Fprintf(w, "\n              next dispatch: %s (%s)", strings.Join(s.Effective, ","), s.Source)
		}
		fmt.Fprintln(w)
	}
}

// renderOverrideWrite prints one change's answer (req is the change
// sent: a param change names its param, whatever else the row carries).
func renderOverrideWrite(w io.Writer, res *protocol.OverrideWriteResult, req protocol.OverrideWriteRequest) {
	fmt.Fprintf(w, "step %s: ", res.Step.ID)
	if !res.Changed {
		fmt.Fprintf(w, "nothing to change (the aspect was not set)\n")
		return
	}
	by := res.Step.ModelsBy
	if by == "" {
		by = res.Step.SkipBy
	}
	switch {
	case req.Param != nil:
		// the step's param overrides as the daemon recorded them
		by = req.Operator
		fmt.Fprintf(w, "overlay")
		for _, p := range res.Step.Params {
			fmt.Fprintf(w, " param %s=%q", p.Name, p.Value)
			if p.Name == req.Param.Name {
				by = p.By
			}
		}
	case req.ClearParam != "":
		by = req.Operator
		fmt.Fprintf(w, "overlay param %s cleared", req.ClearParam)
	case res.Step.Models != nil:
		fmt.Fprintf(w, "overlay models=%s", strings.Join(res.Step.Models, ","))
	case res.Step.Skip != nil:
		fmt.Fprintf(w, "overlay skip=%v", *res.Step.Skip)
	default:
		fmt.Fprintf(w, "overlay cleared")
	}
	fmt.Fprintf(w, " (posture %s, by %s)\n", res.Posture, by)
	switch {
	case res.Published:
		fmt.Fprintf(w, "published: the capsule chain carries it\n")
	case res.Note != "":
		fmt.Fprintf(w, "%s\n", res.Note)
	default:
		fmt.Fprintf(w, "not published yet\n")
	}
}
