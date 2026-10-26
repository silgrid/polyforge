package test

// submit_placement_acceptance_test.go: the e2e
// acceptance suite — authored against the SPEC (the capsule's
// artifacts/e2e-scenarios.md, which mirrors spec.md's Acceptance criteria
// and Scenario blocks), BEFORE the implementation lands. It pins the
// submit placement signalling + missing-clone warning contract through
// the project's real e2e surfaces only: the pf/aihub/fakepi binaries,
// the `pf` CLI (human + --json), the daemon's UDS submit face, the
// cli.toml [workspace] mapping, real git clones under <workspace>/.repo/,
// the daemon log, the durable events file and the shipped skill/docs
// markdown — never implementation internals.
//
// Suite posture (full mapping in artifacts/e2e-scenarios.md):
//   - Every bar here is a NEW-behavior bar: RED on the pre-implementation
//     tree (e2e-author's baseline run) — assertion-level reds, never build
//     reds: this file compiles and vets against the tree as it stands
//     today; every failure is an output/exit-code/JSON/log/filesystem
//     assertion. They must go green when the change lands the spec.
//   - Kept faces that already behave at baseline (the DAEMON-412 refusal,
//     the hub-probe-failure submit staying accepted and durable-queued
//     with no invented placement) are regression guards with NO scenario
//     here: they pass today and the spec requires them unchanged.
//
// Machine id: every daemon boots as m-place (optMachineID), so the
// placement assertions locate the machine by content, not by wording.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// placementMachine is the daemon machine id every scenario boots with.
const placementMachine = "m-place"

// placementRepos are the two task repos of the e2e project: placera and
// placerb. The names never appear in a work item goal, so an output
// assertion on a repo name can only be satisfied by the placement/warning
// lines under test (never vacuously through the submitted slug).
var placementRepos = [2]string{"placera", "placerb"}

// placementInstall boots the standard install with placera and placerb
// registered as the e2e project's repos.
func placementInstall(t *testing.T) *v1Install {
	t.Helper()
	ra, rb := newTaskRemote(t), newTaskRemote(t)
	return newV1InstallRepos(t, []api.ProjectRepo{
		{Name: placementRepos[0], URL: ra.url},
		{Name: placementRepos[1], URL: rb.url},
	})
}

// placementNewWI files one autopilot execution ticket in the e2e project and
// scripts its agent with the canonical envelope plan; inventory, when
// non-nil, is the work item's frozen repo_inventory.
func placementNewWI(t *testing.T, inst *v1Install, goal string, inventory json.RawMessage) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:       v1Project,
		Goal:          goal,
		BundleName:    strPtr("autopilot"),
		RepoInventory: inventory,
	})
	if err != nil {
		t.Fatalf("placement: create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("placement: create work item answer: %v (%s)", err, raw)
	}
	inst.setPlan(t, wi.ID, envelopeAutopilot())
	inst.trackWI(wi.ID)
	return wi.ID
}

// placementSubmitAnswer is the minimum `pf submit --json` answer shape the
// spec pins: the old {accepted, work_item_id} plus the placement
// (machine_id, capsule_path — absent when unknown) and the missing-clone
// warnings (missing_repos, each named repo, sorted).
type placementSubmitAnswer struct {
	Accepted     bool     `json:"accepted"`
	WorkItemID   string   `json:"work_item_id"`
	MachineID    string   `json:"machine_id"`
	CapsulePath  string   `json:"capsule_path"`
	MissingRepos []string `json:"missing_repos"`
}

// placementSubmitJSON runs `pf --json submit <wi>` against d (exit 0
// asserted by the harness) and decodes the raw answer.
func placementSubmitJSON(t *testing.T, d *v1Daemon, wi string) placementSubmitAnswer {
	t.Helper()
	out := d.pf(t, "--json", "submit", wi)
	var a placementSubmitAnswer
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &a); err != nil {
		t.Fatalf("placement: submit --json answer undecodable: %v (%q)", err, out)
	}
	return a
}

