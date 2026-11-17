package runtime

// claimedparams.go: the claim's consumption of a QUEUED work item's
// step_params attrs (polyforge#79) — the values any machine's daemon
// wrote on its machine-signed attrs face while the work item was held by
// no one, so any machine and any Cell that claims the ticket sees them.
// The claim-time lane snapshot (the fresh capsule's own copy, F7) is
// their judge: an entry whose step and param the snapshot declares with
// a legal value merges into the overlay record — the per-ticket layer
// the next dispatch composes over the project's and the lane's values —
// and anything else is dropped with one stderr line, never a refused
// claim (a ticket is not stranded by params its lane drifted away from;
// the same rule the overlay composition holds: an undeclared param
// governs nothing). The merged record is published at once and only
// that publish's success clears the aihub key — the
// writePendingStepOverrides rule: a failed publish keeps the key for the
// next claim while the job's own publishes carry the merged overlay.
//
// The key belongs to the QUEUED ticket (owner ruling 2026-11-17: 未领取的
// 票的参数)； a claimed one's truth is the capsule chain. A RE-claim — the
// sealed-head continue of a blocked ticket (j.sealedScene) — therefore
// clears any residual key WITHOUT merging (review r1#8): the residue is
// a word from before the first claim (the write-side race that left it,
// a publish that failed, a clear that failed), and merging it again with
// the claim's own time would let it win over the running writes the
// chain carries — the newer word. A first claim (no sealed head) is the
// key's one consumption; the narrow loss window of a first claim that
// never published anything is the crash a pending record survives too.
//
// A clear that fails is one loud stderr line; the residue stays for the
// next re-claim, which clears it again without merging.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// StepParamsClaimedBy labels a merged param's writer in the overlay
// record: the attrs key the claim consumed (the record's By field) —
// the daemon's queued faces read the same label back.
const StepParamsClaimedBy = "step_params"

// writeClaimedStepParams merges the claim answer's step_params attrs
// into the fresh capsule's overlay record and clears the key once the
// merge's publish succeeded (see the file comment).
func (r *Runtime) writeClaimedStepParams(ctx context.Context, j *job) error {
	params := StepParamsOfAttrs(j.item.Attrs)
	if len(params) == 0 {
		return nil
	}
	if j.sealedScene != "" {
		// a re-claim: the key is residue of the queued ticket this
		// capsule's chain superseded — cleared, never merged (the file
		// comment's rule)
		fmt.Fprintf(os.Stderr, "runtime: %s: a residual step_params attrs key is cleared at the re-claim without merging (the capsule chain is the truth of a claimed work item)\n", j.wi)
		if _, err := r.hub.WriteAttrs(j.wi, api.AttrsWriteRequest{Key: api.StepParamsKey, Value: []byte("null")}); err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: clearing the residual step_params attrs key failed (it is cleared again at the next re-claim): %v\n", j.wi, err)
		}
		return nil
	}
	kept := claimedStepParams(params, j)
	if len(kept) > 0 {
		ov, err := capsule.ReadStepOverrides(j.capsuleDir)
		if err != nil {
			return fmt.Errorf("merging the queued step params of %s: %w", j.wi, err)
		}
		if err := capsule.WriteStepOverrides(j.capsuleDir, mergePendingAspects(ov, kept)); err != nil {
			return fmt.Errorf("writing the fresh capsule's step params: %w", err)
		}
		pctx, cancel := context.WithTimeout(ctx, protocol.PushTimeout)
		defer cancel()
		if _, pushed, _, perr := pendingPublishFn(pctx, j.git, "step params: the queued ticket's params merged at claim"); perr != nil || !pushed {
			fmt.Fprintf(os.Stderr, "runtime: %s: the claim's publish of the queued step params failed (the attrs key stays for the next claim; the job's own publishes carry the merge): %v\n", j.wi, perr)
			return nil
		}
	}
	// the key's consumption record: the capsule chain (or the judgment
	// that consumed it) is the truth now
	if _, err := r.hub.WriteAttrs(j.wi, api.AttrsWriteRequest{Key: api.StepParamsKey, Value: []byte("null")}); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: clearing the consumed step_params attrs key failed (it merges again at the next claim): %v\n", j.wi, err)
	}
	return nil
}

// claimedStepParams judges the attrs' entries against the fresh capsule's
// lane snapshot: it answers the pending-shaped entries to merge (each
// param with By=step_params and the claim's time — later than any pending
// aspect of the same param). An entry the snapshot does not declare, or
// whose value breaks the bound, is dropped with one stderr line naming it.
func claimedStepParams(params map[string]map[string]string, j *job) map[string]*capsule.StepOverride {
	dir := filepath.Join(j.capsuleDir, capsule.DirWorkflows, j.bundleName)
	lane, errs := workflow.Load(dir)
	if lane == nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: the queued step params cannot be judged (the claim-time lane snapshot does not read): %v; nothing merged\n", j.wi, errs)
		return nil
	}
	byID := make(map[string]*workflow.Step, len(lane.Steps))
	for i := range lane.Steps {
		byID[lane.Steps[i].ID] = &lane.Steps[i]
	}
	at := nowStamp()
	entries := map[string]*capsule.StepOverride{}
	for _, step := range sortedKeys(params) {
		spec := byID[step]
		for _, name := range sortedKeys(params[step]) {
			value := params[step][name]
			reason := ""
			switch {
			case spec == nil:
				reason = "the lane declares no such step"
			case !declaresParam(spec.Params, name):
				reason = "the lane declares no such param on the step"
			case workflow.ParamValueProblem(value) != "":
				reason = workflow.ParamValueProblem(value)
			}
			if reason != "" {
				fmt.Fprintf(os.Stderr, "runtime: %s: the queued step param %s.%s is dropped at the claim (%s)\n", j.wi, step, name, reason)
				continue
			}
			if entries[step] == nil {
				entries[step] = &capsule.StepOverride{}
			}
			if entries[step].Params == nil {
				entries[step].Params = map[string]*capsule.ParamOverride{}
			}
			entries[step].Params[name] = &capsule.ParamOverride{Value: value, By: StepParamsClaimedBy, At: at}
		}
	}
	return entries
}

// declaresParam answers whether the step's declared params carry name
// (the default may be the empty string, so the map's zero is no answer).
func declaresParam(declared map[string]string, name string) bool {
	_, ok := declared[name]
	return ok
}

// sortedKeys answers the map's keys in order (the judgment's stderr lines
// stay deterministic).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
