// carries: workflows/_shared/scripts/facts.py, internal/polyforge/driver/*
// carries: workflows/develop/*, workflows/_shared/skills/*, workflows/_shared/steps/review.md

package test

// v2_12_develop_build_review_test.go (workflow contract v2 PR 12): the
// develop lane's build and review on the real daemon with the fake
// harness (test/fakepi as pi), the scripted agents running the real
// pre-written commands their skills name against fixture repos:
//
//   - TestV212ReviewReplaysRedAndFlagsWeakenedTest: build records two
//     units in artifacts/build/units.json — an honest one and one whose
//     test passes before its implementation; review runs `facts.py build
//     --replay --isolation host` (the skill's line) on a repo that declares
//     a verification container (a fake docker that logs every call, as the
//     release-gate tests' does): the replay runs on the host all the same
//     and no container is started; the honest unit replays red at its test
//     commit, the weak one exits 0, the command exits 1, and review FAILs
//     `test_weakened` naming it; TestV212ReviewReplaysOnTheHostWithoutAContainer
//     is the same on a repo with no Dockerfile.verify;
//   - TestV212SameTreeReviewAsks: a build round a FAIL opened completes on
//     the unchanged tree; review's re-entry finds every task repo at the
//     heads its last round judged (`last_review_round`) and asks the owner
//     (`pf step ask`) instead of judging again — the skill's rule, which
//     replaced the daemon's dispatch-time guard (A9: it keyed on the lane's
//     build_step, which workflow contract 3 removed); no second rewind;
//   - TestV212SameTreeVerificationAsks: verification FAILs a red, build
//     completes on the unchanged tree, review (a new tree for it: sync's
//     merge came after its last round) passes, sync brings nothing;
//     verification finds the earlier round's failed result at the current
//     HEAD and asks instead of running verify.py again;
//   - TestV212NoAcceptanceBuildAsksOnce: a ticket submitted without an
//     acceptance section — build writes the plan with the list it
//     proposes and asks once (`pf step ask`), the owner's free-text reply is
//     relayed into the same dispatch, which builds and completes.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestV212' -count=1 -v

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v212Docker is the verification container's fake engine: w404Docker with
// its state at a fixed path (a step's environment carries no CALLLOG):
// the image built from stdin, every --workdir exec run here with the
// image's PATH only.
func v212Docker(state string) string {
	return strings.ReplaceAll(w404Docker, "$CALLLOG", state)
}

// withVerifyContainer gives demo's main a verification container
// (.polyforge/Dockerfile.verify, pushed before the claim) and puts the
// fake docker first on the daemon's PATH; it answers the docker call log.
func (x *w374DevelopWorld) withVerifyContainer(t *testing.T) string {
	t.Helper()
	calls := filepath.Join(x.w.root, "docker.log")
	if err := os.WriteFile(filepath.Join(x.w.bin, "docker"), []byte(v212Docker(calls)), 0o755); err != nil {
		t.Fatal(err)
	}
	push := x.mainMoves(map[string]string{".polyforge/Dockerfile.verify": "FROM debian:stable-slim\n"}, "main: the verification container")
	if out, err := exec.Command("sh", "-c", push).CombinedOutput(); err != nil {
		t.Fatalf("push the Dockerfile: %v %s", err, out)
	}
	return calls
}

// v212WeakUnit writes u2 the dishonest way: its test passes before its
// implementation (it never checks anything).
const v212WeakUnit = `cd repos/demo && printf '#!/bin/sh\necho NOTES-PASS\n' > tests/notes.sh && git add -A && ` + w374Commit + ` -m 'u2: test' && T2=$(git rev-parse HEAD) && ` +
	`printf 'Notes.\n' > NOTES.md && git add -A && ` + w374Commit + ` -m 'u2: impl' && C2=$(git rev-parse HEAD) && sh tests/notes.sh && ` +
	`printf 'T2=%s\nC2=%s\n' "$T2" "$C2" > ../../artifacts/u2.env`

