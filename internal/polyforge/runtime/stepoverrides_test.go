package runtime

// stepoverrides_test.go (u4, polyforge#25): the daemon-local pending
// overlay record — the changes recorded while the work item was held by
// no job (its blocked capsule sealed read-only, abandoned by the next
// claim's fresh clone) merge into the fresh capsule's overlay file, per
// aspect, when the claim materializes the scene, and the record is
// consumed; a terminal close clears an unconsumed record, a blocked
// close keeps it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// pendingEnv builds a runtime over the demo bundle (workspace_compose_test
// harness) and answers its data dir.
func pendingEnv(t *testing.T) (*Runtime, string) {
	t.Helper()
	setGitIdentity(t)
	wfDir := laneBundle(t, w079Lane)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	return r, ws
}

func readPendingFile(t *testing.T, r *Runtime) map[string]map[string]*capsule.StepOverride {
	t.Helper()
	raw, err := os.ReadFile(r.pendingOverridesPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]*capsule.StepOverride
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("the pending record does not parse: %v", err)
	}
	return m
}

// The claim materializes the pending record into the fresh capsule's
// overlay file (aspect merge over what the chain already carries) and
// consumes it.
func TestPendingOverrideMaterializesAtClaim(t *testing.T) {
	r, _ := pendingEnv(t)
	yes := true
	if err := r.savePendingOverrides("wi_ov4m", map[string]*capsule.StepOverride{
		"fix":     {Models: []string{"pi:ov/pending-a"}, ModelsBy: "usr_1"},
		"release": {Skip: &yes, SkipBy: "usr_1"},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if err := r.Start(context.Background(), "wi_ov4m", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_ov4m")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if m := ov.Steps["fix"]; m == nil || len(m.Models) != 1 || m.Models[0] != "pi:ov/pending-a" || m.ModelsBy != "usr_1" {
		t.Fatalf("the materialized fix entry: %+v", m)
	}
	if s := ov.Steps["release"]; s == nil || s.Skip == nil || !*s.Skip {
		t.Fatalf("the materialized release entry: %+v", s)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4m"] != nil {
		t.Fatalf("the claim must consume the pending record: %+v", pend["wi_ov4m"])
	}
	if err := r.Stop(context.Background(), "wi_ov4m", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// mergePendingAspects: a pending entry's aspects replace the capsule
// entry's same aspects and leave the others (an older chain the capsule
// already carries survives a pending skip, and vice versa).
func TestMergePendingAspects(t *testing.T) {
	yes := true
	no := false
	capsuleOv := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"fix":     {Models: []string{"pi:ov/old-chain"}, ModelsBy: "usr_0", Skip: &no, SkipBy: "usr_0"},
		"review":  {Models: []string{"pi:ov/review-old"}},
		"release": {},
	}}
	pending := map[string]*capsule.StepOverride{
		"fix":     {Skip: &yes, SkipBy: "usr_1"}, // the skip lands, the old chain stays
		"review":  {Models: []string{"pi:ov/review-new"}, ModelsBy: "usr_1"},
		"release": {Skip: &yes, SkipBy: "usr_1"},
	}
	out := mergePendingAspects(capsuleOv, pending)
	if m := out.Steps["fix"]; m == nil || len(m.Models) != 1 || m.Models[0] != "pi:ov/old-chain" || m.Skip == nil || !*m.Skip || m.SkipBy != "usr_1" || m.ModelsBy != "usr_0" {
		t.Fatalf("fix after the merge: %+v (the pending skip replaces only the skip aspect)", m)
	}
	if rv := out.Steps["review"]; rv == nil || len(rv.Models) != 1 || rv.Models[0] != "pi:ov/review-new" {
		t.Fatalf("review after the merge: %+v", rv)
	}
	if _, ok := out.Steps["release"]; !ok {
		t.Fatal("an entry that gains an aspect from the pending record must stay")
	}
}

// A terminal close clears an unconsumed pending record; a blocked close
// keeps it (the re-claim reads it back).
func TestTerminalCloseClearsPendingRecord(t *testing.T) {
	r, _ := pendingEnv(t)
	if err := r.Start(context.Background(), "wi_ov4n", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.savePendingOverrides("wi_ov4n", map[string]*capsule.StepOverride{
		"fix": {Models: []string{"pi:ov/x"}},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	// a blocked close keeps the record (the re-claim consumes it)
	if err := r.Stop(context.Background(), "wi_ov4n", protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked Stop: %v", err)
	}
	if pend := readPendingFile(t, r); pend == nil || pend["wi_ov4n"] == nil {
		t.Fatal("a blocked close must keep the pending record")
	}
	// re-claim over the chain consumes it
	if err := r.Start(context.Background(), "wi_ov4n", StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4n"] != nil {
		t.Fatalf("the re-claim must consume the pending record: %+v", pend["wi_ov4n"])
	}
	// a terminal close clears it
	if err := r.savePendingOverrides("wi_ov4n", map[string]*capsule.StepOverride{
		"fix": {Models: []string{"pi:ov/y"}},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_ov4n", protocol.FinishWrapped); err != nil {
		t.Fatalf("terminal Stop: %v", err)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4n"] != nil {
		t.Fatalf("a terminal close must clear the pending record: %+v", pend["wi_ov4n"])
	}
}

// The pending record is one daemon-global file that different work
// items' actors read-modify-write: concurrent blocked writes of many
// work items must each survive (r1#4 — no write may lose another's
// entry).
func TestPendingOverrideConcurrentWritersKeepEveryEntry(t *testing.T) {
	r, _ := pendingEnv(t)
	const n = 48
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wi := fmt.Sprintf("wi_ov10_%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
				Step: "fix", Operator: "usr_1", Models: []string{"pi:ov/" + wi},
			})
			if err == nil && (!out.Changed || out.Posture != OverridePostureBlocked) {
				err = fmt.Errorf("%s: outcome %+v, want a changed blocked write", wi, out)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent blocked write failed: %v", err)
		}
	}
	pend := readPendingFile(t, r)
	lost := 0
	for i := 0; i < n; i++ {
		wi := fmt.Sprintf("wi_ov10_%02d", i)
		if e := pend[wi]["fix"]; e == nil || len(e.Models) != 1 || e.Models[0] != "pi:ov/"+wi {
			lost++
		}
	}
	if lost != 0 {
		t.Fatalf("%d of %d concurrent blocked writes lost their pending entry (the record's read-modify-write must be serialized)", lost, n)
	}
}

// A clear recorded while the work item is held by no job, of a chain its
// sealed capsule already carries, is a change: the pending record keeps
// a clear tombstone and the next claim's merge drops the chain the
// fresh capsule cloned (r1#5 — the clear restores the source below in
// the blocked posture too). A sibling chain the clear did not name
// survives the merge.
func TestPendingOverrideClearTombstoneDropsCapsuleChain(t *testing.T) {
	r, _ := pendingEnv(t)
	const wi = "wi_ov11"
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	capDir := j.capsuleDir
	// the chains the running posture published into the capsule
	if err := capsule.WriteStepOverrides(capDir, capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"review": {Models: []string{"pi:ov/review-chain"}, ModelsBy: "usr_0"},
		"fix":    {Models: []string{"pi:ov/fix-chain"}, ModelsBy: "usr_0"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked Stop: %v", err)
	}
	out, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
		Step: "review", Operator: "usr_1", ClearModels: true, SealedDir: capDir,
	})
	if err != nil {
		t.Fatalf("the blocked clear: %v", err)
	}
	if !out.Changed || out.Posture != OverridePostureBlocked {
		t.Fatalf("clearing the chain the sealed capsule carries must be a blocked change: %+v", out)
	}
	if p := r.PendingStepOverrides(wi)["review"]; p == nil || !p.ModelsCleared || p.Models != nil {
		t.Fatalf("the pending record must keep the clear tombstone: %+v", p)
	}
	// a repeated clear is the idempotent no-op
	if again, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
		Step: "review", Operator: "usr_1", ClearModels: true, SealedDir: capDir,
	}); err != nil || again.Changed {
		t.Fatalf("a repeated blocked clear must change nothing: %+v %v", again, err)
	}
	// the re-claim merges the tombstone: the cloned chain goes, fix's stays
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	j = r.jobOf(wi)
	if j == nil {
		t.Fatal("the re-claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if e := ov.Steps["review"]; e != nil && e.Models != nil {
		t.Fatalf("the fresh capsule still carries the cleared review chain: %+v", e)
	}
	if e := ov.Steps["fix"]; e == nil || len(e.Models) != 1 || e.Models[0] != "pi:ov/fix-chain" {
		t.Fatalf("the chain the clear did not name must survive the merge: %+v", e)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// The claim merges the pending record into the fresh capsule first and
// deletes it only after that capsule's first publish succeeded (the
// owner ruling of 2026-11-15, row 2): a publish that fails — or a crash
// before it — leaves the record for the next claim, whose merge is
// idempotent.
func TestPendingOverrideKeptUntilFirstPublish(t *testing.T) {
	r, _ := pendingEnv(t)
	const wi = "wi_ov14"
	if err := r.savePendingOverrides(wi, map[string]*capsule.StepOverride{
		"fix": {Models: []string{"pi:ov/kept"}, ModelsBy: "usr_1"},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	real := pendingPublishFn
	t.Cleanup(func() { pendingPublishFn = real })
	// the first claim's publish fails
	pendingPublishFn = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
		return "", false, gitops.PushTimings{}, errors.New("injected: the hub is unreachable")
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("a failed publish of the merged record must not fail the claim")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := ov.Steps["fix"]; e == nil || len(e.Models) != 1 || e.Models[0] != "pi:ov/kept" {
		t.Fatalf("the claim must merge the pending record first: %+v", e)
	}
	if pend := readPendingFile(t, r); pend == nil || pend[wi] == nil {
		t.Fatal("the pending record must survive a claim whose first publish failed")
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked Stop: %v", err)
	}
	// the next claim merges it again (idempotent) and, its publish
	// succeeding, deletes it
	published := false
	pendingPublishFn = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
		sha, pushed, tm, err := real(ctx, o, msg)
		published = err == nil && pushed
		return sha, pushed, tm, err
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	j = r.jobOf(wi)
	if j == nil {
		t.Fatal("the re-claim must run to a live agent")
	}
	if !published {
		t.Fatal("the re-claim must publish the merged capsule before deleting the record")
	}
	ov, err = capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := ov.Steps["fix"]; e == nil || len(e.Models) != 1 || e.Models[0] != "pi:ov/kept" {
		t.Fatalf("the re-claim's merge: %+v", e)
	}
	if pend := readPendingFile(t, r); pend != nil && pend[wi] != nil {
		t.Fatalf("a published claim must delete the record: %+v", pend[wi])
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A pending entry left by a claim whose publish failed is residue the
// running job never drops by itself; a running-posture write whose chain
// push succeeds carries the claim's merge (committed into this capsule at
// claim) and the new change, so it drops the record — a later block and
// re-claim here must not merge the stale value over the owner's newer
// change (polyforge#25 u16, review #4/#5).
func TestPendingOverrideResidueDroppedByPublishedRunningWrite(t *testing.T) {
	r, _ := pendingEnv(t)
	const wi = "wi_ov16"
	if err := r.savePendingOverrides(wi, map[string]*capsule.StepOverride{
		"release": {Models: []string{"pi:ov/stale"}, ModelsBy: "usr_1"},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	real := pendingPublishFn
	t.Cleanup(func() { pendingPublishFn = real })
	// the claim's publish of the merged record fails: the record stays
	pendingPublishFn = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
		return "", false, gitops.PushTimings{}, errors.New("injected: the hub is unreachable")
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.jobOf(wi) == nil {
		t.Fatal("the claim must run to a live agent")
	}
	if pend := readPendingFile(t, r); pend == nil || pend[wi] == nil {
		t.Fatal("precondition: the failed claim publish must leave the pending record")
	}
	pendingPublishFn = real
	// the owner's newer change of the same aspect of the same step, while
	// the job runs; its chain push succeeds
	out, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
		Step: "release", Operator: "usr_2", Models: []string{"pi:ov/newer"},
	})
	if err != nil {
		t.Fatalf("the running write: %v", err)
	}
	if !out.Changed || out.Posture != OverridePostureRunning || !out.Published {
		t.Fatalf("precondition: a changed, published running write: %+v", out)
	}
	if pend := readPendingFile(t, r); pend != nil && pend[wi] != nil {
		t.Fatalf("a published running write must drop the pending residue: %+v", pend[wi])
	}
	// the ticket blocks and is re-claimed here: the newer chain stands
	if err := r.Stop(context.Background(), wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked Stop: %v", err)
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the re-claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatal(err)
	}
	if e := ov.Steps["release"]; e == nil || len(e.Models) != 1 || e.Models[0] != "pi:ov/newer" || e.ModelsBy != "usr_2" {
		t.Fatalf("the re-claim merged the stale pending value over the newer chain: %+v", e)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
