package runtime

// stepoverrides.go: the step-overlay write path (polyforge#25) — one
// per-work-item actor turn per change, three postures:
//
//   - a RUNNING job: the overlay file in its capsule is rewritten
//     (read-modify-write), the capsule chain is committed and pushed at
//     once (a failed push keeps the local commit, exactly like
//     commit-dirty-on-recover), and the change is tapped as an event
//     marked published or not;
//   - a PAUSED record: the same write into the kept scene (a pause seals
//     nothing — stopOp's read-only seal is the terminal closes' — so the
//     scene stays writable), not published: a parked write governs this
//     machine only until its resume here, whose own publishes carry it
//     (the owner ruling of 2026-11-15; aihub refuses pushes for a
//     non-running work item anyway);
//   - no job (the work item parked blocked): the change lands in the
//     daemon-local pending record (like the manual `--models` list),
//     and the next claim's scene preparation merges it into the fresh
//     capsule's overlay file — the sealed scene of the last close is
//     read-only and abandoned by the re-claim's fresh clone, so writing
//     it would lose the change.
//
// The step-status rule is re-checked under the actor (the one thing that
// can move between the route's validation and this turn): a step with a
// live dispatch is refused; a parked work item's frozen current step
// (in_progress, no process) accepts a model chain — the owner's
// change-before-wake window — while the skip switch stays for steps
// that never started.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// The overlay event kinds (one per actual change; a clear that found
// nothing records none).
const (
	EventOverrideSet   = "override_set"
	EventOverrideClear = "override_clear"
)

// The postures the write lands in (protocol.OverrideView.Posture's
// words).
const (
	OverridePostureRunning = "running"
	OverridePosturePaused  = "paused"
	OverridePostureBlocked = "blocked"
)

// OverrideChange is one overlay write as the daemon's route validated it:
// exactly one action, the candidate chain already parsed and normalized.
type OverrideChange struct {
	Step        string
	Operator    string
	Models      []string // non-nil = set the chain
	ClearModels bool
	Skip        *bool // non-nil = set the switch (true/false)
	// SealedDir is the sealed scene the route resolved for a work item
	// held by no job: a clear reads the chain its overlay carries.
	SealedDir string
}

// OverrideOutcome is what the actor turn did.
type OverrideOutcome struct {
	// Posture the write landed in: running, paused or blocked.
	Posture string
	// Changed is false when a clear found nothing (the idempotent
	// success: nothing written, no event).
	Changed bool
	// Published: the chain commit reached the server. A paused change
	// stays in the kept scene until the resume's own publishes carry it,
	// and a blocked change rides the daemon-local pending record into
	// the next claim's fresh capsule — neither is published.
	Published bool
	// Note explains an unpublished change (and anything else the caller
	// should surface).
	Note  string
	Entry *capsule.StepOverride
}

// ApplyStepOverride applies one overlay change to work item wi, one
// actor turn (the same per-work-item mailbox a claim or a resume runs
// on, so the write serializes with both). The route validates the
// change's shape, the step's declaration and (for the parked postures)
// the frozen scene; this turn re-checks the one thing that can move
// before its turn — the step's status in the live or kept scene.
func (r *Runtime) ApplyStepOverride(ctx context.Context, wi string, ch OverrideChange) (OverrideOutcome, error) {
	id := r.CanonicalID(wi)
	if id == "" {
		id = wi
	}
	var out OverrideOutcome
	err := r.call(ctx, id, func() error {
		j := r.jobOf(id)
		if j == nil {
			var err2 error
			out, err2 = r.applyOverrideBlocked(id, ch)
			return err2
		}
		var err2 error
		out, err2 = r.applyOverrideJob(j, ch)
		return err2
	})
	return out, err
}

// applyOverrideJob applies the change to a job the daemon holds, by its
// state.
func (r *Runtime) applyOverrideJob(j *job, ch OverrideChange) (OverrideOutcome, error) {
	j.mu.Lock()
	st, capDir := j.state, j.capsuleDir
	j.mu.Unlock()
	switch st {
	case StateRunning:
		// a live dispatch owns the current step: the overlay is for
		// steps it has not started
		ex := capsuleExecution(capDir)
		if stepLive(ex, ch.Step) {
			return OverrideOutcome{}, conflict("step %s has a live dispatch (its status is %s): the overlay is for steps without one — set it on a step that has not started, or while the work item is parked", ch.Step, ex.Steps[ch.Step].Status)
		}
		return r.applyOverrideScene(j, ch, OverridePostureRunning)
	case StatePaused:
		// the frozen current step (in_progress, no process) accepts a
		// model chain — the owner's change-before-wake window; the skip
		// switch stays for steps that never started
		ex := capsuleExecution(capDir)
		if ch.Skip != nil && *ch.Skip && ex.CurrentStep == ch.Step {
			return OverrideOutcome{}, conflict("step %s has started (its status is %s): the skip switch is for steps that never started", ch.Step, ex.Steps[ch.Step].Status)
		}
		return r.applyOverrideScene(j, ch, OverridePosturePaused)
	default:
		return OverrideOutcome{}, conflict("work item is %s here; try again in a moment", st)
	}
}