// v212UnitsBoth records u1 and u2 in artifacts/build/units.json.
const v212UnitsBoth = `. artifacts/u1.env && . artifacts/u2.env && mkdir -p artifacts/build && ` +
	`printf '[{"id":"u1","test_commit":"%s","impl_commit":"%s","run":{"repo":"demo","cwd":".","argv":["sh","tests/answer.sh"]},"marker":"^ANSWER-PASS$"},` +
	`{"id":"u2","test_commit":"%s","impl_commit":"%s","run":{"repo":"demo","cwd":".","argv":["sh","tests/notes.sh"]},"marker":"^NOTES-PASS$"}]' "$T" "$C" "$T2" "$C2" > artifacts/build/units.json`

// v212SameTree is the python a scripted agent applies its skill's
// same-tree rule with: review compares every task repo's HEAD with the
// end commits the daemon stamped on its last section of
// artifacts/handoff.md (workflow contract 4: a step never reads
// execution.json) and units.json's sha256 with the line its last round
// wrote in review.md; verification the heads of an earlier round's failed
// result and the line heading report.md. The same
// tree and units are asked about (`pf step ask`), anything else leaves a note
// for the next command.
const v212SameTree = `import hashlib, json, os, subprocess, sys
step = sys.argv[1]
heads, recorded = {}, ""
def line_of(path):
    if not os.path.exists(path):
        return ""
    for l in open(path):
        if l.startswith("units.json sha256: "):
            return l.split(": ", 1)[1].strip()
    return ""
q = chr(96)  # a backtick
def stamped_heads(step):
    # the repos' end commits on the step's last section of handoff.md
    out, mine = {}, False
    for l in open("artifacts/handoff.md") if os.path.exists("artifacts/handoff.md") else []:
        if l.startswith("## "):
            mine, cur = l.startswith("## " + step + " "), {}
            if mine:
                out = cur
        elif mine and l.startswith("- " + q) and q + " → " + q in l:
            repo = l[3:l.index(q, 3)]
            out[repo] = l.split(q + " → " + q, 1)[1].split(q, 1)[0]
    return out
if step == "review":
    heads, recorded = stamped_heads("review"), line_of("artifacts/review.md")
elif os.path.exists("artifacts/verify/result.json"):
    r = json.load(open("artifacts/verify/result.json"))
    if r.get("verdict") == "fail" and r.get("round") != os.environ.get("PF_ROUND"):
        heads, recorded = r.get("heads") or {}, line_of("artifacts/verify/report.md")
units = hashlib.sha256(open("artifacts/build/units.json", "rb").read()).hexdigest()
same = bool(heads) and recorded == units and all(subprocess.check_output(["git", "-C", "repos/" + n, "rev-parse", "HEAD"]).decode().strip() == h for n, h in heads.items())
if same:
    subprocess.check_call(["pf", "step", "ask", "same tree: every task repo stands where the last %s round judged it (%s) - judge again, take your ruling, or cancel?" % (step, ", ".join(sorted(heads)))])
else:
    open("artifacts/same-tree-%s.txt" % step, "w").write("a new tree\n")
`

// v212VerifyReport is the verify skill's report line: units.json's sha256
// heading artifacts/verify/report.md, whatever the exit.
const v212VerifyReport = `printf 'units.json sha256: %s\n' "$(sha256sum artifacts/build/units.json | cut -c1-64)" > artifacts/verify/report.md`

// v212World is newW374World with the same-tree helper beside the others.
func v212World(t *testing.T, ticket string) (*w374DevelopWorld, string, string) {
	t.Helper()
	x, wi, branch := newW374World(t, ticket)
	if err := os.WriteFile(filepath.Join(x.scripts, "same-tree.py"), []byte(v212SameTree), 0o644); err != nil {
		t.Fatal(err)
	}
	return x, wi, branch
}

// sameTree is the scripted agent's same-tree command.
func (x *w374DevelopWorld) sameTree(step string) string {
	return "python3 -I " + filepath.Join(x.scripts, "same-tree.py") + " " + step
}

