package test

// w224_summarize_lane_acceptance_test.go: the polyforge#224 acceptance
// suite — the demand-gated transcript-delta watch summarize lane,
// authored against the SPEC (the capsule's artifacts/e2e-scenarios.md
// mirrors spec.md's Acceptance criteria and Scenario blocks) BEFORE the
// implementation lands, through the project's real e2e surfaces only:
// the pf/aihub/fakepi binaries, the `pf` CLI (the watch snapshot's
// --once --json face the panel reads), the daemon's UDS route, the
// workspace `.polyforge.yaml`, real git and the real filesystem — never
// implementation internals.
//
// Suite posture (artifacts/e2e-scenarios.md maps every scenario to its
// criteria): every new-behavior bar is RED on the pre-implementation
// tree — an assertion-level red (a submit refusal, an exit code, a
// missing JSON field), never a build red: this file compiles and vets
// against the tree as it stands today. The bars must go green when the
// change lands the spec.
//
// The dispatch fixture: the fake harness binary's one-shot summarize
// persona (test/fakepi/claude.go runOneShotSummarize — argv[1] == "-p"
// with no step identity and no session flag). The spec pins the dispatch
// as `pi -p --model <m>` / `claude -p <ctx> --model <m>` with the prompt
// on stdin, its config picked "the same way the step spawn picks it" —
// the persona's discriminator IS that bar: the dispatch must run the
// daemon's configured harness binary with the spec'd one-shot argv, or
// the persona never answers and the bars stay red.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w224Token is the credential sentinel the claude candidate's harness
// config carries (the settings.json env block — the single authority
// layer): it must never appear in a dispatch argv, the daemon log or any
// artifact.
const w224Token = "w224-SENTINEL-token"

// w224Snap is the four summarize fields of the watch snapshot JSON plus
// the presence anchors the scenarios assert — authored against the
// SPEC's wire shape (flat on the snapshot next to progress), never the
// implementation's Go types: the bars decode the CLI's --json output.
type w224Snap struct {
	Summary       *string `json:"summary"`
	SummaryError  *string `json:"summary_error"`
	SummaryAgeSec *int64  `json:"summary_age_sec"`
	SummaryCapped *bool   `json:"summary_capped"`
	Known         bool    `json:"known"`
	Execution     json.RawMessage
}

// w224PromptBytesRe picks the stdin prompt byte count out of the fake's
// summary line ("N prompt bytes on stdin").
var w224PromptBytesRe = regexp.MustCompile(`(\d+) prompt bytes on stdin`)

// w224Str answers a held string field's value for failure messages
// ("" when absent).
func w224Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// w224Pf runs the real pf CLI once against the daemon socket and answers
// (combined output, exit code, elapsed) with NO assertion: the baseline
// reds ARE exit codes and outputs.
func w224Pf(t *testing.T, inst *v1Install, sock string, args ...string) (string, int, time.Duration) {
	t.Helper()
	began := time.Now()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url,
		"PF_API_KEY="+inst.adminKey,
		"PF_USER_ID=usr_root",
		"PF_DAEMON_SOCKET="+sock,
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code, time.Since(began)
}

// w224SnapOf fetches one plain (unmarked) snapshot and decodes it.
func w224SnapOf(t *testing.T, inst *v1Install, d *v1Daemon, wi string) w224Snap {
	t.Helper()
	out, code, _ := w224Pf(t, inst, d.sock, "watch", wi, "--once", "--json")
	if code != 0 {
		t.Fatalf("pf watch %s --once --json: exit %d (%s)", wi, code, out)
	}
	return w224DecodeSnap(t, out)
}

// w224DecodeSnap decodes one --once --json output; anything that is not
// the snapshot JSON is a red.
func w224DecodeSnap(t *testing.T, out string) w224Snap {
	t.Helper()
	var s w224Snap
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("the watch snapshot JSON is undecodable: %v (%s)", err, out)
	}
	return s
}

