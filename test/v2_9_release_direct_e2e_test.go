package test

// v2_9_release_direct_e2e_test.go: the release lane under workflow contract
// v2 change 9 (design §4.4, ruling O11) against the REAL `pf daemon` and the
// fakepi pi persona. The lane's agents run the repository's own
// `.polyforge/release.sh` / `.polyforge/deploy.sh` THEMSELVES — in the owner
// repo, under the daemon's command PATH (the step's own PATH), inside one tool
// call — instead of handing the line to the daemon as a run request; the
// command-evidence notary left the lane. The task repo here carries a FAKE
// release procedure (no network, nothing published) that records every call
// with the step environment it ran under; the capsule carries no deploy
// procedure, so deploy deploys nothing and hands off done (workflow
// contract 4: release's deploy `defer` became `done`, the reason in the
// body).
//
// Pinned on the real stack:
//   - release → verify → deploy(nothing deployed) end to end: the procedure ran in
//     repos/<owner> with PATH = the daemon's `[daemon] command_path` (and no
//     PF_COMMAND_PATH in its environment), once for the release and
//     once for `--verify V`, and its output landed in artifacts/release.log;
//   - the tool windows: the release dispatch ran under the lane's 120-minute
//     window (PF_TOOL_WINDOW_MS = 7200000), verify under the daemon's global
//     window (none declared) and deploy under its own 30 minutes;
//   - the daemon ran no line for the lane;
//   - a `pf step handoff --status waive` on a release gate is refused at the
//     call — no gate of the lane declares `waivable: true` — the refusal
//     names the gate's declaration, not the owner message, nothing is
//     recorded, and the step's resumed session hands off done;
//   - downstream reads upstream from result files (workflow contract 4):
// the deploy dispatch's prompt carries its definition's
//     开工前读什么、核对什么 section — the verified version from
//     verified-version.json, checked against the version of the release
//     script's record (artifacts/release-record.json), never execution.json;
//     the scripted deploy agent follows it with its own commands (jq over the
//     two files), so a verified version that differs from the release
//     record's ends the lane failed_gate on deploy with nothing deployed
//     (third scenario);
//   - (second scenario) the release step HOLDS the heavy-run admission for
//     its dispatch: under one slot a second release work item queues
//     (`verify_queued`, slots full) and its release process starts only
//     once the first's is gone.
//
// Run (targeted): GOWORK=off go test -count=1 -run '^TestV29' ./test/

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// v29ReleaseScript is the fake release procedure the task repo carries
// (the lane's procedure contract: `version=`, `commit=`, `notes=` lines on
// a release, exit 0 on `--verify <v>`). It records each call — the
// arguments, the cwd, the PATH it ran under and the step environment —
// one line per call in the capsule's artifacts, nothing else.
const v29ReleaseScript = `#!/usr/bin/env bash
# fake release procedure (e2e): records each call with the environment the
# step ran it under; publishes nothing.
set -eu
log=../../artifacts/fake-release-calls.log
printf 'argv=%s|cwd=%s|path=%s|command_path=%s|tool_window_ms=%s|step=%s|spawn=%s\n' \
  "$*" "$(pwd)" "$PATH" "${PF_COMMAND_PATH+set}" "${PF_TOOL_WINDOW_MS-}" "${PF_STEP-}" "${PF_SPAWN-}" >> "$log"
if [ "${1-}" = "--verify" ]; then
  echo "fake release: verified ${2-}"
  exit 0
fi
echo "fake release: releasing the head"
echo "version=v0.9.1"
echo "commit=$(git rev-parse HEAD)"
echo "notes=RELEASE-NOTES.md"
`

const (
	v29Version = "v0.9.1"
	v29Commit  = "0000000000000000000000000000000000000029"
	v29Repo    = "demo-src"
	// v29Global is the daemon's global tool window the first scenario runs
	// under: verify declares none, so its process carries this one.
	v29Global = 7 * time.Minute
)

// v29ReleaseLine is the release step's own command (steps/release.md §4),
// as a fakepi Run line: the fake runs it with `sh -c` from the capsule
// root, so the step text's line is wrapped in a bash call.
var v29ReleaseLine = `bash -c 'set -o pipefail; cd repos/` + v29Repo + ` && .polyforge/release.sh 2>&1 | tee ../../artifacts/release.log'`