// stepLive reports whether the step's record shows a live dispatch: only
// the current step can be in_progress, and a running job's current step
// holds (or is about to hold) a dispatch.
func stepLive(ex capsule.Execution, step string) bool {
	st, ok := ex.Steps[step]
	return ok && st != nil && st.Status == capsule.StepInProgress
}

// applyOverrideScene writes the change into the live or paused job's
// capsule (the read-modify-write); a running job's chain is committed
// and pushed at once, a paused one's waits for the resume's publishes.
// The event comes last and says whether the change was published.
func (r *Runtime) applyOverrideScene(j *job, ch OverrideChange, posture string) (OverrideOutcome, error) {
	j.mu.Lock()
	capDir := j.capsuleDir
	git := j.git
	j.mu.Unlock()
	changed, entry, err := r.mutateOverlay(capDir, ch)
	if err != nil {
		return OverrideOutcome{Posture: posture}, err
	}
	if !changed {
		return OverrideOutcome{Posture: posture, Entry: entry}, nil
	}
	// the publish first, the event after: the event says whether the
	// change reached the chain (the event log is the daemon's, not the
	// capsule's — the commit carries nothing of it)
	out := OverrideOutcome{Posture: posture, Changed: true, Entry: entry, Published: true}
	if posture == OverridePosturePaused {
		out.Published = false
		out.Note = "recorded in the kept scene, not published: it governs this machine's resume, whose own publishes carry it into the capsule chain"
		r.tapOverride(j.wi, ch, posture, false)
		return out, nil
	}
	_, pushed, _, perr := gitops.CommitPush(context.Background(), git, "step override: "+ch.Step)
	if perr != nil {
		out.Published = false
		out.Note = fmt.Sprintf("the overlay is written and its commit stands locally, but the chain push failed: %v — the next publish retries it", perr)
	} else if !pushed {
		out.Published = false
		out.Note = "the overlay is written and committed; the push reported nothing sent"
	}
	if out.Published {
		// the chain now carries the claim's merge (committed into this
		// capsule at claim) and this write: a pending residue a failed
		// claim publish left is redundant, and a later re-claim must not
		// merge it over this newer change. A failed push keeps it (a
		// crash would re-claim from a chain lacking the merge).
		r.clearPendingOverrides(j.wi)
	}
	r.tapOverride(j.wi, ch, posture, out.Published)
	return out, nil
}

// applyOverrideBlocked records the change in the daemon-local pending
// record: the sealed scene of the last close is read-only and abandoned
// by the re-claim's fresh clone, so the change must ride the claim.
func (r *Runtime) applyOverrideBlocked(wi string, ch OverrideChange) (OverrideOutcome, error) {
	changed, entry, err := r.mutatePending(wi, ch)
	if err != nil {
		return OverrideOutcome{Posture: OverridePostureBlocked}, err
	}
	if !changed {
		return OverrideOutcome{Posture: OverridePostureBlocked, Entry: entry}, nil
	}
	r.tapOverride(wi, ch, OverridePostureBlocked, false)
	return OverrideOutcome{Posture: OverridePostureBlocked, Changed: true, Entry: entry,
		Published: false,
		Note:      "recorded locally; it lands with the next claim of the work item here (pf resume), which carries it into the fresh capsule"}, nil
}