// v212WaitAwaiting waits for the work item's first awaiting_owner event;
// at the deadline it reports what the scripted agents ran (command, exit,
// output tail) and the events, so a missed ask names its cause.
func v212WaitAwaiting(t *testing.T, d *v1Daemon, wi, desc string) {
	t.Helper()
	waitDeadline(t, 120*time.Second, func() string {
		var b strings.Builder
		b.WriteString(desc + "; the scripted runs:")
		for _, e := range fakeJournal(t, d.capsuleDir(wi)) {
			if e["type"] == "run" {
				out := fmt.Sprint(e["output"])
				if len(out) > 300 {
					out = out[len(out)-300:]
				}
				fmt.Fprintf(&b, "\n  [%v] %v exit %v: %s", e["step"], e["cmd"], e["exit_code"], out)
			}
		}
		b.WriteString("\nevents: " + strings.Join(d.eventKinds(t, wi), ","))
		return b.String()
	}, func() bool { return len(v26cAwaiting(t, d.dataDir, wi)) > 0 })
}

// v212Rewinds answers the rewind events' bodies.
func v212Rewinds(t *testing.T, d *v1Daemon, wi string) []string {
	t.Helper()
	var out []string
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "rewind" {
			out = append(out, string(e.Body))
		}
	}
	return out
}

func TestV212ReviewReplaysRedAndFlagsWeakenedTest(t *testing.T) {
	v212ReplayScenario(t, true)
}

func TestV212ReviewReplaysOnTheHostWithoutAContainer(t *testing.T) {
	v212ReplayScenario(t, false)
}