// w224Demand issues the human-side marked request (`pf watch <wi>
// --once --demand --json`): the route must answer as a read (exit 0).
func w224Demand(t *testing.T, inst *v1Install, d *v1Daemon, wi string) string {
	t.Helper()
	out, code, _ := w224Pf(t, inst, d.sock, "watch", wi, "--once", "--demand", "--json")
	if code != 0 {
		t.Fatalf("pf watch %s --once --demand --json: exit %d (%s) — the marked snapshot request is a read any human-side caller may fetch", wi, code, out)
	}
	return out
}

// w224AwaitSummary polls plain --once --json fetches (unmarked — the
// polls themselves never arm the lane) until a summary or an error lands.
func w224AwaitSummary(t *testing.T, inst *v1Install, d *v1Daemon, wi string, bound time.Duration) w224Snap {
	t.Helper()
	deadline := time.Now().Add(bound)
	var last string
	for time.Now().Before(deadline) {
		s := w224SnapOf(t, inst, d, wi)
		last = "summary set / error set"
		if s.Summary != nil {
			return s
		}
		if s.SummaryError != nil {
			return s
		}
		last = "(no summary, no error)"
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("no summarize fact ever landed in the snapshot of %s within %v (%s; daemon log tail: %s)", wi, bound, last, d.logTail())
	return w224Snap{}
}

// w224AssertSummaryLine pins the held line's content contract and the
// dispatch facts the fake narrates: ONE plain-text line, at most 200
// characters, held with its age and no error, naming the model, a
// positive stdin prompt byte count (the prompt rides stdin, never
// argv), the context flag and no prompt in argv.
func w224AssertSummaryLine(t *testing.T, s w224Snap, wantModel, wantCtx string) string {
	t.Helper()
	if s.Summary == nil {
		t.Fatalf("no summary is held (%+v)", s)
	}
	line := *s.Summary
	if strings.Contains(line, "\n") {
		t.Fatalf("the held summary is not one line: %q", line)
	}
	if len(line) > 200 {
		t.Fatalf("the held summary exceeds 200 characters: %d (%q)", len(line), line)
	}
	if !strings.HasPrefix(line, "w224 fake summary: model="+wantModel+", ") {
		t.Fatalf("the summary does not name the dispatched model %q: %q", wantModel, line)
	}
	m := w224PromptBytesRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the summary does not report the stdin prompt bytes: %q", line)
	}
	if n := m[1]; n == "0" {
		t.Fatalf("the summarize prompt did not ride stdin (0 prompt bytes): %q", line)
	}
	if !strings.Contains(line, "ctx="+wantCtx) {
		t.Fatalf("the summary does not report context flag %q: %q", wantCtx, line)
	}
	if !strings.Contains(line, "argv=no-prompt") {
		t.Fatalf("the summarize prompt (or a chunk of it) rode the dispatch argv: %q", line)
	}
	if s.SummaryError != nil {
		t.Fatalf("a summary_error is held alongside a successful summary: %q", *s.SummaryError)
	}
	if s.SummaryCapped != nil {
		t.Fatalf("summary_capped is present below the cap: %v", *s.SummaryCapped)
	}
	if s.SummaryAgeSec == nil {
		t.Fatalf("no summary_age_sec is held alongside the summary: %q", line)
	}
	if *s.SummaryAgeSec < 0 {
		t.Fatalf("a negative summary age: %d", *s.SummaryAgeSec)
	}
	return line
}