// v29VerifyLine is verify's check (e): the repo's own verifier on the
// released tree, under the step's PATH.
var v29VerifyLine = `bash -c 'cd repos/` + v29Repo + ` && git fetch --tags origin && .polyforge/release.sh --verify ` + v29Version + `'`

// v29DeployCheck is the deploy agent's read-first check as its own
// commands: the verified version (verified-version.json) against the
// version of the release script's record (artifacts/release-record.json —
// a result file, never execution.json, which no step reads), then — the
// capsule carries no deploy procedure — nothing deployed (state none); a
// mismatch is the gate's FAIL naming both, nothing deployed. The file it
// writes is the scripted hand-off's output (its fail_reason the
// hand-off's --reason).
const v29DeployCheck = `rv=$(jq -r .version artifacts/release-record.json) && vv=$(jq -r .version artifacts/verified-version.json) && ` +
	`if [ -n "$vv" ] && [ "$vv" = "$rv" ]; then printf '{"state":"none","summary":"no repo carries a deploy procedure: nothing deployed"}' > artifacts/deploy-out.json; ` +
	`else printf '{"state":"not_changed","fail_reason":"verified version %s is not the version of the release record (%s): nothing deployed"}' "$vv" "$rv" > artifacts/deploy-out.json; fi`

// v29ReleaseActions scripts the three steps of one release lane: release
// runs the fake procedure and writes its record, verify runs `--verify V`
// and writes the verified version (verifiedVersion; "" = the released
// one), deploy follows its read-first check and, with no procedure in the
// capsule, deploys nothing (recording its own tool window on the way) —
// done when the versions agree, fail when they do not (the scripted
// agent's status is the one its check computes). releaseDelay
// keeps the release process alive that long after the procedure ran
// (the admission scenario's lever); extraDeploy, when set, is an envelope
// action dispatched BEFORE the final deploy (the refused-waiver shape).
func v29ReleaseActions(releaseDelay time.Duration, extraDeploy []map[string]any, verifiedVersion ...string) []map[string]any {
	vv := v29Version
	if len(verifiedVersion) > 0 {
		vv = verifiedVersion[0]
	}
	record := `{"version":"` + v29Version + `","commit":"` + v29Commit + `","default_branch":"main","origin_head_at_release":"` + v29Commit + `",` +
		`"notes":"RELEASE-NOTES.md","repo":"repos/` + v29Repo + `","pre_release_tags_file":"artifacts/pre-release-tags.txt","resumed":false}` + "\n"
	actions := []map[string]any{
		{"do": "envelope", "id": "release", "outcome": "completed", "ms": int(releaseDelay / time.Millisecond),
			"run":   []string{v29ReleaseLine},
			"files": map[string]string{"artifacts/release-record.json": record, "artifacts/pre-release-tags.txt": ""},
			"output": map[string]any{"verdict": "pass", "version": v29Version, "commit": v29Commit, "notes": "RELEASE-NOTES.md",
				"artifacts_published": []string{v29Version}, "resumed": false}},
		{"do": "envelope", "id": "verify", "outcome": "completed",
			"run":   []string{v29VerifyLine},
			"files": map[string]string{"artifacts/verified-version.json": `{"version":"` + vv + `","commit":"` + v29Commit + `"}` + "\n"},
			"output": map[string]any{"verdict": "pass", "version": vv, "commit": v29Commit,
				"checks": []map[string]any{
					{"name": "release commit and origin head", "status": "pass"},
					{"name": "the tag", "status": "pass"},
					{"name": "immutability", "status": "pass"},
					{"name": "release notes", "status": "pass"},
					{"name": "the repo's own verifier", "status": "pass", "detail": "release.sh --verify exited 0"},
				}}},
	}
	actions = append(actions, extraDeploy...)
	outcome := "completed"
	if vv != v29Version {
		outcome = "gate_failed"
	}
	return append(actions, map[string]any{"do": "envelope", "id": "deploy", "outcome": outcome,
		"run":         []string{`printenv PF_TOOL_WINDOW_MS > artifacts/deploy-env.txt`, v29DeployCheck},
		"output_file": "artifacts/deploy-out.json"})
}

// v29DeployPrompt answers the fake's copy of a dispatch's prompt.
func v29DeployPrompt(t *testing.T, capDir string, spawnN int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(spawnN)+".txt"))
	if err != nil {
		t.Fatalf("the deploy dispatch's prompt: %v", err)
	}
	return string(raw)
}