// v212ReplayScenario is the weakened-test scenario, with or without the
// repo's verification container.
func v212ReplayScenario(t *testing.T, container bool) {
	x, wi, _ := v212World(t, w374TicketNoStop)
	calls := ""
	if container {
		calls = x.withVerifyContainer(t)
	}
	x.inst.setPlanFull(t, wi, []map[string]any{
		// 1. build: u1 honest, u2 weak, both recorded in units.json
		buildAction([]string{x.planFiles(), w374Unit, v212WeakUnit, v212UnitsBoth, w374Output("u1 red then green; u2 notes")}),
		// 2. review: the skill's lines — the replay runs in the container
		x.reviewAction("facts"),
		// 3. build resumes on the FAIL: parked (the scenario ends here)
		{"do": "envelope", "id": "build", "park": true},
	}, nil)
	d := startDeliverDaemon(t, x.inst, x.w, wi)
	waitUntil(t, 300*time.Second, "review's FAIL rewinding build", func() bool { return len(v212Rewinds(t, d, wi)) > 0 })
	capDir := d.capsuleDir(wi)

	// the replay ran (exit 1: a unit is not red) on the host
	if exits := w374RunExits(t, capDir, w374FactsReplayLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("facts.py build --replay exits (u2 not red: 1): %v", exits)
	}
	bf := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "facts", "build.json"))
	bd, _ := bf["details"].(map[string]any)
	units, _ := bd["units"].([]any)
	replays := map[string]map[string]any{}
	for _, u := range units {
		m, _ := u.(map[string]any)
		r, _ := m["replay"].(map[string]any)
		replays[fmt.Sprint(m["id"])] = r
	}
	if bd["replay"] == nil || bf["verdict"] != "fail" || len(replays) != 2 || replays["u1"] == nil || replays["u1"]["red"] != true ||
		replays["u2"] == nil || replays["u2"]["red"] != false || replays["u2"]["exit_code"] != float64(0) {
		t.Fatalf("the replay: u1 red at its test commit, u2 exit 0 (not red): %v", bf)
	}
	for id, r := range replays {
		// the review skill's line replays on the host (--isolation host):
		// whether or not the repo declares a verification container
		if host, _ := r["host"].(bool); !host {
			t.Fatalf("unit %s replayed with host=%v, want the host (container declared %v): %v", id, r["host"], container, r)
		}
	}
	if !strings.Contains(fmt.Sprint(bf["summary"]), `unit "u2"`) || !strings.Contains(fmt.Sprint(bf["summary"]), "passes before its implementation") {
		t.Fatalf("the summary names u2: %v", bf["summary"])
	}
	if container {
		if log := v1ReadString(calls); strings.Contains(log, "docker exec") || strings.Contains(log, "docker run") {
			t.Fatalf("the review's replay started a container though its line says --isolation host: %q", log)
		}
	}
	fmt.Printf("PASS R1 review replayed each unit's red run on the host (container declared %v): u1 red, u2 not\n", container)

	// review FAILed test_weakened naming u2: one rewind to build
	rewinds := v212Rewinds(t, d, wi)
	if len(rewinds) != 1 || !strings.Contains(rewinds[0], `"gate":"review"`) || !strings.Contains(rewinds[0], `"target":"build"`) {
		t.Fatalf("one rewind review → build: %v", rewinds)
	}
	// review's hand-off (dispatch 2) is the gate's fail, its reason naming
	// u2 alone; the findings, classed, are in artifacts/review.md (the
	// review skill's file: the verdict, the units.json line, every finding)
	ho := acceptedHandoff(t, capDir, "review", 2)
	if ho.Handoff.Status != "fail" || !strings.Contains(ho.Handoff.Reason, "unit u2") || strings.Contains(ho.Handoff.Reason, "unit u1") {
		t.Fatalf("review must hand off fail naming u2 (not u1): %+v", *ho.Handoff)
	}
	if secs := w374Sections(t, capDir, "review"); len(secs) != 1 || secs[0].status != "fail" {
		t.Fatalf("review's one section of artifacts/handoff.md is a fail: %+v", secs)
	}
	review := v1ReadString(filepath.Join(capDir, "artifacts", "review.md"))
	var rr struct {
		Verdict  string           `json:"verdict"`
		Findings []map[string]any `json:"findings"`
	}
	if i := strings.Index(review, "{"); i < 0 || json.Unmarshal([]byte(review[i:]), &rr) != nil {
		t.Fatalf("artifacts/review.md carries no verdict and findings: %q", review)
	}
	flagged := false
	for _, m := range rr.Findings {
		if m["severity"] == "blocking" && m["class"] == "test_weakened" && strings.Contains(fmt.Sprint(m["detail"]), "unit u2") {
			flagged = true
		}
		if strings.Contains(fmt.Sprint(m["detail"]), "unit u1") {
			t.Fatalf("u1 replayed red: no finding names it: %v", m)
		}
	}
	if rr.Verdict != "fail" || !flagged {
		t.Fatalf("review must FAIL test_weakened naming u2: %s", review)
	}
	fmt.Println("PASS R2 the weakened unit is flagged test_weakened; one rewind")
}