// w224Yaml writes the workspace's `.polyforge.yaml` with the given
// top-level body (e.g. the root summarize key, polyforge#242).
func w224Yaml(t *testing.T, ws, content string) {
	t.Helper()
	body := "# w224: the workspace yaml under test\n" + content
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// w224RemoveYaml deletes the workspace's `.polyforge.yaml` (the
// absent-equivalent unconfigured state).
func w224RemoveYaml(t *testing.T, ws string) {
	t.Helper()
	if err := os.Remove(filepath.Join(ws, ".polyforge.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// w224NewWI files one autopilot execution ticket and scripts its agent
// with the plan function's actions (the plan may name the WI — the Run
// commands do).
func w224NewWI(t *testing.T, inst *v1Install, goal string, plan func(wi string) []map[string]any) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: v1Project, Goal: goal, BundleName: strPtr("autopilot"),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	inst.trackWI(wi.ID)
	inst.setPlan(t, wi.ID, plan(wi.ID))
	return wi.ID
}

// w224GrowCmd is the Run command that grows the step's live transcript
// (the harness session store's newest file — the same dir the daemon's
// shared locator scans) after sleepS seconds: the scripted stand-in for
// a turn a real agent appends mid-step.
func w224GrowCmd(sleepS int) string {
	return fmt.Sprintf(`sleep %d; for f in "$PI_CODING_AGENT_SESSION_DIR"/*.jsonl; do printf '%%s\n' '{"t":"turn","at":"w224","text":"scripted transcript growth"}' >> "$f"; done`, sleepS)
}

// w224AwaitRun polls the fake agent's journal until the Run command whose
// text contains needle has run to completion, and answers its entry.
func w224AwaitRun(t *testing.T, capDir, needle string, bound time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		for _, rec := range fakeJournal(t, capDir) {
			if rec["type"] == "run" && strings.Contains(fmt.Sprint(rec["cmd"]), needle) {
				return rec
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("the scripted Run command %q never completed within %v (journal: %+v)", needle, bound, fakeJournal(t, capDir))
	return nil
}

// w224HarnessRoot answers a harness root whose claude-config
// settings.json carries the credential sentinel in its env block (token
// auth mode — the 2026-10-19 ruling's tested combination: a gateway base
// URL, never the official URL).
func w224HarnessRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ccDir := filepath.Join(root, "claude-config")
	if err := os.MkdirAll(ccDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"env":{"ANTHROPIC_AUTH_TOKEN":"` + w224Token + `","ANTHROPIC_BASE_URL":"https://gw.w224.example"}}`
	if err := os.WriteFile(filepath.Join(ccDir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// w224OptArgs adds daemon argv flags to a v1Daemon.
func w224OptArgs(a ...string) daemonOpt {
	return func(d *v1Daemon) { d.extraArgs = append(d.extraArgs, a...) }
}

// w224ChatWatcherPolls runs the resident per-chat watcher (`pf watch
// --chat`, a 300ms poll) against the daemon for dur — its snapshot
// fetches are the resident watcher's polls the demand gate must never
// count — and answers its combined output.
func w224ChatWatcherPolls(t *testing.T, inst *v1Install, d *v1Daemon, wi, chat string, dur time.Duration) string {
	t.Helper()
	if out, code, _ := w224Pf(t, inst, d.sock, "watch", "subscribe", wi, "--chat", chat); code != 0 {
		t.Fatalf("pf watch subscribe %s --chat %s: exit %d (%s)", wi, chat, code, out)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(inst.pfPath, "--config", hermeticConfigPath(t), "watch", "--chat", chat, "--poll", "300ms")
	cmd.Env = append(os.Environ(), "HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "PF_DAEMON_SOCKET="+d.sock)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	time.Sleep(dur)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return out.String()
}

// w224GrepTree answers the first file under root whose content contains
// needle ("" = none).
func w224GrepTree(t *testing.T, root, needle string) string {
	t.Helper()
	var hit string
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if hit != "" || err != nil || e.IsDir() {
			return nil
		}
		if e.Type()&os.ModeSymlink != 0 {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr == nil && strings.Contains(string(raw), needle) {
			hit = path
		}
		return nil
	})
	return hit
}

// ---------------------------------------------------------------------------
// Scenario 1 — The panel's marked poll arms the lane.
// ---------------------------------------------------------------------------

// TestW224MarkedPollArmsTheLane pins the demand gate's core and the wire
// shape: with the lane configured and a step running, unmarked polls and
// the resident chat watcher's polls never start a call, a step-peer's
// demand=1 authorizes nothing (the route stays a read), and the human
// side's `--once --demand` starts exactly one background call whose
// plain line lands in the snapshot with its age. The 60 s interval and
// the transcript-growth gate then space the calls: a marked request
// within 60 s starts nothing, and after the interval passes a marked
// request on a still-ungrown transcript STILL starts nothing — until the
// transcript grows.
func TestW224MarkedPollArmsTheLane(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-good]\n")
	d.start()

	// fix's action first runs the step-peer's own marked snapshot request
	// (a step-tree child of the agent), then grows the transcript 75 s in.
	wi := w224NewWI(t, inst, "w224: the marked poll arms the summarize lane", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "run": []string{
				inst.pfPath + " watch " + wi + " --once --demand --json",
				w224GrowCmd(75),
			}, "ms": 30000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	capDir := d.capsuleDir(wi)
	t.Cleanup(func() { killOrphans(wi) })

	// Unmarked polls never arm the lane: the snapshot stays silent (no
	// summary, no error, no capped) however often the panel polls.
	for i := 0; i < 3; i++ {
		if s := w224SnapOf(t, inst, d, wi); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
			t.Fatalf("an unmarked poll armed the summarize lane: %+v", s)
		}
	}

	// The resident chat watcher's polls never count as demand either: a
	// few seconds of its 300 ms polling loop, then still nothing.
	w224ChatWatcherPolls(t, inst, d, wi, "w224chat", 4*time.Second)
	if s := w224SnapOf(t, inst, d, wi); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the resident chat watcher's polls armed the summarize lane: %+v", s)
	}

	// A step agent's demand flag authorizes nothing: the agent's own Run
	// command (a step process tree of this daemon) fetched the snapshot
	// with the marker — the route answered it as a read (exit 0, the
	// snapshot JSON) and no call started (its answer carries no summarize
	// fact, and none lands afterwards).
	peer := w224AwaitRun(t, capDir, "--demand", 20*time.Second)
	if code, _ := peer["exit_code"].(float64); code != 0 {
		t.Fatalf("the step-peer's marked snapshot request failed: exit %v (output %q) — the route must stay a read any caller may fetch", peer["exit_code"], peer["output"])
	}
	if s := w224DecodeSnap(t, fmt.Sprint(peer["output"])); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
		t.Fatalf("the step-peer's demand=1 started a summarize call: %+v", s)
	}
	time.Sleep(3 * time.Second)
	if s := w224SnapOf(t, inst, d, wi); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the step-peer's demand=1 recorded demand (a call landed): %+v", s)
	}

	// The human side's marked poll arms the lane: exactly one background
	// call, whose plain line lands in the next snapshot with its age.
	w224Demand(t, inst, d, wi)
	first := w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 15*time.Second), "e2e-w224-good", "none")

	// The 60 s interval: a marked request right after the call starts no
	// second call — the held line is unchanged (the fake's pid names the
	// dispatch).
	w224Demand(t, inst, d, wi)
	time.Sleep(2 * time.Second)
	if s := w224SnapOf(t, inst, d, wi); s.Summary == nil || *s.Summary != first {
		t.Fatalf("a second summarize call fired within 60 s of the first: the held line changed %q -> %q", first, w224Str(s.Summary))
	}

	// The transcript-growth gate: the interval has passed by the time the
	// scripted growth lands, but the transcript had not grown since the
	// call — a marked request STILL starts nothing (the held line is
	// unchanged) until the growth lands.
	w224AwaitRun(t, capDir, "PI_CODING_AGENT_SESSION_DIR", 85*time.Second)
	w224Demand(t, inst, d, wi)
	s := w224AwaitSummary(t, inst, d, wi, 15*time.Second)
	second := w224AssertSummaryLine(t, s, "e2e-w224-good", "none")
	if second == first {
		t.Fatalf("the grown transcript fired no new summarize call: the held line is unchanged %q", first)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2 — A failed call walks to the next candidate.
// ---------------------------------------------------------------------------

// TestW224FailedCallWalksToTheNextCandidate pins the failure wire and the
// candidate walk: the first call (the pi candidate) fails raw, the
// snapshot carries the RAW error text (never faked) and stays intact, a
// marked request within 60 s starts no second call, and once the
// interval passes and the transcript grows, the next call dispatches the
// NEXT candidate — the claude one, under the 2026-10-19 ruling's context
// flag for token auth mode (--bare), the official URL never contacted —
// and its success clears the error. No credential value appears in the
// dispatch argv, the daemon log or any artifact.
func TestW224FailedCallWalksToTheNextCandidate(t *testing.T) {
	inst := newV1Install(t)
	root := w224HarnessRoot(t)
	d := newV1Daemon(t, inst, w224OptArgs("--claude-bin", inst.fakepiPath, "--harness-root", root))
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-fail, claude:e2e-w224-claude]\n")
	d.start()

	wi := w224NewWI(t, inst, "w224: a failed call walks to the next candidate", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "run": []string{w224GrowCmd(75)}, "ms": 30000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	capDir := d.capsuleDir(wi)
	t.Cleanup(func() { killOrphans(wi) })

	// The first call fails raw: the snapshot carries the RAW error text
	// (the harness's own stderr wording, never faked), no summary, and
	// the failure breaks nothing (the snapshot stays whole).
	w224Demand(t, inst, d, wi)
	s := w224AwaitSummary(t, inst, d, wi, 15*time.Second)
	if s.Summary != nil {
		t.Fatalf("a summary is held for a failed call: %q", *s.Summary)
	}
	if s.SummaryError == nil {
		t.Fatalf("the failed call left no summary_error (%+v)", s)
	}
	rawErr := *s.SummaryError
	if !strings.Contains(rawErr, "w224 fake summarize dispatch failed: model=e2e-w224-fail") || !strings.Contains(rawErr, "exit 1 by plan") {
		t.Fatalf("summary_error is not the harness's raw error text: %q", rawErr)
	}
	if !s.Known || len(s.Execution) == 0 {
		t.Fatalf("the failed call broke the snapshot: %+v", s)
	}

	// The 60 s interval: a marked request right after the failure starts
	// no second call — the raw error text is unchanged.
	w224Demand(t, inst, d, wi)
	time.Sleep(2 * time.Second)
	if s2 := w224SnapOf(t, inst, d, wi); s2.SummaryError == nil || *s2.SummaryError != rawErr {
		t.Fatalf("a second summarize call fired within 60 s of the first: the error changed %q -> %q", rawErr, w224Str(s2.SummaryError))
	}

	// The walk: the interval passes, the transcript grows, and the next
	// call dispatches the NEXT candidate — the claude one, context flag
	// --bare (token auth mode per the ruling), its success clearing the
	// error.
	w224AwaitRun(t, capDir, "PI_CODING_AGENT_SESSION_DIR", 85*time.Second)
	w224Demand(t, inst, d, wi)
	w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 15*time.Second), "e2e-w224-claude", "--bare")

	// No credential value appears in the dispatch argv, the daemon log or
	// any artifact: the sentinel lives only in the settings.json the test
	// wrote (outside the capsule).
	if raw, err := os.ReadFile(d.logPath); err == nil && strings.Contains(string(raw), w224Token) {
		t.Fatalf("the credential sentinel leaked into the daemon log")
	}
	if hit := w224GrepTree(t, capDir, w224Token); hit != "" {
		t.Fatalf("the credential sentinel leaked into the capsule tree: %s", hit)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3 — The summarize key validates.
// ---------------------------------------------------------------------------

// TestW224SummarizeKeyValidates pins the model source: a claim under a
// yaml carrying the root `summarize.models` key (polyforge#242) validates
// (the WI runs), a WI claimed with no such key has the lane silently off
// (a marked request makes zero calls, no error in any face), the claim
// resolves the list at claim time (a yaml edit never reaches a running
// WI's lane), `pf doctor` reports no refusal and no new row for the
// summarize key, and any other unknown id still refuses DAEMON-414
// before the claim exactly as today.
func TestW224SummarizeKeyValidates(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-good]\n")
	d.start()

	// The summarize key validates: the submit and the claim pass, the
	// step runs.
	wi1 := w224NewWI(t, inst, "w224: the summarize key validates", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "ms": 45000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi1)
	waitStepStatus(t, d, wi1, "fix", "in_progress", 30*time.Second)
	t.Cleanup(func() { killOrphans(wi1) })

	// Unconfigured is silently off: the yaml loses the key (the
	// absent-equivalent state), the next claim runs fine, and the marked
	// request on that WI makes zero calls and no error anywhere.
	w224RemoveYaml(t, d.workspace)
	wi2 := w224NewWI(t, inst, "w224: unconfigured is silently off", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "ms": 45000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi2)
	waitStepStatus(t, d, wi2, "fix", "in_progress", 30*time.Second)
	t.Cleanup(func() { killOrphans(wi2) })
	out := w224Demand(t, inst, d, wi2)
	if s := w224DecodeSnap(t, out); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
		t.Fatalf("the unconfigured lane is not silently off: %+v (the four fields must simply stay absent)", s)
	}
	time.Sleep(3 * time.Second)
	if s := w224SnapOf(t, inst, d, wi2); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the unconfigured lane made a call or an error: %+v", s)
	}

	// The list resolves at claim time: the yaml gains the key back, but
	// the running WI's lane stays off (its claim-time list is empty; the
	// edit governs the next claim only).
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-good]\n")
	w224Demand(t, inst, d, wi2)
	time.Sleep(3 * time.Second)
	if s := w224SnapOf(t, inst, d, wi2); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("a yaml edit reached a running WI's summarize lane (the list must resolve at claim time): %+v", s)
	}

	// `pf doctor` gains no row and no refusal for the summarize key: with
	// the key present and valid, the doctor output mentions no summarize
	// refusal at all.
	cfgPath := filepath.Join(t.TempDir(), "w224-doctor.toml")
	w113ConfigFile(t, cfgPath, inst.url, inst.adminKey, d.sock, []w113WS{{key: d.workspace, projects: []string{"e2e"}}})
	docOut, _ := w113Pf(t, inst, w113Env{config: cfgPath}, "doctor")
	if strings.Contains(docOut, "summarize") {
		t.Fatalf("pf doctor reports the summarize key (a refusal or a new row): %s", docOut)
	}

	// Any other unknown id still refuses exactly as today: DAEMON-414
	// before the claim, the work item stays queued.
	w224Yaml(t, d.workspace, "steps:\n  e2e:\n    w224nope:\n      models: [pi:e2e-w224-good]\n")
	wi3 := w224NewWI(t, inst, "w224: an unknown id still refuses", func(wi string) []map[string]any {
		return nil
	})
	refusal, code, _ := w224Pf(t, inst, d.sock, "submit", wi3)
	if code != 1 {
		t.Fatalf("pf submit %s under an unknown per-step id: exit %d, want 1 (output %q)", wi3, code, refusal)
	}
	if !strings.Contains(refusal, "steps.e2e.w224nope") || !strings.Contains(refusal, "DAEMON-414") {
		t.Fatalf("the unknown per-step id did not refuse DAEMON-414 naming the key: %q", refusal)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4 — A restart re-arms fresh.
// ---------------------------------------------------------------------------

// TestW224RestartReArmsFresh pins the memory-only state: a summarize call
// lands before a hard daemon crash (kill -9 equals shutdown), the
// restarted daemon's recovery resumes the step, and the next marked
// request with growth re-summarizes from scratch (a NEW call — the
// restart reset the delta position and the interval clock). No state
// file and no watchers directory exists anywhere under the workspace.
func TestW224RestartReArmsFresh(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-good]\n")
	d.start()

	wi := w224NewWI(t, inst, "w224: a restart re-arms fresh", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "ms": 25000},                                          // spawn 2: the pre-crash window
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 25000}, // spawn 3: the resumed window
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	t.Cleanup(func() { killOrphans(wi) })

	// The pre-crash call lands.
	w224Demand(t, inst, d, wi)
	before := w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 15*time.Second), "e2e-w224-good", "none")

	// Crash hard (kill -9 equals shutdown): the whole lane state dies with
	// the process. The restarted daemon recovers the running work item and
	// resumes the step (a new dispatch — the cap counter starts over).
	d.crash()
	d2 := &v1Daemon{
		t: t, inst: inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, logPath: filepath.Join(t.TempDir(), "w224d2.log"),
	}
	t.Cleanup(d2.stop)
	d2.start()
	waitSpawnCount(t, d2, wi, 3, 60*time.Second)

	// The next watched growth re-summarizes from scratch: a marked request
	// on the restarted daemon fires a NEW call (the fresh lane's zero
	// position makes the resumed transcript a growth).
	w224Demand(t, inst, d2, wi)
	after := w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d2, wi, 25*time.Second), "e2e-w224-good", "none")
	if after == before {
		t.Fatalf("the restart re-armed nothing: the held line is unchanged %q (a fresh call must re-summarize from the zero position)", before)
	}

	// No state file, no watchers directory: the lane left nothing on disk.
	if _, err := os.Stat(filepath.Join(d.workspace, ".polyforge")); !os.IsNotExist(err) {
		t.Fatalf("a watchers state directory exists under the workspace: %v", err)
	}
	if hit := w224GlobState(t, d.workspace); hit != "" {
		t.Fatalf("a summarize state file exists under the workspace: %s", hit)
	}
}

