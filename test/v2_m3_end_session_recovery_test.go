package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// v2 m3 (v2-converge §6, §8 tier four): recovering a work item held by a
// dead daemon needs NO special takeover machinery — only two ordinary
// concepts:
//
//  1. an admin/maintainer force-ENDS the stale session (WI -> queued; the
//     old holder is fenced because its secret no longer matches), and
//  2. any daemon claims it normally; the claim restores progress from the
//     aihub capsule revision chain (prepareCapsule clone + the
//     refs/pf-task carrier lane for task repos) and the driver continues
//     from execution.json's current step.
//
// This scenario is the acceptance oracle of that claim: daemon A runs the
// WI through two completed steps (grill, fix — fix commits to a task
// repo) and is parked on the third; A is then killed hard (SIGKILL, and
// its step process reaped: the machine is gone); an admin force-ends the
// session; daemon B (different machine id, data dir and workspace) claims
// the queued WI with a plain `pf submit`.
//
//   - B resumes at the step AFTER the last completed one: neither grill
//     nor fix is re-dispatched on B;
//   - A's task-repo commit, which reached aihub through the carrier ref,
//     is present in B's clone;
//   - every late write from A's old secret is rejected: the git face
//     (GIT-401 naming the new holder), a real git push, and the
//     session-fenced session-end verdict;
//   - A restarting later neither resurrects nor double-runs the WI: its
//     restart session verify sees the session is no longer its own and
//     releases (kill tree, quarantine, session_released, no hub change).
func TestV2M3EndSessionRecovery(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	wi := inst.newWI(t, "m3: end-session + normal claim resumes from the chain",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo-src", "origin": remote.url},
			// Daemon A parks alive on the third step (the stuck holder).
			{"do": "envelope", "id": "verification", "park": true},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	daemonA := newV1Daemon(t, inst, optMachineID("m-e2e-a"))
	daemonA.start()
	daemonA.pf(t, "submit", wi)

	// A: two steps done, the third dispatched and parked.
	waitStepStatus(t, daemonA, wi, "fix", "done", 90*time.Second)
	waitStepStatus(t, daemonA, wi, "verification", "in_progress", 60*time.Second)
	waitUntil(t, 30*time.Second, "A's step never parked", func() bool {
		for _, rec := range fakeJournal(t, daemonA.capsuleDir(wi)) {
			if rec["type"] == "park" && rec["step"] == "verification" {
				return true
			}
		}
		return false
	})
	repoA := filepath.Join(daemonA.capsuleDir(wi), "repos", "demo-src")
	codeHead := strings.TrimSpace(gitRun(t, repoA, "rev-parse", "HEAD"))
	if codeHead == remote.head {
		t.Fatalf("the fix step left no task-repo commit (HEAD %s == origin %s)", codeHead, remote.head)
	}
	preSpawns := len(spawnJournal(t, daemonA.capsuleDir(wi)))
	if preSpawns != 3 {
		t.Fatalf("daemon A dispatched %d step processes, want 3", preSpawns)
	}

	// Everything A did must have REACHED aihub before the crash: the
	// carrier ref (task-repo commit), the execution state (fix done, third
	// step in flight) and the spawn journal. This is the premise of the
	// whole recovery path — recovery restores only what aihub holds.
	waitUntil(t, 60*time.Second, "A's work never fully reached aihub", func() bool {
		ref := exec.Command("git", "--git-dir", inst.bareRepo(wi), "rev-parse", "--verify", "-q", "refs/pf-task/demo-src")
		out, err := ref.Output()
		if err != nil || strings.TrimSpace(string(out)) != codeHead {
			return false
		}
		if inst.bareCommitCount(t, wi) == 0 || !inst.bareHasPath(t, wi, "runtime-state/execution.json") {
			return false
		}
		var x executionView
		if json.Unmarshal([]byte(inst.bareFileAtMain(t, wi, "runtime-state/execution.json")), &x) != nil {
			return false
		}
		fix, ver := x.Steps["fix"], x.Steps["verification"]
		if fix == nil || fix.Status != "done" || ver == nil || ver.Status != "in_progress" {
			return false
		}
		return strings.Count(inst.bareFileAtMain(t, wi, "runtime-state/spawn-journal.jsonl"), "\n") >= 3
	})

	// A's secret (state.json is the one place it lives) and the fencing
	// baseline: the credential works while A holds the session.
	sessA := inst.sessionOf(t, wi)
	oldSecret := persistedSecret(t, daemonA, wi)
	if fp := api.SessionFingerprint(oldSecret); fp != sessA.Fingerprint {
		t.Fatalf("A's persisted secret does not hash to the public fingerprint (%s != %s)", fp, sessA.Fingerprint)
	}
	gitFace := inst.url + "/git/" + wi + ".git/info/refs?service=git-receive-pack"
	if code, _, body := httpStatusOf("GET", gitFace, wi, oldSecret); code != 200 {
		t.Fatalf("pre-end credential probe: HTTP %d: %s", code, body)
	}

	// A dies hard; its step process dies with the machine.
	daemonA.crash()
	killOrphans(wi)

	// The admin force-ends the stale session: the WI returns to queued,
	// the session row is gone, an audit event names the old fingerprint.
	endSession(t, inst, wi, "m3 acceptance: holder is dead")
	inst.waitWIStatus(t, wi, "queued", 15*time.Second)
	if s := inst.sessionOf(t, wi); s.Fingerprint != "" {
		t.Fatalf("force-end left a session behind: %+v", s)
	}

	// The old secret is fenced everywhere it could write (queued: no
	// holder at all).
	assertStaleWritesRejected(t, inst, wi, oldSecret, "")

	// The plan B follows. Spawn ordinals are chain-wide (B re-seeds its
	// derivation counter from the cloned spawn journal: its first
	// dispatch is #4): #4 re-runs the interrupted verification step and
	// parks on it (the last step) so the WI stays held by B while A
	// restarts below.
	inst.setPlan(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "park": true},
		{"do": "envelope", "id": "verification", "park": true},
	})

	// Daemon B: another machine, another data dir, a PLAIN claim.
	daemonB := newV1Daemon(t, inst, optMachineID("m-e2e-b"))
	daemonB.start()
	daemonB.pf(t, "submit", wi)
	var sessB wiSession
	waitUntil(t, 30*time.Second, "B never claimed the queued WI", func() bool {
		sessB = inst.sessionOf(t, wi)
		return sessB.Fingerprint != "" && sessB.Machine == "m-e2e-b"
	})
	if sessB.Fingerprint == sessA.Fingerprint {
		t.Fatal("B holds A's session")
	}

	// B resumed at the interrupted step (the last one) and parks on it.
	waitStepStatus(t, daemonB, wi, "verification", "in_progress", 60*time.Second)
	capB := daemonB.capsuleDir(wi)
	waitUntil(t, 30*time.Second, "B's verification never parked", func() bool {
		for _, rec := range fakeJournal(t, capB) {
			if n, _ := rec["spawn_n"].(float64); rec["type"] == "park" && int(n) > preSpawns {
				return true
			}
		}
		return false
	})
	// fakepi's journal rides the chain, so B's capsule carries whatever
	// prefix of A's records had been pushed when A was killed (the
	// journal under logs/ is not part of the synced premise above, so
	// its length is NOT A's local length). B's OWN envelope writes are
	// told apart by the capsule path they record: A's name A's capsule.
	var stepsRun []string
	for _, rec := range fakeJournal(t, capB) {
		if p, _ := rec["path"].(string); rec["type"] == "envelope" && strings.HasPrefix(p, capB+string(filepath.Separator)) {
			stepsRun = append(stepsRun, rec["step"].(string))
		}
		// B's parked step writes no envelope: its park record (spawn
		// ordinal past A's) marks the step B ran.
		if n, _ := rec["spawn_n"].(float64); rec["type"] == "park" && int(n) > preSpawns {
			stepsRun = append(stepsRun, rec["step"].(string))
		}
	}
	if len(stepsRun) == 0 || contains(stepsRun, "grill") || contains(stepsRun, "fix") {
		t.Fatalf("B re-ran a completed step or ran nothing: B's envelope writes %v", stepsRun)
	}
	if stepsRun[0] != "verification" {
		t.Fatalf("B did not resume at the interrupted step: %v", stepsRun)
	}
	x := readExecution(t, capB)
	if x.CurrentStep != "verification" || x.Steps["grill"].Status != "done" || x.Steps["fix"].Status != "done" {
		t.Fatalf("B's execution state wrong: %s (current %s)", execDigest(x), x.CurrentStep)
	}
	if n := len(spawnJournal(t, capB)); n != preSpawns+1 {
		t.Fatalf("B's spawn journal holds %d dispatches, want %d (A's 3 + the re-run verification)", n, preSpawns+1)
	}

	// A's task-repo commit, carried through aihub, is in B's clone.
	repoB := filepath.Join(capB, "repos", "demo-src")
	if head := strings.TrimSpace(gitRun(t, repoB, "rev-parse", "HEAD")); head != codeHead {
		t.Fatalf("B's task repo at %s, want A's commit %s", head, codeHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || !strings.Contains(string(b), wi) {
		t.Fatalf("A's work.txt is missing from B's clone: %q, %v", b, err)
	}

	// Late writes from A's old secret are rejected, now naming B.
	assertStaleWritesRejected(t, inst, wi, oldSecret, sessB.Fingerprint)

	// B keeps running with its verification step parked: a LIVE step process
	// for the same WI id on the same kernel. A's restart release sweeps
	// by PF_WI_ID AND PF_DAEMON_ID, so it must leave B's process alone
	// (on one host both daemons share the kernel).
	stBefore := inst.wiStatus(t, wi)
	stepPIDsBefore := daemonStepPIDs(wi, "m-e2e-b")
	if len(stepPIDsBefore) == 0 {
		t.Fatal("B's parked verification step has no live process to protect")
	}
	spawnsAtRelease := len(spawnJournal(t, capB))

	// A restarts later: its restart session verify sees the WI is held by
	// another session and releases — no resurrection, no double-run, no
	// hub transition, the capsule quarantined.
	daemonA2 := &v1Daemon{
		t: t, inst: inst, sock: daemonA.sock, dataDir: daemonA.dataDir, workspace: daemonA.workspace,
		machineID: daemonA.machineID, extraArgs: daemonA.extraArgs,
		logPath: filepath.Join(t.TempDir(), "daemonA2.log"),
	}
	t.Cleanup(func() { daemonA2.stop() })
	daemonA2.start()
	waitForEvent(t, daemonA2, wi, "session_released", 30*time.Second)
	for _, j := range daemonA2.status(t).Jobs {
		if j.WorkItemID == wi {
			t.Fatalf("restarted daemon A still holds a job for %s", wi)
		}
	}
	if st := inst.wiStatus(t, wi); st != stBefore {
		t.Fatalf("A's restart moved the hub state: %q, was %q", st, stBefore)
	}
	for _, pid := range stepPIDsBefore {
		if syscall.Kill(pid, 0) != nil {
			t.Fatalf("A's restart release killed B's live step process %d", pid)
		}
	}
	if s := inst.sessionOf(t, wi); s.Fingerprint != sessB.Fingerprint {
		t.Fatalf("A's restart touched B's session: %+v", s)
	}
	quarantined := false
	ents, _ := os.ReadDir(filepath.Join(daemonA.workspace, "capsules"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), wiPathName(t, inst, wi)+".quarantined.") {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatal("A's stale capsule was not quarantined")
	}
	// No double-run: B's chain-wide dispatch count did not move.
	time.Sleep(2 * time.Second)
	if n := len(spawnJournal(t, capB)); n != spawnsAtRelease {
		t.Fatalf("a dispatch appeared after A's restart: %d, want %d", n, spawnsAtRelease)
	}
}

// persistedSecret reads daemon d's session secret for wi out of its
// runtime-state.json.
func persistedSecret(t *testing.T, d *v1Daemon, wi string) string {
	t.Helper()
	var secret string
	waitUntil(t, 15*time.Second, "daemon never persisted its session secret", func() bool {
		raw, err := os.ReadFile(filepath.Join(d.dataDir, "runtime-state.json"))
		if err != nil {
			return false
		}
		var st struct {
			Jobs []struct {
				WorkItemID string `json:"work_item_id"`
				SessionID  string `json:"session_id"`
			} `json:"jobs"`
		}
		if json.Unmarshal(raw, &st) != nil {
			return false
		}
		for _, j := range st.Jobs {
			if j.WorkItemID == wi {
				secret = j.SessionID
			}
		}
		return secret != ""
	})
	return secret
}

// endSession force-ends the held session through the operator CLI
// (`pf ops end-session`, the human's admin key, aihub-direct: no daemon).
func endSession(t *testing.T, inst *v1Install, wi, reason string) {
	t.Helper()
	out := inst.pf(t, "ops", "end-session", wi, "--reason", reason)
	if !strings.Contains(out, "queued") || !strings.Contains(out, "pf submit") {
		t.Fatalf("end-session answer: %q", out)
	}
}

// assertStaleWritesRejected proves the old secret can no longer write:
// the git proxy refuses it GIT-401 (naming holderFP when one exists), a
// real git push fails authentication, and the session-fenced end verdict
// is refused without touching the work item.
func assertStaleWritesRejected(t *testing.T, inst *v1Install, wi, secret, holderFP string) {
	t.Helper()
	gitFace := inst.url + "/git/" + wi + ".git/info/refs?service=git-receive-pack"
	waitUntil(t, 30*time.Second, "old credential still accepted by the git face", func() bool {
		code, hdr, body := httpStatusOf("GET", gitFace, wi, secret)
		return code == 401 && strings.Contains(hdr.Get("X-PF-Git-Error"), "GIT-401") &&
			(holderFP == "" || strings.Contains(body, holderFP))
	})

	pushTmp := shortTmp(t, "pfm3push")
	gitRun(t, "", "init", "-q", "-b", "main", pushTmp)
	gitRun(t, pushTmp, "commit", "--allow-empty", "-m", "stale push attempt")
	askpass := filepath.Join(pushTmp, "askpass.sh")
	script := "#!/bin/sh\ncase \"$1\" in Username*) printf '%s\\n' '" + wi + "';; *) printf '%s\\n' '" + secret + "';; esac\n"
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "push", inst.url+"/git/"+wi+".git", "HEAD:refs/heads/main")
	cmd.Env = append(os.Environ(), "GIT_ASKPASS="+askpass, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("stale push was accepted: %s", out)
	}
	if !strings.Contains(string(out), "Authentication failed") && !strings.Contains(string(out), "401") {
		t.Fatalf("stale push failed for the wrong reason: %s", out)
	}

	before := inst.wiStatus(t, wi)
	err = inst.client.SessionEnd(wi, api.SessionEndRequest{SessionID: secret, Status: api.StatusFailed})
	if err == nil {
		t.Fatal("the old secret's session-end was accepted")
	}
	if got := apiReason(t, err); got != "STALE_SESSION" {
		t.Fatalf("stale session-end refused with %q, want STALE_SESSION", got)
	}
	if after := inst.wiStatus(t, wi); after != before {
		t.Fatalf("a refused stale end moved the WI: %s -> %s", before, after)
	}
}

// daemonStepPIDs lists live pids carrying BOTH the WI's and the daemon's
// env markers (the pair the daemon's orphan sweep keys on).
func daemonStepPIDs(wi, daemonID string) []int {
	var out []int
	for _, pid := range orphanPIDs(wi) {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(string(raw), "\x00") {
			if kv == "PF_DAEMON_ID="+daemonID {
				out = append(out, pid)
				break
			}
		}
	}
	return out
}