// placementSameSet reports whether got and want carry the same string set
// (order-free; the daemon sorts missing_repos).
func placementSameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g, w := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// placementLog reads the daemon's whole log (logTail keeps only the last
// 4KB; the auto-pull clone warning lands right after the claim, early).
func placementLog(t *testing.T, d *v1Daemon) string {
	t.Helper()
	raw, err := os.ReadFile(d.logPath)
	if err != nil {
		t.Fatalf("placement: daemon log: %v", err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// Requirement 1 + 2: the submit face states the claim's placement; the
// --json answer carries it (the old shape is replaced in place).
// ---------------------------------------------------------------------------

// TestSubmitPlacementAcceptedSubmitStatesPlacementHumanAndJSON: an accepted submit
// with every clone present states the placement — the local daemon's
// machine id and the workspace/capsule landing path — on the human face,
// and the --json answer carries machine_id + capsule_path with an empty
// missing_repos (all clones present: no warning).
func TestSubmitPlacementAcceptedSubmitStatesPlacementHumanAndJSON(t *testing.T) {
	inst := placementInstall(t)
	d := newV1Daemon(t, inst, optMachineID(placementMachine))
	d.start()
	for _, name := range placementRepos {
		w198Clone(t, d.workspace, name, "main")
	}

	wiH := placementNewWI(t, inst, "placement accepted submit states the placement", nil)
	out := d.pf(t, "submit", wiH)
	if !strings.Contains(out, placementMachine) {
		t.Fatalf("human output must state the target machine %q alongside today's acceptance line (got %q)", placementMachine, out)
	}
	if capH := d.capsuleDir(wiH); !strings.Contains(out, capH) {
		t.Fatalf("human output must state the workspace / capsule landing path %q (got %q)", capH, out)
	}

	wiJ := placementNewWI(t, inst, "placement json answer carries the placement", nil)
	a := placementSubmitJSON(t, d, wiJ)
	if !a.Accepted || a.WorkItemID == "" {
		t.Fatalf("submit --json must stay accepted: %+v", a)
	}
	if a.MachineID != placementMachine {
		t.Fatalf("submit --json machine_id %q, want %q (the old {accepted, work_item_id}-only shape is replaced in place)", a.MachineID, placementMachine)
	}
	if capJ := d.capsuleDir(wiJ); a.CapsulePath != capJ {
		t.Fatalf("submit --json capsule_path %q, want the claim's own landing path %q", a.CapsulePath, capJ)
	}
	if len(a.MissingRepos) != 0 {
		t.Fatalf("all clones present: missing_repos must be empty, got %v", a.MissingRepos)
	}

	// both submits were accepted claims, never refusals
	for _, wi := range []string{wiH, wiJ} {
		w113WaitCapsule(t, d.capsuleDir(wi), 90*time.Second)
	}
}

// ---------------------------------------------------------------------------
// Requirement 3: warn on every missing repo clone, still accept.
// ---------------------------------------------------------------------------

// TestSubmitPlacementMissingCloneWarnedByNameStillAccepted: the mapped workspace
// lacks a git clone for one of the repos the claim will clone — the human
// output MUST warn naming that repo (and not the present one), and the
// daemon MUST still accept (no refusal, no override flag).
func TestSubmitPlacementMissingCloneWarnedByNameStillAccepted(t *testing.T) {
	inst := placementInstall(t)
	d := newV1Daemon(t, inst, optMachineID(placementMachine))
	d.start()
	w198Clone(t, d.workspace, placementRepos[0], "main") // placerb missing

	wi := placementNewWI(t, inst, "placement missing clone warned, still accepted", nil)
	out := d.pf(t, "submit", wi)
	if !strings.Contains(out, "accepted by the local daemon") {
		t.Fatalf("the submit must stay accepted alongside the warning (got %q)", out)
	}
	if !strings.Contains(out, placementRepos[1]) {
		t.Fatalf("human output must warn naming the missing repo %s (got %q)", placementRepos[1], out)
	}
	if strings.Contains(out, placementRepos[0]) {
		t.Fatalf("the present clone %s must not be warned (got %q)", placementRepos[0], out)
	}
	w113WaitCapsule(t, d.capsuleDir(wi), 90*time.Second)
}

// TestSubmitPlacementCloneSetDeclaredInventoryElseAllProjectRepos: the check set
// is the work item's frozen repo_inventory when it declares one (exactly
// that list — an undeclared project repo is not checked), else every
// repo of the project.
func TestSubmitPlacementCloneSetDeclaredInventoryElseAllProjectRepos(t *testing.T) {
	inst := placementInstall(t)
	d := newV1Daemon(t, inst, optMachineID(placementMachine))
	d.start()
	// the mapped workspace carries no clone at all

	wiInv := placementNewWI(t, inst, "placement declared inventory is the exact check set",
		json.RawMessage(`[{"name":"`+placementRepos[0]+`"}]`))
	a := placementSubmitJSON(t, d, wiInv)
	if !a.Accepted {
		t.Fatalf("the submit must stay accepted: %+v", a)
	}
	if !placementSameSet(a.MissingRepos, placementRepos[:1]) {
		t.Fatalf("declared inventory: missing_repos %v, want exactly [%s] — the undeclared %s is not checked", a.MissingRepos, placementRepos[0], placementRepos[1])
	}

	wiAll := placementNewWI(t, inst, "placement no inventory checks every project repo", nil)
	b := placementSubmitJSON(t, d, wiAll)
	if !b.Accepted {
		t.Fatalf("the submit must stay accepted: %+v", b)
	}
	if !placementSameSet(b.MissingRepos, placementRepos[:]) {
		t.Fatalf("no inventory: missing_repos %v, want every project repo %v", b.MissingRepos, placementRepos)
	}
}

// TestSubmitPlacementCrossMachineClaimWithoutLocalClonesAccepted: the pi#122
// pattern — a claim lands on a machine whose mapped workspace has no
// local clones at all (the capsule clones fresh) — the submit is still
// accepted WITH a warning naming every repo the claim will clone, and
// the claim runs to wrapped, never refused.
func TestSubmitPlacementCrossMachineClaimWithoutLocalClonesAccepted(t *testing.T) {
	inst := placementInstall(t)
	d := newV1Daemon(t, inst, optMachineID(placementMachine))
	d.start()
	// no clone under d.workspace/.repo — the cross-machine shape

	wi := placementNewWI(t, inst, "placement cross-machine claim accepted with warning", nil)
	out := d.pf(t, "submit", wi)
	for _, name := range placementRepos {
		if !strings.Contains(out, name) {
			t.Fatalf("cross-machine submit must be accepted WITH a warning naming %s (got %q)", name, out)
		}
	}
	inst.waitWIStatus(t, wi, "wrapped", 240*time.Second)
}

// ---------------------------------------------------------------------------
// Requirement 4: auto-pulled claims log one missing-clone warning without
// blocking and without a new event type.
// ---------------------------------------------------------------------------

// TestSubmitPlacementAutoPullMissingClonesOneWarningNoNewEvent: with `pf ops auto`
// on, an auto-pulled claim whose mapped workspace lacks the clones logs
// exactly one warning naming each missing repo, is not blocked (the run
// wraps) and emits no event kind a clones-present claim does not.
func TestSubmitPlacementAutoPullMissingClonesOneWarningNoNewEvent(t *testing.T) {
	inst := placementInstall(t)

	// happy path: every clone present — no warning line, and the
	// event-kind vocabulary a missing-clone claim must not exceed.
	dA := newV1Daemon(t, inst, optAuto(), optMachineID(placementMachine))
	dA.start()
	for _, name := range placementRepos {
		w198Clone(t, dA.workspace, name, "main")
	}
	wiA := placementNewWI(t, inst, "placement auto pull, all clones present", nil)
	inst.waitWIStatus(t, wiA, "wrapped", 240*time.Second)
	if log := placementLog(t, dA); strings.Contains(log, placementRepos[0]) || strings.Contains(log, placementRepos[1]) {
		t.Fatalf("clones present: the daemon log must carry no clone warning (log %q)", log)
	}
	happy := dA.eventKinds(t, wiA)
	dA.stop() // its auto wave must not race the second daemon's claim

	// missing clones: the auto claim logs one warning per missing repo,
	// is not blocked, and adds no event kind.
	dB := newV1Daemon(t, inst, optAuto(), optMachineID(placementMachine))
	dB.start()
	wiB := placementNewWI(t, inst, "placement auto pull, clones missing", nil)
	inst.waitWIStatus(t, wiB, "wrapped", 240*time.Second)
	log := placementLog(t, dB)
	for _, name := range placementRepos {
		if n := strings.Count(log, name); n != 1 {
			t.Fatalf("auto claim with missing clones: the daemon log must carry exactly one warning naming %s (found %d; log %q)", name, n, log)
		}
	}
	for _, k := range dB.eventKinds(t, wiB) {
		if !contains(happy, k) {
			t.Fatalf("auto claim with missing clones emitted NEW event kind %q (clones-present kinds %v) — no new event type is allowed", k, happy)
		}
	}
}

// ---------------------------------------------------------------------------
// Requirement 2 (docs): upgrade.md notes the --json shape change.
// ---------------------------------------------------------------------------

// TestSubmitPlacementUpgradeNotesSubmitJSONShapeChange: docs/upgrade.md must note
// the `pf submit --json` answer shape change for external parsers — the
// new fields machine_id / capsule_path / missing_repos replace the old
// {accepted, work_item_id}-only shape in place.
func TestSubmitPlacementUpgradeNotesSubmitJSONShapeChange(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "docs", "upgrade.md"))
	if err != nil {
		t.Fatalf("placement: docs/upgrade.md: %v", err)
	}
	doc := string(raw)
	for _, tok := range []string{"machine_id", "capsule_path", "missing_repos"} {
		if !strings.Contains(doc, tok) {
			t.Fatalf("docs/upgrade.md must note the `pf submit --json` answer shape change for external parsers (token %q missing)", tok)
		}
	}
}

// ---------------------------------------------------------------------------
// Requirements 5 + 6 (skill docs): the pre-submit owner confirmation and
// the verbatim relay of placement + warnings.
// ---------------------------------------------------------------------------

// placementAny reports whether s carries at least one of the tokens.
func placementAny(s string, toks ...string) bool {
	for _, tok := range toks {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// TestSubmitPlacementSkillDocsPrecheckAndRelay: the shipped skill docs carry the
// new contract — verbs/submit.md documents the new output (placement
// line, missing-clone warning, the replaced --json shape) and requires
// the pre-submit owner confirmation (resolve the placement from cli.toml
// and the repo set from repo_inventory; confirm when the target workspace
// differs from the session cwd or a clone is missing; submit only after
// yes) plus the verbatim relay of the placement line and warnings with
// the pf pause / pf cancel hint; execute-intent.md routes the same
// precheck.
func TestSubmitPlacementSkillDocsPrecheckAndRelay(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("placement: %s: %v", rel, err)
		}
		return string(b)
	}
	submit := strings.ToLower(read(filepath.Join("skills", "polyforge", "verbs", "submit.md")))

	// the new output is documented: the placement line (machine +
	// capsule landing path) and the missing-clone warning.
	if !placementAny(submit, "machine_id", "machine id", "machine-id") {
		t.Fatalf("verbs/submit.md must document the placement line's machine (token machine_id / machine id / machine-id missing)")
	}
	for _, tok := range []string{"missing", ".repo/"} {
		if !strings.Contains(submit, tok) {
			t.Fatalf("verbs/submit.md must document the missing-clone warning (token %q missing)", tok)
		}
	}
	// the --json line no longer teaches the replaced shape, and names
	// the new answer fields.
	if strings.Contains(submit, "{accepted, work_item_id}") {
		t.Fatalf("verbs/submit.md still teaches the replaced --json shape {accepted, work_item_id}")
	}
	if !placementAny(submit, "machine_id", "capsule_path", "missing_repos") {
		t.Fatalf("verbs/submit.md must name the new --json answer fields (machine_id / capsule_path / missing_repos)")
	}
	// the pre-submit owner confirmation: resolve the placement from the
	// cli.toml mapping and the repo set from repo_inventory; a session
	// cwd outside every mapped workspace counts as ≠ target.
	for _, tok := range []string{"cli.toml", "repo_inventory", "cwd"} {
		if !strings.Contains(submit, tok) {
			t.Fatalf("verbs/submit.md must carry the pre-submit placement/clone resolution (token %q missing)", tok)
		}
	}
	if !placementAny(submit, "confirm", "ask the owner") {
		t.Fatalf("verbs/submit.md must require asking the owner before submitting when placement or clone context is off")
	}
	// the verbatim relay of the new output, and the pause/cancel hint
	// for a warning the precheck did not predict.
	if !placementAny(submit, "word for word", "verbatim") {
		t.Fatalf("verbs/submit.md must require relaying the placement line and warnings word for word")
	}
	for _, tok := range []string{"pf pause", "pf cancel"} {
		if !strings.Contains(submit, tok) {
			t.Fatalf("verbs/submit.md must tell the owner %q can stop a work item whose warning the precheck did not predict", tok)
		}
	}

	// execute-intent.md routes its submit step through the same
	// pre-submit confirmation.
	ei := strings.ToLower(read(filepath.Join("skills", "polyforge", "verbs", "execute-intent.md")))
	if !placementAny(ei, "confirm", "ask the owner") {
		t.Fatalf("verbs/execute-intent.md must carry the same pre-submit confirmation requirement on its submit step")
	}
}