// mutatePending is the pending record's read-modify-write of work item
// wi's entry for the change's step, under pendingMu (the record is
// daemon-global: other work items' actors write it too).
func (r *Runtime) mutatePending(wi string, ch OverrideChange) (bool, *capsule.StepOverride, error) {
	// a clear also reaches the chain the sealed capsule carries (read
	// before the lock: no I/O beyond the record's own under it)
	sealedChain := false
	if ch.ClearModels && ch.SealedDir != "" {
		ov, err := capsule.ReadStepOverrides(ch.SealedDir)
		if err != nil {
			return false, nil, conflict("the sealed capsule's overlay record does not read: %v", err)
		}
		sealedChain = ov.Steps[ch.Step] != nil && ov.Steps[ch.Step].Models != nil
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	entries, err := r.readPendingOverrides()
	if err != nil {
		return false, nil, err
	}
	cur := entries[wi]
	if cur == nil {
		cur = map[string]*capsule.StepOverride{}
	}
	changed, entry := applyAspect(cur, ch, sealedChain)
	if !changed {
		return false, entry, nil
	}
	entries[wi] = cur
	if err := r.writePendingOverrides(entries); err != nil {
		return false, nil, err
	}
	return true, entry, nil
}

// applyAspect applies the change's one aspect to the step's entry (nil =
// none yet), answering whether anything changed and the entry after.
// sealedChain (the pending record only) says the sealed capsule below
// carries a chain for the step: a clear then leaves the tombstone the
// claim merge applies, unless one already stands.
func applyAspect(entries map[string]*capsule.StepOverride, ch OverrideChange, sealedChain bool) (bool, *capsule.StepOverride) {
	e := entries[ch.Step]
	if e == nil {
		e = &capsule.StepOverride{}
		entries[ch.Step] = e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch {
	case ch.Models != nil:
		if equalCandidates(e.Models, ch.Models) {
			return false, e
		}
		e.Models = append([]string(nil), ch.Models...)
		e.ModelsBy, e.ModelsAt, e.ModelsCleared = ch.Operator, now, false
	case ch.ClearModels:
		if e.Models == nil && (!sealedChain || e.ModelsCleared) {
			return false, e
		}
		e.Models, e.ModelsBy, e.ModelsAt = nil, "", ""
		e.ModelsCleared = sealedChain
	case ch.Skip != nil:
		if e.Skip != nil && *e.Skip == *ch.Skip {
			return false, e
		}
		skip := *ch.Skip
		e.Skip = &skip
		e.SkipBy, e.SkipAt = ch.Operator, now
	}
	return true, e
}

// mutateOverlay is the capsule's read-modify-write: exactly one aspect
// of one step's entry. An unchanged clear answers changed=false and
// writes nothing.
func (r *Runtime) mutateOverlay(capDir string, ch OverrideChange) (bool, *capsule.StepOverride, error) {
	ov, err := capsule.ReadStepOverrides(capDir)
	if err != nil {
		return false, nil, conflict("the overlay record of this capsule does not read: %v", err)
	}
	changed, entry := applyAspect(ov.Steps, ch, false)
	if !changed {
		return false, entry, nil
	}
	if err := capsule.WriteStepOverrides(capDir, ov); err != nil {
		return false, nil, &api.Error{Code: api.ErrDaemonInternal, Detail: fmt.Sprintf("writing the overlay record failed (nothing was applied): %v", err)}
	}
	return true, entry, nil
}

// tapOverride records the change as one event: the operator, the step
// and the new value (the chain, or the skip switch's new state), and
// whether the change reached the capsule chain (published: false for a
// parked posture — the change governs only this machine until its wake
// or claim here publishes it — and for a failed push).
func (r *Runtime) tapOverride(wi string, ch OverrideChange, posture string, published bool) {
	kind, aspect, value := EventOverrideSet, "", ""
	switch {
	case ch.Models != nil:
		aspect, value = "models", strings.Join(ch.Models, ",")
	case ch.ClearModels:
		kind, aspect, value = EventOverrideClear, "models", "cleared"
	case ch.Skip != nil:
		aspect, value = "skip", fmt.Sprintf("%v", *ch.Skip)
		if !*ch.Skip {
			kind = EventOverrideClear
		}
	}
	r.tap(wi, kind, map[string]any{
		"step":      ch.Step,
		"aspect":    aspect,
		"value":     value,
		"operator":  ch.Operator,
		"posture":   posture,
		"published": published,
		"models":    ch.Models,
		"note":      "the step overlay changed; it governs the step's next dispatch",
	})
}

// equalCandidates reports whether two chains name the same candidates in
// order (the idempotence check of a repeated set).
func equalCandidates(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// The daemon-local pending record: work item id → per-step overlay
// entries recorded while the work item was held by no job, consumed by
// the next claim's scene preparation.

// pendingOverridesPath is the daemon-local pending record's path (the
// manual `--models` list's neighbour — a local operator choice, never in
// the capsule).
func (r *Runtime) pendingOverridesPath() string {
	return filepath.Join(r.cfg.DataDir, "pending-step-overrides.json")
}

// readPendingOverrides answers the whole pending record (an empty map
// when none); a torn file is a loud error, never silently overwritten.
// The caller holds pendingMu (as for writePendingOverrides).
func (r *Runtime) readPendingOverrides() (map[string]map[string]*capsule.StepOverride, error) {
	raw, err := os.ReadFile(r.pendingOverridesPath())
	if os.IsNotExist(err) {
		return map[string]map[string]*capsule.StepOverride{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]map[string]*capsule.StepOverride
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		if err == nil {
			err = errors.New("the record is not a JSON object")
		}
		return nil, fmt.Errorf("the pending step-override record does not parse (not overwriting it; fix or remove the file, then retry): %w", err)
	}
	return m, nil
}

// writePendingOverrides writes the whole pending record atomically; an
// empty record removes the file.
func (r *Runtime) writePendingOverrides(m map[string]map[string]*capsule.StepOverride) error {
	if len(m) == 0 {
		if err := os.Remove(r.pendingOverridesPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(r.pendingOverridesPath(), blob, 0o600)
}

// savePendingOverrides records work item wi's pending overlay entries.
func (r *Runtime) savePendingOverrides(wi string, entries map[string]*capsule.StepOverride) error {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	m, err := r.readPendingOverrides()
	if err != nil {
		return err
	}
	m[wi] = entries
	return r.writePendingOverrides(m)
}

// PendingStepOverrides answers the daemon-local pending overlay entries
// of work item wi (nil when none): the changes recorded while the work
// item was held by no job, waiting for its next claim here.
func (r *Runtime) PendingStepOverrides(wi string) map[string]*capsule.StepOverride {
	id := r.CanonicalID(wi)
	if id == "" {
		id = wi
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	m, err := r.readPendingOverrides()
	if err != nil {
		return nil
	}
	return m[id]
}

// ManualModels answers the work item's frozen manual candidate list
// (`pf submit --models`), nil when none — the daemon's read face for the
// overlay view's source labels.
func (r *Runtime) ManualModels(wi string) []string {
	return r.loadManualModels(wi)
}

// pendingPublishFn is the claim's publish of the merged pending record
// (a seam for the failed-publish test).
var pendingPublishFn = gitops.CommitPush

// writePendingStepOverrides is the claim's consumption of the pending
// record, intent before action: the fresh capsule's overlay file gains
// the pending entries (aspect merge over what the chain already
// carried), the capsule is published at once, and only that publish's
// success deletes the record's key. A failed publish — or a crash before
// it — keeps the record for the next claim, whose merge is idempotent
// (the claim goes on: the job's own publishes carry the merge). A record
// that cannot be read or merged fails the claim loudly (the operator's
// recorded change must not silently vanish). Only wi's own actor writes
// wi's key, so the record is not held locked across the publish.
func (r *Runtime) writePendingStepOverrides(ctx context.Context, j *job) error {
	entries := r.PendingStepOverrides(j.wi)
	if len(entries) == 0 {
		return nil
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		return fmt.Errorf("merging the recorded step overrides into the fresh capsule: %w", err)
	}
	merged := mergePendingAspects(ov, entries)
	if err := capsule.WriteStepOverrides(j.capsuleDir, merged); err != nil {
		return fmt.Errorf("writing the fresh capsule's step overrides: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, protocol.PushTimeout)
	defer cancel()
	if _, pushed, _, perr := pendingPublishFn(pctx, j.git, "step override: the pending record merged at claim"); perr != nil || !pushed {
		fmt.Fprintf(os.Stderr, "runtime: %s: the claim's publish of the merged step overrides failed (the pending record stays for the next claim; the job's publishes carry the merge): %v\n", j.wi, perr)
		return nil
	}
	r.clearPendingOverrides(j.wi)
	return nil
}

// mergePendingAspects merges pending entries over the capsule's overlay
// record, per aspect: a pending aspect replaces the capsule entry's same
// aspect and leaves the others (an older chain the capsule already
// carries survives a pending skip, and vice versa); a clear tombstone
// drops the capsule's chain.
func mergePendingAspects(ov capsule.StepOverrides, pending map[string]*capsule.StepOverride) capsule.StepOverrides {
	if ov.Steps == nil {
		ov.Steps = map[string]*capsule.StepOverride{}
	}
	for id, p := range pending {
		if p == nil {
			continue
		}
		e := ov.Steps[id]
		if e == nil {
			e = &capsule.StepOverride{}
			ov.Steps[id] = e
		}
		if p.Models != nil {
			e.Models, e.ModelsBy, e.ModelsAt = append([]string(nil), p.Models...), p.ModelsBy, p.ModelsAt
		}
		if p.ModelsCleared {
			e.Models, e.ModelsBy, e.ModelsAt = nil, "", ""
		}
		if p.Skip != nil {
			skip := *p.Skip
			e.Skip, e.SkipBy, e.SkipAt = &skip, p.SkipBy, p.SkipAt
		}
	}
	return ov
}

// clearPendingOverrides drops work item wi's pending entries (a terminal
// close, or the claim whose publish carried them; a blocked close keeps
// them for the re-claim).
func (r *Runtime) clearPendingOverrides(wi string) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	m, err := r.readPendingOverrides()
	if err != nil {
		return
	}
	if _, ok := m[wi]; !ok {
		return
	}
	delete(m, wi)
	if err := r.writePendingOverrides(m); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: pending step-override record cleanup failed: %v\n", wi, err)
	}
}