// v29AssertDeployReadsFirst checks a fresh deploy dispatch's prompt for
// its definition's read-first section (workflow contract 4: the step's
// own text, no daemon-rendered `# Verify first` or input list): the
// verified version from artifacts/verified-version.json, checked against
// the release record's version (artifacts/release-record.json, the release
// script's result file — a check, never a source), a mismatch the gate's
// fail; and no step reads execution.json.
func v29AssertDeployReadsFirst(t *testing.T, capDir string, spawnN int) {
	t.Helper()
	prompt := v29DeployPrompt(t, capDir, spawnN)
	i := strings.Index(prompt, "\n## 开工前读什么、核对什么")
	if i < 0 {
		t.Fatalf("the deploy prompt carries no 开工前读什么、核对什么 section:\n%s", prompt)
	}
	section := prompt[i:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		section = section[:j+1]
	}
	for _, want := range []string{
		"`artifacts/verified-version.json`",
		"`artifacts/release-record.json`",
		"That value is a check, never a source.",
		"is a `fail` (production `not_changed`)",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the deploy prompt's 开工前读什么、核对什么 lacks %q:\n%s", want, section)
		}
	}
	for _, gone := range []string{"\n# Verify first\n", "steps.release.output.version"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("the deploy prompt still carries the contract 3 %q", gone)
		}
	}
}

// v29Handoffs answers the capsule's hand-off results of step on the call
// ledger (runtime-state/step-calls.jsonl), accepted and refused, in order.
func v29Handoffs(t *testing.T, capDir, step string) []stepcall.Line {
	t.Helper()
	lines, err := stepcall.Read(capDir)
	if err != nil {
		t.Fatalf("the call ledger: %v", err)
	}
	var out []stepcall.Line
	for _, l := range lines {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHandoff && l.Step == step {
			out = append(out, l)
		}
	}
	return out
}

// v29DeployOut reads the deploy check's file.
func v29DeployOut(t *testing.T, capDir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "artifacts", "deploy-out.json"))
	if err != nil {
		t.Fatalf("the deploy check's file: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the deploy check's file: %v", err)
	}
	return out
}

// v29WaiveAttempt is a deploy dispatch that hands off `waive` citing an
// owner message of this round (`pf step handoff --status waive
// --per-owner <at>`). The message line stands in for the owner relay: the
// fake appends the daemon's own record shape to runtime-state/asks.jsonl,
// then cites its stamp — exactly what a real agent reads off the record
// after `pf answer`. The daemon must refuse the call on the gate's
// declaration (no `waivable: true`), not on the message; nothing is
// recorded and the process stays in its turn.
var v29WaiveAttempt = []map[string]any{
	{"do": "envelope", "id": "deploy", "outcome": "gate_failed",
		"run": []string{`at=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ) && mkdir -p runtime-state artifacts && ` +
			`printf '{"at":"%s","kind":"message","step":"deploy","text":"just waive the deploy and move on","by":"usr_root"}\n' "$at" >> runtime-state/asks.jsonl && ` +
			`printf '{"kind":"waive","reply_at":"%s","reason":"the owner asked to waive the deploy gate"}' "$at" > artifacts/deploy-intent.json`},
		"intent_file": "artifacts/deploy-intent.json",
		"output":      map[string]any{"verdict": "fail", "state": "not_changed", "fail_reason": "scripted: the deploy procedure refused; the owner was asked to waive"}},
}

// v29PlantReleaseScript commits the fake release procedure (executable)
// on the task remote's main, so the claim's clone carries it.
func v29PlantReleaseScript(t *testing.T, remote taskRemote) {
	t.Helper()
	dir := filepath.Join(remote.dir, ".polyforge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.sh"), []byte(v29ReleaseScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote.dir, "RELEASE-NOTES.md"), []byte("# notes\n\nthe fake release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, remote.dir, "add", "-A")
	gitRun(t, remote.dir, "commit", "-q", "-m", "fake release procedure")
}

// v29Call is one recorded call of the fake procedure.
type v29Call map[string]string

// v29Calls reads the fake procedure's call log of a capsule.
func v29Calls(t *testing.T, capDir string) []v29Call {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "artifacts", "fake-release-calls.log"))
	if err != nil {
		t.Fatalf("the fake release procedure left no call log: %v", err)
	}
	var out []v29Call
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		c := v29Call{}
		for _, kv := range strings.Split(line, "|") {
			k, v, _ := strings.Cut(kv, "=")
			c[k] = v
		}
		out = append(out, c)
	}
	return out
}

