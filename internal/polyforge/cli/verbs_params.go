package cli

// verbs_params.go: `pf params <id>` (polyforge#79) — the composed params
// read face: one row per declared step, every param the lane declares on
// it with its FINAL value and the layer it comes from — the per-ticket
// layer ("step override": the capsule's overlay record of a claimed
// work item, or the reserved step_params attrs key of a queued one), the
// project's steps.<project>.<step>.params.<name>, or the lane default.
// The overlay view (`pf override`) stays the overlay layer alone; this
// verb is the one Pi Web's panel and work-item detail read through. It
// is read-only — the write is `pf override --step <id> --param` (a
// queued work item included).

import (
	"flag"
	"fmt"
	"io"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

var cmdParams = &Command{
	Name:    "params",
	Group:   GroupExec,
	Summary: "View one work item's step params: every declared param's final value and the layer it comes from",
	Usage:   "pf params <id>",
	Setup:   func(*flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("params: expected exactly one work item id")
		}
		raw, err := ctx.daemon().ParamsView(args[0])
		if err != nil {
			return err
		}
		finishOrRaw(ctx, raw, func(v *protocol.ParamsView) bool {
			renderParamsView(ctx.Stdout, v)
			return true
		})
		return nil
	},
}

// renderParamsView prints the view: one line per step with its status,
// every declared param's final value and source under it.
func renderParamsView(w io.Writer, v *protocol.ParamsView) {
	fmt.Fprintf(w, "work item:  %s\n", v.WorkItem)
	fmt.Fprintf(w, "posture:    %s\n", v.Posture)
	for _, s := range v.Steps {
		status := s.Status
		if status == "" {
			status = "not started"
		}
		fmt.Fprintf(w, "step %-14s status=%s", s.ID, status)
		for _, p := range s.Params {
			fmt.Fprintf(w, "\n              param %s=%q (%s)", p.Name, p.Value, p.Source)
		}
		fmt.Fprintln(w)
	}
}
