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
//     capsule's overlay file, per aspect, the newer time winning — the
//     sealed scene of the last close is read-only and abandoned by the
//     re-claim's fresh clone, so writing it would lose the change.
//
// The step-status rule is re-checked under the actor (the one thing that
// can move between the route's validation and this turn): a step with a
// live dispatch is refused; a parked work item's frozen current step
// (in_progress, no process) accepts a model chain — the owner's
// change-before-wake window — while the skip switch stays for steps
// that never started. A step param (polyforge#78) follows the model
// chain's rule: one param per change, each with its own operator and
// time, a clear kept as a tombstone so no older value comes back.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// The overlay event kinds (one per actual change; a clear that found
// nothing records none).
const (
	EventOverrideSet   = "override_set"
	EventOverrideClear = "override_clear"
	// EventStepTierFallback is the dispatch-time composition's diagnostic
	// (polyforge#81 Q12 A): an overlay tier the Host [step_tiers] catalog
	// does not declare (or declares empty) fell through to the next layer.
	// One event per composition that carries a fallback, scope
	// "composition" — never "this step's dispatch already fell back".
	EventStepTierFallback = "step_tier_fallback"
	// EventStepTierDropped is the claim-time diagnostic (polyforge#81
	// Q14 A): a queued tier named a step the claim's lane snapshot no
	// longer declares, so it was not merged into the fresh capsule's
	// overlay. One event names the work item, the lane, the dropped steps
	// and their tiers; the claim goes on.
	EventStepTierDropped = "step_tier_dropped"
)

// The postures the write lands in (protocol.OverrideView.Posture's
// words). The queued posture (polyforge#79) is a work item no daemon
// holds: its per-ticket params ride the reserved step_params attrs key,
// and the write face there is the daemon's machine-signed attrs route
// (never a capsule).
const (
	OverridePostureRunning = "running"
	OverridePosturePaused  = "paused"
	OverridePostureBlocked = "blocked"
	OverridePostureQueued  = "queued"
)

// StepParamAttr is one param's entry in the reserved step_params attrs
// key (polyforge#79): the value the queued write set, with the time it
// was written. The claim merges by that time (mergePendingAspects), so a
// residue from before an earlier claim loses to the newer running writes
// the capsule chain carries, and a value written after a re-queue wins —
// neither may be dropped by the re-claim path (review r2#1).
type StepParamAttr struct {
	Value string `json:"value"`
	At    string `json:"at"`
}