func TestV212SameTreeReviewAsks(t *testing.T) {
	x, wi, _ := v212World(t, w374TicketNoStop)
	x.inst.setPlanFull(t, wi, []map[string]any{
		buildAction(w374Build("u1 red then green", x.planFiles())),
		// review FAILs (rewind 1)
		x.reviewAction("fail:ac_gap:answer.txt states the number without its meaning"),
		// build's round completes on the same tree (it changed nothing)
		buildAction([]string{w374Output("u1; nothing changed")}),
		// review's re-entry: the same tree — it asks instead of judging
		{"do": "envelope", "id": "review", "end_turn": true, "run": []string{x.sameTree("review")}},
	}, nil)
	d := startDeliverDaemon(t, x.inst, x.w, wi)
	v212WaitAwaiting(t, d, wi, "review's same-tree ask (awaiting_owner)")
	capDir := d.capsuleDir(wi)
	ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0])
	if ev["step"] != "review" || !strings.HasPrefix(fmt.Sprint(ev["question"]), "same tree: every task repo stands where the last review round judged it (demo)") {
		t.Fatalf("SYMPTOM: review must ask on the same tree: %v", ev)
	}
	x2 := readExecution(t, capDir)
	head := strings.TrimSpace(gitRun(t, filepath.Join(capDir, "repos", "demo"), "rev-parse", "HEAD"))
	doc, _ := os.ReadFile(filepath.Join(capDir, "artifacts", "handoff.md"))
	if !strings.Contains(string(doc), "## review · round 1") || !strings.Contains(string(doc), "` → `"+head+"`") {
		t.Fatalf("the review section's stamped end commit must be the tree the first round judged, the current HEAD %s:\n%s", head, doc)
	}
	if n, _ := x2.RewindStats["count"].(float64); n != 1 || x2.CurrentStep != "review" {
		t.Fatalf("the same tree spends no second rewind: %v, current %s", x2.RewindStats, x2.CurrentStep)
	}
	if exits := w374RunExits(t, capDir, w374FactsPlanLine); len(exits) != 1 {
		t.Fatalf("the re-entry judged nothing again (facts.py ran once): %v", exits)
	}
	fmt.Println("PASS S1 review's same-tree re-entry asks the owner, no second rewind")
}

func TestV212SameTreeVerificationAsks(t *testing.T) {
	x, wi, _ := v212World(t, w374TicketNoStop)
	// main gains a red check before sync: the sync merges it (a new tree
	// for review's next round), verify.py is red on it
	extra := x.mainMoves(map[string]string{
		".polyforge/verify-scope.conf": "stack=none\nfull_command=sh tests/answer.sh && sh tests/extra.sh\n",
		"tests/extra.sh":               "exit 1\n",
	}, "main: an extra check that is red")
	x.inst.setPlanFull(t, wi, []map[string]any{
		buildAction(w374Build("u1 red then green", x.planFiles())),
		x.reviewAction("facts", extra),
		x.syncAction(),
		// verification: red, FAILed to build (rewind 1)
		{"do": "envelope", "id": "verification", "outcome": "gate_failed", "no_started_at": true, "run": []string{x.sameTree("verification"), w374VerifyLine, v212VerifyReport},
			"output": map[string]any{"verdict": "fail", "fail_reason": "tests/extra.sh exit 1"}},
		// build's round completes on the same tree
		buildAction([]string{w374Output("u1; nothing changed")}),
		// review: a new tree for it (sync merged after its last round)
		{"do": "envelope", "id": "review", "outcome": "completed", "output_file": "artifacts/review-out.json", "no_started_at": true,
			"run": []string{x.sameTree("review"), w374FactsPlanLine, w374FactsReplayLine, w374FactsCarriesLine, "python3 -I " + filepath.Join(x.scripts, "review-route.py") + " facts"}},
		x.syncAction(),
		// verification's re-entry: the earlier round failed on this tree
		{"do": "envelope", "id": "verification", "end_turn": true, "run": []string{x.sameTree("verification")}},
	}, nil)
	d := startDeliverDaemon(t, x.inst, x.w, wi)
	v212WaitAwaiting(t, d, wi, "verification's same-tree ask (awaiting_owner)")
	capDir := d.capsuleDir(wi)
	ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0])
	if ev["step"] != "verification" || !strings.HasPrefix(fmt.Sprint(ev["question"]), "same tree: every task repo stands where the last verification round judged it (demo)") {
		t.Fatalf("SYMPTOM: verification must ask on the same tree: %v", ev)
	}
	if _, err := os.Stat(filepath.Join(capDir, "artifacts", "same-tree-review.txt")); err != nil {
		t.Fatalf("review's second round saw a new tree (sync merged after its first): %v", err)
	}
	if exits := w374RunExits(t, capDir, w374VerifyLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("verify.py ran once (red); the same tree is not run again: %v", exits)
	}
	res := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "verify", "result.json"))
	if c := w374Check(res, "demo:unit-run:u1"); c == nil || c["status"] != "pass" {
		t.Fatalf("the verification ran u1's recorded run (green: the red is main's check): %v", c)
	}
	x2 := readExecution(t, capDir)
	if n, _ := x2.RewindStats["count"].(float64); n != 1 || x2.CurrentStep != "verification" {
		t.Fatalf("the same tree spends no second rewind: %v, current %s", x2.RewindStats, x2.CurrentStep)
	}
	fmt.Println("PASS S2 verification's same tree is asked about, not run again; the units ran in the verification")
}