// v29WindowMS answers a tool window as the PF_TOOL_WINDOW_MS value.
func v29WindowMS(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

// TestV29ReleaseLaneRunsTheProceduresItself: release → verify →
// deploy(nothing deployed) on the real daemon, the fake procedure run by
// the steps themselves under the daemon's command PATH and the lane's tool windows,
// no notary record, and the refused waiver on the deploy gate.
func TestV29ReleaseLaneRunsTheProceduresItself(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	v29PlantReleaseScript(t, remote)
	wi := newWIWithBundle(t, inst, "v2 change 9: the release lane runs release.sh itself", "release",
		v29ReleaseActions(0, v29WaiveAttempt))
	t.Cleanup(func() { killOrphans(wi) })

	// the daemon's command PATH: a directory of its own first, the rest of
	// this process's PATH — every step process starts with it as its PATH
	commandPath := t.TempDir() + string(os.PathListSeparator) + os.Getenv("PATH")
	d := newV1Daemon(t, inst, optSupervision(supBounds{tool: v29Global}), optDaemonKeys("command_path = "+strconv.Quote(commandPath)))
	d.start()
	d.pf(t, "submit", wi)

	// the waiver is refused at the call on the gate's declaration; the
	// step's turn ends without a hand-off, its session is resumed with the
	// reminder, and it hands off done
	inst.waitWIStatus(t, wi, "wrapped", 300*time.Second)
	d.settleClose(wi)
	capDir := d.capsuleDir(wi)
	hs := v29Handoffs(t, capDir, "deploy")
	if len(hs) != 2 || hs[0].Outcome != stepcall.OutcomeRefused || hs[1].Outcome != stepcall.OutcomeOK ||
		hs[1].Handoff == nil || hs[1].Handoff.Status != stepcall.StatusDone {
		t.Fatalf("deploy's hand-offs: want the refused waiver, then an accepted done: %+v", hs)
	}
	if why := hs[0].Error; !strings.Contains(why, "waive is a waivable gate's") || !strings.Contains(why, "not declare it waivable") {
		t.Fatalf("the waive hand-off on the deploy gate was not refused as non-waivable: %q", why)
	}
	if strings.Contains(hs[0].Error, "names no owner message") {
		t.Fatalf("the refusal rests on the message, not on the gate's declaration: %q", hs[0].Error)
	}

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Fatalf("the lane did not complete: %s", execDigest(x))
	}
	for _, id := range []string{"release", "verify", "deploy"} {
		if st := x.Steps[id]; st == nil || st.Status != "done" {
			t.Fatalf("step %s is not done: %s", id, execDigest(x))
		}
	}
	if st, _ := v29DeployOut(t, capDir)["state"].(string); st != "none" {
		t.Fatalf("deploy without a procedure deploys nothing (state none), got %v", v29DeployOut(t, capDir))
	}
	// the deploy dispatch was told what to read first; its resumed session
	// (after the refused waiver) was reminded to hand off
	v29AssertDeployReadsFirst(t, capDir, 3)
	if p := v29DeployPrompt(t, capDir, 4); !strings.Contains(p, "ended without `pf step handoff`") || !strings.Contains(p, "hand off now with `pf step handoff`") {
		t.Errorf("the resumed deploy dispatch was not reminded to hand off:\n%s", p)
	}

	// the procedure ran twice, by the steps themselves, in the owner repo,
	// under the daemon's command PATH
	calls := v29Calls(t, capDir)
	if len(calls) != 2 {
		t.Fatalf("the fake procedure recorded %d calls, want 2 (release, --verify): %v", len(calls), calls)
	}
	if calls[0]["argv"] != "" || calls[0]["step"] != "release" || calls[0]["spawn"] != "1" {
		t.Errorf("the first call is not the release step's bare run: %v", calls[0])
	}
	if calls[1]["argv"] != "--verify "+v29Version || calls[1]["step"] != "verify" {
		t.Errorf("the second call is not verify's `--verify V`: %v", calls[1])
	}
	wantCwd := filepath.Join(capDir, "repos", v29Repo)
	pfDir, harnessDir := filepath.Dir(inst.pfPath), filepath.Dir(inst.fakepiPath)
	if r, err := filepath.EvalSymlinks(inst.pfPath); err == nil {
		pfDir = filepath.Dir(r)
	}
	for i, c := range calls {
		if got, _ := filepath.EvalSymlinks(c["cwd"]); got != wantCwd {
			if want, _ := filepath.EvalSymlinks(wantCwd); got != want {
				t.Errorf("call %d ran in %q, want the owner repo %q", i, c["cwd"], wantCwd)
			}
		}
		// the step's PATH is the daemon's pf directory, the harness binary's
		// directory and the command PATH, each entry once; the fake harness
		// puts its own bin dir first again for the commands it runs
		if want := harnessDir + string(os.PathListSeparator) + runtime.JoinPath(pfDir, harnessDir, commandPath); c["path"] != want || c["command_path"] != "" {
			t.Errorf("call %d ran under PATH %q (PF_COMMAND_PATH %q), want the step PATH behind the harness's bin dir, %q, and no PF_COMMAND_PATH", i, c["path"], c["command_path"], want)
		}
	}
	// the tool windows: release its own 120 minutes, verify the daemon's
	// global window, deploy its own 30
	if got := calls[0]["tool_window_ms"]; got != v29WindowMS(120*time.Minute) {
		t.Errorf("the release procedure ran under PF_TOOL_WINDOW_MS=%s, want %s (budget.tool_minutes: 120)", got, v29WindowMS(120*time.Minute))
	}
	if got := calls[1]["tool_window_ms"]; got != v29WindowMS(v29Global) {
		t.Errorf("verify ran under PF_TOOL_WINDOW_MS=%s, want the daemon's global %s (no tool_minutes declared)", got, v29WindowMS(v29Global))
	}
	raw, err := os.ReadFile(filepath.Join(capDir, "artifacts", "deploy-env.txt"))
	if err != nil {
		t.Fatalf("deploy's environment record: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != v29WindowMS(30*time.Minute) {
		t.Errorf("deploy ran under PF_TOOL_WINDOW_MS=%s, want %s (budget.tool_minutes: 30)", got, v29WindowMS(30*time.Minute))
	}
	// the procedure's output is the step's log, as the step text tees it
	if log, err := os.ReadFile(filepath.Join(capDir, "artifacts", "release.log")); err != nil || !strings.Contains(string(log), "version="+v29Version) {
		t.Errorf("artifacts/release.log does not carry the procedure's output: %q (%v)", log, err)
	}
	// the daemon ran no line for the lane
	for _, kind := range d.eventKinds(t, wi) {
		if kind == "run_command" || kind == "run_retry" {
			t.Errorf("event %s: the daemon ran a line for the lane (a run request); the steps run their procedures themselves", kind)
		}
	}
}

// TestV29ReleaseStepHoldsTheHeavyAdmission: under one heavy-run slot the
// second release work item's release step queues (verify_queued, slots
// full) and its process starts only once the first's release process is
// gone — the slot is held for the whole dispatch, the way the daemon's
// verification runs hold it.
func TestV29ReleaseStepHoldsTheHeavyAdmission(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	v29PlantReleaseScript(t, remote)
	const hold = 6 * time.Second
	first := newWIWithBundle(t, inst, "v2 change 9: the first release holds the slot", "release", v29ReleaseActions(hold, nil))
	second := newWIWithBundle(t, inst, "v2 change 9: the second release waits for it", "release", v29ReleaseActions(0, nil))
	t.Cleanup(func() { killOrphans(first); killOrphans(second) })

	d := newV1Daemon(t, inst)
	// one heavy-run slot: the daemon's own config file (a later --config
	// wins over the harness's mapping fixture)
	cfg := filepath.Join(t.TempDir(), "v29-cli.toml")
	body := "# v29 cli.toml\n\n[daemon]\nmax_concurrent_bars = 1\n\n[workspace.\"" + d.workspace + "\"]\nprojects = [\"" + v1Project + "\"]\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	d.extraArgs = append(d.extraArgs, "--config", cfg)
	d.start()

	d.pf(t, "submit", first)
	waitSpawnCount(t, d, first, 1, 60*time.Second)
	d.pf(t, "submit", second)

	// the second's release step queues on the full slot ...
	queued := waitForEvent(t, d, second, "verify_queued", 60*time.Second)
	var q struct {
		Step   string `json:"step"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(queued.Body, &q)
	if q.Step != "release" || !strings.Contains(q.Reason, "heavy-run slots full (1/1 running)") {
		t.Fatalf("the second release did not queue on the held slot: %s", queued.Body)
	}
	if spawns := spawnJournal(t, d.capsuleDir(second)); len(spawns) != 0 {
		t.Fatalf("the second release step was dispatched while queued: %v", spawns)
	}
	// ... and is admitted only once the first's release process is gone
	waitForEvent(t, d, second, "verify_admitted", 60*time.Second)
	for _, wi := range []string{first, second} {
		inst.waitWIStatus(t, wi, "wrapped", 240*time.Second)
		d.settleClose(wi)
	}
	var firstEnvelope string
	for _, e := range fakeJournal(t, d.capsuleDir(first)) {
		if e["type"] == "envelope" && e["step"] == "release" {
			firstEnvelope, _ = e["at"].(string)
		}
	}
	spawns := spawnJournal(t, d.capsuleDir(second))
	if len(spawns) == 0 || firstEnvelope == "" {
		t.Fatalf("no spawn of the second (%d) or no release envelope of the first (%q)", len(spawns), firstEnvelope)
	}
	secondStart, _ := spawns[0]["at"].(string)
	if secondStart <= firstEnvelope {
		t.Fatalf("the second release process started at %s, before the first's release envelope at %s: the slot was not held for the dispatch", secondStart, firstEnvelope)
	}
	for _, wi := range []string{first, second} {
		x := readExecution(t, d.capsuleDir(wi))
		if x.WorkflowStatus != "completed" {
			t.Fatalf("%s did not complete: %s", wi, execDigest(x))
		}
		if calls := v29Calls(t, d.capsuleDir(wi)); len(calls) != 2 || calls[0]["tool_window_ms"] != v29WindowMS(120*time.Minute) {
			t.Fatalf("%s: the procedure's calls %v", wi, calls)
		}
	}
}

// TestV29DeployRefusesAVerifiedVersionThatIsNotReleases: verify hands
// over a verified version that is not the release record's; deploy's
// read-first check (its definition, in its prompt: the version from the
// release script's record, a result file) does not hold, so the gate
// hands off fail and — no on_fail — the lane ends failed_gate on deploy,
// nothing deployed.
func TestV29DeployRefusesAVerifiedVersionThatIsNotReleases(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	v29PlantReleaseScript(t, remote)
	const wrong = "v0.9.2"
	wi := newWIWithBundle(t, inst, "v2 change 9: deploy verifies the verified version against release's", "release",
		v29ReleaseActions(0, nil, wrong))
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	inst.waitWIStatus(t, wi, "failed", 240*time.Second)
	d.settleClose(wi)
	capDir := d.capsuleDir(wi)

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "failed_gate" || x.CurrentStep != "deploy" {
		t.Fatalf("a verified version that is not release's must end the lane failed_gate on deploy: %s", execDigest(x))
	}
	for _, id := range []string{"release", "verify"} {
		if st := x.Steps[id]; st == nil || st.Status != "done" {
			t.Fatalf("step %s is not done: %s", id, execDigest(x))
		}
	}
	hs := v29Handoffs(t, capDir, "deploy")
	if len(hs) != 1 || hs[0].Outcome != stepcall.OutcomeOK || hs[0].Handoff == nil || hs[0].Handoff.Status != stepcall.StatusFail {
		t.Fatalf("deploy hands off fail once: %+v", hs)
	}
	if reason := hs[0].Handoff.Reason; !strings.Contains(reason, wrong) || !strings.Contains(reason, v29Version) {
		t.Fatalf("deploy's FAIL must name both versions: %q", reason)
	}
	if st, _ := v29DeployOut(t, capDir)["state"].(string); st != "not_changed" {
		t.Fatalf("nothing is deployed on a refused version: %v", v29DeployOut(t, capDir))
	}
	v29AssertDeployReadsFirst(t, capDir, 3)
	if spawns := spawnJournal(t, capDir); len(spawns) != 3 {
		t.Fatalf("the FAIL is terminal (no rewind, no re-dispatch): %d spawns", len(spawns))
	}
}