// StepParamsOfAttrs answers the per-ticket step params an attrs map
// carries under api.StepParamsKey — {"<step-id>": {"<param>": {"value":
// "<value>", "at": "<RFC 3339 write time>"}}}, map[step]map[param]entry —
// tolerating anything else: a non-object key, a step entry that is no
// object, an entry that is no map or whose value is missing, null or not
// a string, an empty step or param name are all dropped — never kept as
// an empty value (the attrs key is a machine-written boundary whose
// entries were judged against a lane at write time; the claim re-validates
// every kept entry against its own lane snapshot, and a planted or
// drifted shape never blocks a claim). An entry without a parseable time
// keeps an empty At — the merge counts it older than any timed capsule
// aspect (pendingAspectApplies). A key that carries nothing usable
// answers nil.
func StepParamsOfAttrs(attrs map[string]any) map[string]map[string]StepParamAttr {
	raw, ok := attrs[api.StepParamsKey]
	if !ok {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]map[string]StepParamAttr{}
	for step, sv := range obj {
		if step == "" {
			continue
		}
		so, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		for name, v := range so {
			if name == "" {
				continue
			}
			// the key's ONE writer (the daemon's queued write) produces
			// one entry object per param; anything else — a bare string
			// (a shape only this change's intermediate commits wrote,
			// never shipped: no compat path), an entry that is no map —
			// is junk
			e, ok := v.(map[string]any)
			if !ok {
				continue
			}
			value, ok := e["value"].(string)
			if !ok {
				// a missing, null or non-string value is an entry without
				// a usable value: dropped, never kept as ""
				continue
			}
			at, _ := e["at"].(string)
			if strings.Contains(value, "\n") {
				continue
			}
			if out[step] == nil {
				out[step] = map[string]StepParamAttr{}
			}
			out[step][name] = StepParamAttr{Value: value, At: at}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// overridePushTimeout bounds the running posture's chain push (the same
// deadline the claim's pending-record publish uses, protocol.PushTimeout;
// a var so a test can shrink it). A push that outlives it gives up: the
// overlay's commit stands locally and the next publish retries it — the
// same failed-push shape a refused push takes (polyforge#47 AC3).
var overridePushTimeout time.Duration = protocol.PushTimeout

// OverrideChange is one overlay write as the daemon's route validated it:
// exactly one action, the candidate chain already parsed and normalized.
type OverrideChange struct {
	Step        string
	Operator    string
	Models      []string // non-nil = set the chain
	ClearModels bool
	Skip        *bool // non-nil = set the switch (true/false)
	// Param (non-nil) sets one declared step param; ClearParam names one
	// to clear.
	Param      *protocol.OverrideParam
	ClearParam string
	// Tier sets the step's capability-tier name (polyforge#81); ClearTier
	// clears it.
	Tier      string
	ClearTier bool
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
		out, err2 = r.applyOverrideJob(ctx, j, ch)
		return err2
	})
	return out, err
}

// applyOverrideJob applies the change to a job the daemon holds, by its
// state.
func (r *Runtime) applyOverrideJob(ctx context.Context, j *job, ch OverrideChange) (OverrideOutcome, error) {
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
		return r.applyOverrideScene(ctx, j, ch, OverridePostureRunning)
	case StatePaused:
		// the frozen current step (in_progress, no process) accepts a
		// model chain — the owner's change-before-wake window; the skip
		// switch stays for steps that never started
		ex := capsuleExecution(capDir)
		if ch.Skip != nil && *ch.Skip && ex.CurrentStep == ch.Step {
			return OverrideOutcome{}, conflict("step %s has started (its status is %s): the skip switch is for steps that never started", ch.Step, ex.Steps[ch.Step].Status)
		}
		return r.applyOverrideScene(ctx, j, ch, OverridePosturePaused)
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
func (r *Runtime) applyOverrideScene(ctx context.Context, j *job, ch OverrideChange, posture string) (OverrideOutcome, error) {
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
	// the chain push is bounded by the pending write's deadline
	// (protocol.PushTimeout): a push that outlives it gives up — the
	// overlay's commit stands locally and the next publish retries it —
	// so a stuck remote never pins the work item's actor (the pause and
	// wrap closes wait on it, polyforge#47 AC3).
	pctx, cancel := context.WithTimeout(ctx, overridePushTimeout)
	defer cancel()
	_, pushed, _, perr := gitops.CommitPush(pctx, git, "step override: "+ch.Step)
	if perr != nil {
		out.Published = false
		out.Note = fmt.Sprintf("the overlay is written and its commit stands locally, but the chain push failed: %v — the next publish retries it", perr)
	} else if !pushed {
		out.Published = false
		out.Note = "the overlay is written and committed; the push reported nothing sent"
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
	// a clear also reaches the chain or param the sealed capsule carries
	// (read before the lock: no I/O beyond the record's own under it)
	sealed := false
	if (ch.ClearModels || ch.ClearParam != "") && ch.SealedDir != "" {
		ov, err := capsule.ReadStepOverrides(ch.SealedDir)
		if err != nil {
			return false, nil, conflict("the sealed capsule's overlay record does not read: %v", err)
		}
		if e := ov.Steps[ch.Step]; e != nil {
			if ch.ClearModels {
				sealed = e.Models != nil
			} else {
				p := e.Params[ch.ClearParam]
				sealed = p != nil && !p.Cleared
			}
		}
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
	changed, entry := applyAspectAt(cur, ch, true, sealed)
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
// Every change records its operator and time — a clear too: it leaves
// the models aspect with no chain and the clear's ModelsBy/ModelsAt, so
// the claim's merge (mergePendingAspects) can tell it from an older
// chain. A param keeps the same history per name: a set records its
// value, operator and time; a clear leaves the param's tombstone
// (Cleared, with the clear's operator and time) — in the capsule's record
// and the pending one alike, since an empty value is a legal set. sealed
// (the pending record only) says the sealed capsule below carries the
// cleared chain or param for the step: a clear then leaves the tombstone
// the claim merge applies, unless one already stands.
func applyAspect(entries map[string]*capsule.StepOverride, ch OverrideChange, sealed bool) (bool, *capsule.StepOverride) {
	return applyAspectAt(entries, ch, false, sealed)
}

// applyAspectAt is applyAspect naming whether the entries are the
// daemon-local pending record (a tier clear then keeps the tombstone the
// claim's merge applies).
func applyAspectAt(entries map[string]*capsule.StepOverride, ch OverrideChange, pendingRecord, sealed bool) (bool, *capsule.StepOverride) {
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
		if e.Models == nil && (!sealed || e.ModelsCleared) {
			return false, e
		}
		e.Models, e.ModelsBy, e.ModelsAt = nil, ch.Operator, now
		e.ModelsCleared = sealed
	case ch.Tier != "":
		if e.Tier == ch.Tier && !e.TierCleared {
			return false, e
		}
		e.Tier, e.TierBy, e.TierAt, e.TierCleared = ch.Tier, ch.Operator, now, false
	case ch.ClearTier:
		if e.Tier == "" && (!sealed || e.TierCleared) {
			return false, e
		}
		e.Tier, e.TierBy, e.TierAt = "", ch.Operator, now
		e.TierCleared = pendingRecord
	case ch.Skip != nil:
		if e.Skip != nil && *e.Skip == *ch.Skip {
			return false, e
		}
		skip := *ch.Skip
		e.Skip = &skip
		e.SkipBy, e.SkipAt = ch.Operator, now
	case ch.Param != nil:
		if p := e.Params[ch.Param.Name]; p != nil && !p.Cleared && p.Value == ch.Param.Value {
			return false, e
		}
		if e.Params == nil {
			e.Params = map[string]*capsule.ParamOverride{}
		}
		e.Params[ch.Param.Name] = &capsule.ParamOverride{Value: ch.Param.Value, By: ch.Operator, At: now}
	case ch.ClearParam != "":
		p := e.Params[ch.ClearParam]
		if (p == nil && !sealed) || (p != nil && p.Cleared) {
			return false, e
		}
		if e.Params == nil {
			e.Params = map[string]*capsule.ParamOverride{}
		}
		e.Params[ch.ClearParam] = &capsule.ParamOverride{By: ch.Operator, At: now, Cleared: true}
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
// and the new value (the chain, the skip switch's new state, or the
// param's name and value), and
// whether the change reached the capsule chain (published: false for a
// parked posture — the change governs only this machine until its wake
// or claim here publishes it — and for a failed push).
func (r *Runtime) tapOverride(wi string, ch OverrideChange, posture string, published bool) {
	kind, aspect, value, param := EventOverrideSet, "", "", ""
	switch {
	case ch.Models != nil:
		aspect, value = "models", strings.Join(ch.Models, ",")
	case ch.ClearModels:
		kind, aspect, value = EventOverrideClear, "models", "cleared"
	case ch.Tier != "":
		aspect, value = "tier", ch.Tier
	case ch.ClearTier:
		kind, aspect, value = EventOverrideClear, "tier", "cleared"
	case ch.Skip != nil:
		aspect, value = "skip", fmt.Sprintf("%v", *ch.Skip)
		if !*ch.Skip {
			kind = EventOverrideClear
		}
	case ch.Param != nil:
		aspect, param, value = "param", ch.Param.Name, ch.Param.Value
	case ch.ClearParam != "":
		kind, aspect, param, value = EventOverrideClear, "param", ch.ClearParam, "cleared"
	}
	body := map[string]any{
		"step":      ch.Step,
		"aspect":    aspect,
		"value":     value,
		"operator":  ch.Operator,
		"posture":   posture,
		"published": published,
		"models":    ch.Models,
		"note":      "the step overlay changed; it governs the step's next dispatch",
	}
	if param != "" {
		body["param"] = param
	}
	if ch.Tier != "" {
		body["tier"] = ch.Tier
	}
	r.tap(wi, kind, body)
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

// StepTiersCatalog answers the Host's live [step_tiers] catalog
// (polyforge#81), nil when no catalog is configured: the dispatch-time
// composition expands an overlay tier through it, and the override
// route validates a written tier name against it.
func (r *Runtime) StepTiersCatalog() map[string][]string {
	if r.cfg.StepTiers == nil {
		return nil
	}
	return r.cfg.StepTiers()
}

// pendingPublishFn is the claim's publish of the merged pending record
// (a seam for the failed-publish test).
var pendingPublishFn = gitops.CommitPush

// writePendingStepOverrides is the claim's consumption of the pending
// record, intent before action: the fresh capsule's overlay file gains
// the pending entries (aspect merge over what the chain already
// carried), the capsule is published at once, and only that publish's
// success deletes the record's key. A failed publish — or a crash before
// it — keeps the record for the next claim (the claim goes on: the job's
// own publishes carry the merge). That residue is merged again then, per
// aspect by time (mergePendingAspects), so a later write the capsule
// chain carries of the same aspect — a running or paused write, a clear —
// is never undone by the stale pending value (the owner ruling of
// 2026-11-15T01:37:30Z, review r2#1). A record
// that cannot be read or merged fails the claim loudly (the operator's
// recorded change must not silently vanish). Only wi's own actor writes
// wi's key, so the record is not held locked across the publish.
func (r *Runtime) writePendingStepOverrides(ctx context.Context, j *job) error {
	r.pendingMu.Lock()
	all, err := r.readPendingOverrides()
	r.pendingMu.Unlock()
	if err != nil {
		return fmt.Errorf("reading the recorded step overrides of %s: %w", j.wi, err)
	}
	entries := all[j.wi]
	if len(entries) == 0 {
		return nil
	}
	// polyforge#81 Q14 A: the claim-time lane snapshot re-judges the
	// pending tiers. A tier whose step the fresh lane no longer declares
	// cannot dispatch, so it never enters the overlay; one visible
	// diagnosis asks the operator to set it again on the running lane.
	// The claim goes on and the other aspects are untouched.
	var dropped bool
	entries, dropped = r.revalidatePendingTiers(j, entries)
	if dropped {
		r.persistPendingOverrides(j.wi, entries)
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

// revalidatePendingTiers drops every pending tier whose step the claim's
// fresh lane snapshot does not declare (polyforge#81 Q14 A) and records
// one step_tier_dropped diagnosis naming the work item, the lane, the
// steps and their tiers. A lane snapshot that cannot be read at all drops
// the pending tiers for the same reason (they cannot be judged) and the
// diagnosis says so. It answers the filtered entries and whether anything
// was dropped; models, skip and params are never touched.
func (r *Runtime) revalidatePendingTiers(j *job, entries map[string]*capsule.StepOverride) (map[string]*capsule.StepOverride, bool) {
	hasTier := false
	for _, e := range entries {
		if e != nil && (e.Tier != "" || e.TierCleared) {
			hasTier = true
			break
		}
	}
	if !hasTier {
		return entries, false
	}
	lane, errs := workflow.Load(filepath.Join(j.capsuleDir, capsule.DirWorkflows, j.bundleName))
	byID := map[string]bool{}
	if lane != nil {
		for i := range lane.Steps {
			byID[lane.Steps[i].ID] = true
		}
	}
	var droppedSteps []map[string]any
	for step, e := range entries {
		if e == nil || (e.Tier == "" && !e.TierCleared) {
			continue
		}
		if lane != nil && byID[step] {
			continue
		}
		droppedSteps = append(droppedSteps, map[string]any{"step": step, "tier": e.Tier})
		e.Tier, e.TierBy, e.TierAt, e.TierCleared = "", "", "", false
	}
	if len(droppedSteps) == 0 {
		return entries, false
	}
	sort.Slice(droppedSteps, func(a, b int) bool {
		return droppedSteps[a]["step"].(string) < droppedSteps[b]["step"].(string)
	})
	reason := "step_missing"
	if lane == nil {
		reason = "lane_unreadable"
	}
	r.tap(j.wi, EventStepTierDropped, map[string]any{
		"work_item":   j.wi,
		"lane":        j.bundleName,
		"reason":      reason,
		"steps":       droppedSteps,
		"lane_errors": fmt.Sprint(errs),
		"note":        "a queued capability tier was not merged: the claim-time lane snapshot does not declare its step; set the tier again on a step of the lane the ticket now runs",
	})
	return entries, true
}

// persistPendingOverrides rewrites the daemon-local pending record's entry
// for wi (deleting it when nothing is left); a failed write is reported
// and left for the next claim's judgment.
func (r *Runtime) persistPendingOverrides(wi string, entries map[string]*capsule.StepOverride) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	m, err := r.readPendingOverrides()
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: re-reading the pending overlay record to drop an invalid tier failed (it is re-judged at the next claim): %v\n", wi, err)
		return
	}
	if len(entries) == 0 {
		delete(m, wi)
	} else {
		m[wi] = entries
	}
	if err := r.writePendingOverrides(m); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: persisting the pending overlay record without an invalid tier failed (it is re-judged at the next claim): %v\n", wi, err)
	}
}

// mergePendingAspects merges pending entries over the capsule's overlay
// record, per aspect (models, skip, each param by name), by time: a pending aspect replaces
// the capsule entry's same aspect only when its time is not earlier than
// the capsule's (pendingAspectApplies), and leaves the other aspect (an
// older chain the capsule already carries survives a pending skip, and
// vice versa). A pending clear tombstone (ModelsCleared) carries its time
// like a chain: applied, it leaves the capsule entry with no chain and the
// clear's operator and time (never the tombstone flag, which is the
// pending record's alone), so a later merge of an older chain cannot
// resurrect it. A param's clear is its tombstone in both records (an
// empty value is a legal set), merged by its time like a value.
func mergePendingAspects(ov capsule.StepOverrides, pending map[string]*capsule.StepOverride) capsule.StepOverrides {
	merged, _ := mergePendingMarked(ov, pending)
	return merged
}

// PendingAspects says which aspects of a step's merged overlay entry the
// pending record supplied (the claim's merge applied them over the
// capsule's).
type PendingAspects struct {
	Models bool
	Tier   bool
	Skip   bool
	Params bool // some param of the step
}

// MergedStepOverrides answers the overlay the work item's next claim here
// would carry: a copy of the capsule's record ov (ov itself is not
// written) with the pending entries merged by the claim's own rule
// (mergePendingAspects — a pending aspect older than the capsule's same
// aspect does not apply), and per step which aspects came from pending
// (only steps with an applied aspect are keyed). The read face of the
// overrides view and the watch snapshot (polyforge#25 u20, AC8): they show
// what will govern, never a residue the re-claim will discard.
func MergedStepOverrides(ov capsule.StepOverrides, pending map[string]*capsule.StepOverride) (capsule.StepOverrides, map[string]PendingAspects) {
	cp := capsule.StepOverrides{Steps: make(map[string]*capsule.StepOverride, len(ov.Steps))}
	for id, e := range ov.Steps {
		if e == nil {
			continue
		}
		c := *e
		if e.Params != nil {
			c.Params = make(map[string]*capsule.ParamOverride, len(e.Params))
			for name, p := range e.Params {
				if p != nil {
					pc := *p
					c.Params[name] = &pc
				}
			}
		}
		cp.Steps[id] = &c
	}
	return mergePendingMarked(cp, pending)
}

// mergePendingMarked is mergePendingAspects answering, per step, which
// aspects the pending record supplied.
func mergePendingMarked(ov capsule.StepOverrides, pending map[string]*capsule.StepOverride) (capsule.StepOverrides, map[string]PendingAspects) {
	applied := map[string]PendingAspects{}
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
		var a PendingAspects
		if (p.Models != nil || p.ModelsCleared) && pendingAspectApplies(p.ModelsAt, e.ModelsAt) {
			if p.ModelsCleared {
				e.Models, e.ModelsBy, e.ModelsAt = nil, p.ModelsBy, p.ModelsAt
			} else {
				e.Models, e.ModelsBy, e.ModelsAt = append([]string(nil), p.Models...), p.ModelsBy, p.ModelsAt
			}
			a.Models = true
		}
		if (p.Tier != "" || p.TierCleared) && pendingAspectApplies(p.TierAt, e.TierAt) {
			if p.TierCleared {
				e.Tier, e.TierBy, e.TierAt = "", p.TierBy, p.TierAt
			} else {
				e.Tier, e.TierBy, e.TierAt = p.Tier, p.TierBy, p.TierAt
			}
			a.Tier = true
		}
		if p.Skip != nil && pendingAspectApplies(p.SkipAt, e.SkipAt) {
			skip := *p.Skip
			e.Skip, e.SkipBy, e.SkipAt = &skip, p.SkipBy, p.SkipAt
			a.Skip = true
		}
		for name, pp := range p.Params {
			if pp == nil {
				continue
			}
			capAt := ""
			if cur := e.Params[name]; cur != nil {
				capAt = cur.At
			}
			if !pendingAspectApplies(pp.At, capAt) {
				continue
			}
			if e.Params == nil {
				e.Params = map[string]*capsule.ParamOverride{}
			}
			pc := *pp
			e.Params[name] = &pc
			a.Params = true
		}
		if a.Models || a.Tier || a.Skip || a.Params {
			applied[id] = a
		}
	}
	return ov, applied
}

// pendingAspectApplies compares one aspect's recorded times, parsed
// (RFC 3339, never as strings): the pending aspect applies unless it is
// earlier than the capsule's. A capsule aspect with no or an unparseable
// time counts as older (the pending one applies); a pending aspect with
// no or an unparseable time — every pending write records one, so only a
// hand-edited record lacks it — counts as older than any timed capsule
// aspect (the capsule's time is the evidence of a later write).
func pendingAspectApplies(pendingAt, capsuleAt string) bool {
	c, err := time.Parse(time.RFC3339Nano, capsuleAt)
	if err != nil {
		return true
	}
	p, err := time.Parse(time.RFC3339Nano, pendingAt)
	if err != nil {
		return false
	}
	return !p.Before(c)
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