func TestV212NoAcceptanceBuildAsksOnce(t *testing.T) {
	const ticket = "Make the answer 42.\n"
	const question = "case (d): the ticket has no acceptance section. Proposed: AC1 tests/answer.sh passes: answer.txt carries 42. Build against it?"
	const words = "按你的建议来，就这一条"
	x, wi, _ := v212World(t, ticket)
	x.inst.setPlanFull(t, wi, []map[string]any{
		// the plan with the proposed list, one ask, the turn ends; the reply
		// is relayed into this dispatch, which builds and completes
		{"do": "envelope", "id": "build", "end_turn": true, "run": []string{x.planFiles(), "pf step ask " + shellQuote(question)},
			"on_message": buildAction(w374Build("u1 red then green; asked (d) with the proposed AC1, the owner said " + words + ": read as AC1 alone is the judged list"))},
		// review (the scenario ends once build completed)
		{"do": "envelope", "id": "review", "park": true},
	}, nil)
	d := newV1Daemon(t, x.inst, x.w.daemonEnv())
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	// pf submit refuses such a ticket without the override
	d.pf(t, "submit", wi, "--allow-no-acceptance")
	v212WaitAwaiting(t, d, wi, "build's (d) ask (awaiting_owner)")
	if ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0]); ev["step"] != "build" || ev["question"] != question {
		t.Fatalf("SYMPTOM: build must ask once with the proposed list: %v", ev)
	}
	if out := d.pf(t, "answer", wi, "--text", words, "--by", "usr_root"); !strings.Contains(out, "reply to ask") {
		t.Fatalf("pf answer: %s", out)
	}
	capDir := d.capsuleDir(wi)
	waitUntil(t, 120*time.Second, "build completed on the reply", func() bool {
		st := readExecution(t, capDir).Steps["build"]
		return st != nil && st.Status == "done"
	})
	var asks, replies int
	for _, l := range v26dAsks(t, capDir) {
		switch l.Kind {
		case "ask":
			asks++
		case "reply":
			replies++
		}
	}
	if asks != 1 || replies != 1 {
		t.Fatalf("one ask, one reply: asks %d replies %d", asks, replies)
	}
	if n := len(v26cAwaiting(t, d.dataDir, wi)); n != 1 {
		t.Fatalf("build asked once: %d awaiting_owner events", n)
	}
	// the asking dispatch itself handed off done, its 结论 carrying the
	// owner's words verbatim (the body build wrote, not the header's quote)
	if ho := acceptedHandoff(t, capDir, "build", 1); ho.Handoff.Status != "done" {
		t.Fatalf("build's dispatch 1 hands off done: %+v", *ho.Handoff)
	}
	secs := w374Sections(t, capDir, "build")
	if len(secs) != 1 || secs[0].status != "done" {
		t.Fatalf("build's one section of artifacts/handoff.md is done: %+v", secs)
	}
	_, conclusion, _ := strings.Cut(secs[0].text, "### 结论\n")
	conclusion, _, _ = strings.Cut(conclusion, "\n### ")
	if !strings.Contains(conclusion, words) {
		t.Fatalf("build's 结论 carries the owner's words verbatim: %q", conclusion)
	}
	if plan := v1ReadString(filepath.Join(capDir, "artifacts", "plan.md")); !strings.Contains(plan, "## Acceptance") || !strings.Contains(plan, "- AC1") {
		t.Fatalf("the plan carries the proposed list: %s", plan)
	}
	fmt.Println("PASS A1 a ticket without acceptance: build asks once at plan time, the reply completes it")
}