// w224GlobState answers the first *.state file under root ("" = none).
func w224GlobState(t *testing.T, root string) string {
	t.Helper()
	var hit string
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if hit != "" || err != nil || e.IsDir() {
			return nil
		}
		if strings.HasSuffix(e.Name(), ".state") {
			hit = path
		}
		return nil
	})
	return hit
}

// ---------------------------------------------------------------------------
// Scenario 5 — A call in flight does not stall the snapshot.
// ---------------------------------------------------------------------------

// TestW224InFlightCallDoesNotStallTheSnapshot pins the background
// guarantee: the summarize dispatch is a "slow" model (12 s in flight),
// and every snapshot fetch that arrives while it is in flight — the
// marked request itself first, then plain fetches — answers immediately
// with the pre-call state, never waiting on the model call; the call
// lands afterwards (the landing is the flight's proof).
func TestW224InFlightCallDoesNotStallTheSnapshot(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:e2e-w224-slow]\n")
	d.start()

	wi := w224NewWI(t, inst, "w224: an in-flight call does not stall the snapshot", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "ms": 60000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	t.Cleanup(func() { killOrphans(wi) })

	// The marked request itself answers immediately with the pre-call
	// state: the route never waits on the 12 s model call it just armed.
	out, code, el := w224Pf(t, inst, d.sock, "watch", wi, "--once", "--demand", "--json")
	if code != 0 {
		t.Fatalf("pf watch %s --once --demand --json: exit %d (%s)", wi, code, out)
	}
	if el > 5*time.Second {
		t.Fatalf("the marked snapshot request waited %v on the in-flight summarize call (the route must answer immediately)", el)
	}
	if s := w224DecodeSnap(t, out); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the marked request answered the in-flight call's state, not the pre-call state: %+v", s)
	}

	// Plain fetches inside the flight: each answers immediately, each
	// carries the pre-call state.
	for i := 0; i < 2; i++ {
		fetchOut, fetchCode, fetchEl := w224Pf(t, inst, d.sock, "watch", wi, "--once", "--json")
		if fetchCode != 0 {
			t.Fatalf("pf watch %s --once --json: exit %d (%s)", wi, fetchCode, fetchOut)
		}
		if fetchEl > 5*time.Second {
			t.Fatalf("a snapshot fetch waited %v on the in-flight summarize call", fetchEl)
		}
		if s := w224DecodeSnap(t, fetchOut); s.Summary != nil || s.SummaryError != nil {
			t.Fatalf("a fetch inside the flight carried the in-flight call's state, not the pre-call state: %+v", s)
		}
	}

	// The call lands (the flight's proof): the slow model's line appears.
	w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 25*time.Second), "e2e-w224-slow", "none")
}
