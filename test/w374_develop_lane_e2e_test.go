// carries: internal/polyforge/driver/*, internal/polyforge/daemon/*, internal/polyforge/cli/*
// carries: workflows/develop/*, workflows/_shared/steps/*, workflows/_shared/skills/*, workflows/_shared/scripts/*

package test

// w374_develop_lane_e2e_test.go ( D2, rewritten by workflow
// contract v2 change 11, ported to workflow contract 4 by):
// the develop lane end to end through the real daemon + Go driver and the
// fake harness (test/fakepi as pi, the fake `gh`): build → review → sync →
// verification → publish → merge, every step an agent step ending its
// dispatch with one `pf step handoff` — the four mechanical ones scripted
// to run the real pre-written commands their skills name (the workflow
// scripts `sync.py`, `verify.py` and `merge.py` as `"$PF_PYTHON"
// "$PF_SHARED_DIR/scripts/<x>.py"` from the capsule's lane snapshot under
// the daemon's PF_PYTHON) against fixture repos, and to check what the step before them
// handed off the way their skills say: from the result files the commands
// write and the daemon's stamped headers in artifacts/handoff.md — never
// from runtime-state/execution.json.
//
// TestW374DevelopLaneEndToEnd drives build and review (no driver check,
// no notary, no sweep) —
//
//   - the confirm_plan stop is build's own `pf step ask` right after the
//     plan, answered in free text through the relay (`pf answer --text`)
//     into the same dispatch, before the first test commit;
//   - build runs its unit red then green itself and records it in
//     artifacts/build/units.json;
//   - review runs the skill's facts.py lines — `facts.py build --replay
//     --isolation host` replays u1's run on the host, red at its test
//     commit — and
//     hands off `fail` with its class: one charged rewind, build resumes;
//     no sweep follows a pass;
//
// — and then the delivery (downstream checks upstream); every scripted
// sync first runs its check (sync-check.py: the heads stamped on review's
// latest section, or its own earlier after), the reading it logs asserted:
//
//   - sync (the reviewed tree): origin's main moved with a conflicting
//     answer.txt; `sync.py` exits 1 leaving the merge in progress, the sync
//     agent resolves the file in place (wrongly: it drops the 42) and
//     the sync script's `--continue` line commits the merge, rewriting the result with
//     `resolved: [answer.txt]`;
//   - verification: `verify.py --tier light --unit-runs` exits 1 over the
//     resolved tree (the result file says fail, u1's own run among the
//     red checks) and the scripted agent LIES: it hands off done;
//   - publish checks the result file first and hands off `fail` to build
//     (charged: no owner message cited) — the red bar never reaches a PR;
//   - build fixes the file, review passes while main moves cleanly, sync
//     (the reviewed tree) makes a merge commit, verification passes,
//     publish pushes the branch and opens the PR; main then moves again,
//     conflicting with the branch's NOTES.md: the forge refuses the merge,
//     `merge.py` exits 1 and merge hands off `fail` to sync (charged);
//   - sync (its own earlier after — not the reviewed head) resolves
//     NOTES.md in place and continues, verification (u1's run green:
//     `demo:unit-run:u1`) and publish run again (the PR body lists
//     "conflict resolved in sync: NOTES.md"), `merge.py` lands the PR at
//     the re-verified head (`gh pr merge --merge --match-head-commit`) and
//     writes artifacts/merge/landing.json, the merge agent registers it
//     (`pf step hub wrap-attrs integration @artifacts/merge/landing.json`)
//     before its `done`, and the wrap carries it: aihub's attrs
//     `integration: {state: landed, repos: [{repo, pr, head,
//     light_verified_head, merge_commit}], merge_commits}`, which pf watch
//     shows; max_rewinds (3) exactly spent.
//
// TestV2DevelopVerificationRedAskedThenWaived: a red the agent cannot
// place — origin's main brought a failing check — makes the verification
// agent `pf step ask` and end its turn: the step parks awaiting the owner
// (the heavy-admission step's process ends at the ask); the owner's 放行
// resumes the session and the agent hands off `waive --per-owner <the
// reply's at>`; no rewind is charged, publish reads the waive stamped on
// the verification's section of the result's round beside the red result
// file, and the wrap carries the waiver in `attrs.waivers` (the owner's
// words verbatim, the agent's reason) — the landing itself names none.
//
// TestV2DevelopSyncRewindKeepsBuildsWork: a real conflict the sync agent
// will not resolve goes back to build through the owner: on the reply the
// agent runs the sync script's `--abort` line and hands off `rewind --to build
// --per-owner <the reply's at>`; build commits on a clean tree, and the
// next sync starts over from that commit — nothing discarded — and the
// lane lands it.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW374DevelopLane|^TestV2Develop' -count=1 -v

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

const w374Ticket = "Make the answer 42.\n\nconfirm_plan: true\n\n## Acceptance\n\n- AC1 tests/answer.sh passes: answer.txt carries 42\n"

// w374TicketNoStop is the ticket without the confirm_plan stop: build
// completes in one dispatch (the acceptance section is the judged list).
const w374TicketNoStop = "Make the answer 42.\n\n## Acceptance\n\n- AC1 tests/answer.sh passes: answer.txt carries 42\n"

const w374Plan = "# plan\n\n## Acceptance\n\n- AC1 tests/answer.sh passes: answer.txt carries 42\n\n## Non-goals\n\nnone\n\n## Units\n\n" +
	"1. **u1** — the answer (AC1)\n   - Touched files: `answer.txt` (write), `NOTES.md` (write)\n   - Coupled obligations: none — nothing lists the file\n\n" +
	"## Coverage\n\n- AC1 test: tests/answer.sh\n"

// w374Commit is the scripted agent's commit identity.
const w374Commit = `git -c user.name=fake-agent -c user.email=fake-agent@invalid commit -q`

// w374Unit writes u1 the honest way (workflow contract v2 PR 12: the
// session's own runs, no pf bar-run): the test committed alone and run
// red on that clean commit, then the implementation committed and the
// same command run green — the commits kept in artifacts/u1.env for this
// and later rounds. The test prints ANSWER-PASS on a pass (u1's marker).
const w374Unit = `cd repos/demo && mkdir -p tests && printf '#!/bin/sh\ngrep -q 42 answer.txt && echo ANSWER-PASS\n' > tests/answer.sh && ` +
	`git add -A && ` + w374Commit + ` -m 'u1: test (red)' && T=$(git rev-parse HEAD) && ! sh tests/answer.sh && ` +
	`echo 42 > answer.txt && git add -A && ` + w374Commit + ` -m 'u1: impl (green)' && C=$(git rev-parse HEAD) && sh tests/answer.sh && ` +
	`printf 'T=%s\nC=%s\n' "$T" "$C" > ../../artifacts/u1.env`

// w374Units records u1 in artifacts/build/units.json (the build skill's
// shape): the run exactly as it ran red and green, and its marker.
const w374Units = `. artifacts/u1.env && mkdir -p artifacts/build && ` +
	`printf '[{"id":"u1","test_commit":"%s","impl_commit":"%s","run":{"repo":"demo","cwd":".","argv":["sh","tests/answer.sh"]},"marker":"^ANSWER-PASS$"}]' "$T" "$C" > artifacts/build/units.json`

// w374Output writes build's output (its coverage and summary).
func w374Output(summary string) string {
	return `printf '{"coverage":[{"ac_id":"AC1","kind":"test","ref":"demo/tests/answer.sh: answer"}],"summary":"%s"}' "` + summary + `" > artifacts/build-out.json`
}

// w374Build is the scripted build's honest dispatch: the unit, its record
// and the output (extra commands run first).
func w374Build(summary string, first ...string) []string {
	return append(append([]string{}, first...), w374Unit, w374Units, w374Output(summary))
}

const w374BuildOut = "artifacts/build-out.json"

// The skills' command lines, verbatim (the scripted agents run exactly
// what the skills say; a line here that drifts from the skill is the
// prompt-render test's to catch).
const (
	w374SyncLine     = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --json --out artifacts/sync/result.json`
	w374SyncContinue = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --continue --json --out artifacts/sync/result.json`
	w374VerifyLine   = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify.py" --tier light --unit-runs artifacts/build/units.json --json --out artifacts/verify/result.json`
	w374MergeLine    = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish artifacts/publish.json --verified artifacts/verify/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json`
	// the merge skill's registration of the landing record merge.py wrote
	// (merge declares wrap_attrs: [integration]: its done is refused
	// until this line was accepted in the round)
	w374WrapAttrsLine = "pf step hub wrap-attrs integration @artifacts/merge/landing.json"
	w374AbortLine     = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --abort --json --out artifacts/sync/result.json`
	// the review skill's three lines (workflow contract v2 PR 12)
	w374FactsPlanLine    = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" plan-conformance --json --out artifacts/facts/plan-conformance.json`
	w374FactsReplayLine  = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --replay --isolation host --json --out artifacts/facts/build.json`
	w374FactsCarriesLine = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json`
)

// w374ReviewRoute is the python the scripted review agent classes the
// facts with (the review skill's step 4): a unit that does not resolve,
// whose commits are not test first, or whose replay is not red is a
// blocking test_weakened finding; argv[1] "fail:<class>:<detail>" adds
// the judgment's own blocking finding. review.md records the units.json
// it judged (the skill's `units.json sha256:` line).
const w374ReviewRoute = `import hashlib, json, sys
mode = sys.argv[1] if len(sys.argv) > 1 else "facts"
b = json.load(open("artifacts/facts/build.json"))
d = b.get("details") or {}
findings = []
for u in d.get("units") or []:
    r = u.get("replay")
    if u.get("unresolved") or u.get("commits") or (r is not None and not r.get("red")):
        how = "exited %s" % r.get("exit_code") if r is not None else "is not test first"
        findings.append({"severity": "blocking", "class": "test_weakened", "detail": "unit %s: its run %s on the tree of its test_commit (facts.py build --replay)" % (u.get("id"), how)})
if mode.startswith("fail:"):
    _, cls, detail = mode.split(":", 2)
    findings.append({"severity": "blocking", "class": cls, "ac_id": "AC1", "detail": detail})
blocking = [f for f in findings if f["severity"] == "blocking"]
out = {"verdict": "fail" if blocking else "pass", "findings": findings}
if blocking:
    out["fail_reason"] = "; ".join(f["detail"] for f in blocking)
units = hashlib.sha256(open("artifacts/build/units.json", "rb").read()).hexdigest()
open("artifacts/review.md", "w").write("# review\n\nunits.json sha256: " + units + "\n\n" + json.dumps(out, indent=1) + "\n")
json.dump(out, open("artifacts/review-out.json", "w"))
`

// w374SectionsPy is the python the scripted agents read artifacts/handoff.md
// with (the head of publish-check.py and sync-check.py) (the step header: a section's header is the daemon's stamp): every
// section of a step, oldest first — its round, its status and its text
// (the header's Task repo commits lines stamp each task repo's start → end commit,
// its `--per-owner` block quotes the owner message a status cited).
const w374SectionsPy = `import re
def sections(step):
    out = []
    for s in re.split(r"(?m)^## ", open("artifacts/handoff.md", encoding="utf-8").read()):
        m = re.match(r"(\S+) · round (\d+) · dispatch (\d+) · (\S+)", s)
        if m and m.group(1) == step:
            out.append({"round": m.group(2), "spawn": m.group(3), "status": m.group(4), "text": s})
    return out
def heads(section):
    return dict(re.findall(r"(?m)^- ` + "`([^`]+)`: `[^`]*` → `([0-9a-f]+)`" + `", section["text"]))
`

// w374SyncOut is the python the scripted sync agent restates the result
// file with (its hand-off's 结论: per repo before → after and, verbatim,
// the files it resolved).
const w374SyncOut = `import json
r = json.load(open("artifacts/sync/result.json"))
parts = ["%s %s -> %s (default %s), resolved in sync: %s" % (x["name"], x["before"], x.get("after", ""), x.get("default_head", ""), ", ".join(x.get("resolved") or []) or "none") for x in r["details"]["repos"]]
json.dump({"summary": "synced: " + r["summary"] + "; " + "; ".join(parts)}, open("artifacts/sync-out.json", "w"))
`

// w374MergeOut is the python the scripted merge agent routes with (the
// merge skill's step 3, on the result file): a pass hands off done — the
// landing record merge.py wrote is registered before it
// (w374RegisterLanding) — a refusal the FAIL naming the result's why.
const w374MergeOut = `import json
r = json.load(open("artifacts/merge/result.json"))
if r["verdict"] == "pass":
    out = {"verdict": "pass", "summary": r["summary"]}
else:
    out = {"verdict": "fail", "fail_reason": r["summary"] + "; " + "; ".join(x.get("why", "") for x in r["details"]["repos"])}
json.dump(out, open("artifacts/merge-out.json", "w"))
`

// w374RegisterLanding is the scripted merge agent's registration of the
// landing record, exactly the skill's line, on a landing only: merge.py
// writes artifacts/merge/landing.json on exit 0 alone and removes a stale
// one on any other exit, so the file is there exactly when this round's
// run landed every PR.
const w374RegisterLanding = "if [ -f artifacts/merge/landing.json ]; then " + w374WrapAttrsLine + "; else echo 'merge.py landed nothing: no landing record to register'; fi"

// w374PublishOut is the python the scripted publish agent checks first
// with (the publish skill): the verification's result file names every
// repo's HEAD, and says pass — or the verification's latest section of
// artifacts/handoff.md is stamped waive in the result's round and the
// owner message its header quotes for --per-owner accepts the red — and
// review's latest section is stamped done with artifacts/review.md saying
// pass; else the FAIL naming the source.
const w374PublishOut = w374SectionsPy + `import json, subprocess, os
v = json.load(open("artifacts/verify/result.json"))
waived = None
ver = sections("verification")
if ver and ver[-1]["status"] == "waive" and ver[-1]["round"] == str(v.get("round")):
    q = re.search(r"\*\*The owner message ` + "`--per-owner`" + ` cites\*\*\n\n- (\S+) [^\n]*\n\n(~{3,})\n(.*?)\n\2\n", ver[-1]["text"], re.S)
    if q and any(w in q.group(3) for w in ("放行", "ship it", "merge anyway")):
        waived = {"round": ver[-1]["round"], "per_owner": q.group(1), "words": q.group(3)}
rv = sections("review")
review = rv[-1]["status"] if rv else "absent"
rmd = open("artifacts/review.md", encoding="utf-8").read() if os.path.exists("artifacts/review.md") else ""
if review == "done" and not re.search(r'"verdict": "pass"', rmd):
    review = "done, but artifacts/review.md does not say pass"
moved = [n for n in sorted(os.listdir("repos")) if v["heads"].get(n) != subprocess.run(["git", "-C", "repos/" + n, "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()]
if moved:
    out = {"verdict": "fail", "fail_reason": "verify-first: artifacts/verify/result.json names another head for " + ", ".join(moved)}
elif v["verdict"] != "pass" and not waived:
    out = {"verdict": "fail", "fail_reason": "verify-first: artifacts/verify/result.json says " + v["verdict"] + " and the verification's section of its round is stamped " + (ver[-1]["status"] if ver else "nothing") + ", not a waive (the verification handed off done)"}
elif review != "done":
    out = {"verdict": "fail", "fail_reason": "verify-first: review's latest section is " + review}
else:
    out = {"verdict": "pass", "proceed": True, "waived": waived}
json.dump(out, open("artifacts/publish-check.json", "w"))
`

// w374Intent is the python a scripted agent finds the owner message its
// status carries out with (`intent.py <kind> <step> [<to>]` →
// artifacts/<step>-intent.json, which fakepi turns into the hand-off's
// status, --to and --per-owner): reply_at is the `at` of the owner's last
// reply or message to the step in runtime-state/asks.jsonl (the record the
// step header names; the scenario cannot know the stamp in advance), the
// reason its reading of the words.
const w374Intent = `import json, sys
kind, step = sys.argv[1], sys.argv[2]
at = ""
for line in open("runtime-state/asks.jsonl"):
    line = line.strip()
    if not line:
        continue
    l = json.loads(line)
    if l.get("kind") in ("reply", "message") and l.get("step") in (None, "", step):
        at = l["at"]
intent = {"kind": kind, "reply_at": at, "reason": "the owner's reply, read: " + kind}
if len(sys.argv) > 3:
    intent["to"] = sys.argv[3]
json.dump(intent, open("artifacts/" + step + "-intent.json", "w"))
`

// w374SyncCheck is the python the scripted sync agent checks first with
// (the sync skill): per task repo, HEAD is the tree review judged (the end
// commit stamped on review's latest section of artifacts/handoff.md) or
// this step's own earlier after (the result file a merge FAIL sent the
// lane back over). Each run appends its reading per repo to
// artifacts/sync-checks.log; a mismatch exits 1.
const w374SyncCheck = w374SectionsPy + `import json, os, subprocess, sys
rv = sections("review")
judged = heads(rv[-1]) if rv else {}
prior = {}
if os.path.exists("artifacts/sync/result.json"):
    for r in (json.load(open("artifacts/sync/result.json")).get("details") or {}).get("repos") or []:
        prior[r["name"]] = r.get("after") or ""
seen = []
for n in sorted(os.listdir("repos")):
    head = subprocess.run(["git", "-C", "repos/" + n, "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()
    if judged.get(n) == head:
        seen.append("reviewed")
    elif head and prior.get(n) == head:
        seen.append("own-after")
    else:
        seen.append("mismatch")
open("artifacts/sync-checks.log", "a").write(",".join(seen) + "\n")
sys.exit(1 if "mismatch" in seen else 0)
`

// w374DevelopWorld is one scenario's world: the deliver world (bare
// origins, the fake gh), the `pf-*` links beside it, the scope engine at
// the step command's default place and the scripted agents' python
// helpers (outside the capsule).
type w374DevelopWorld struct {
	w       *deliverWorld
	inst    *v1Install
	seed    string // the clone of demo's origin the world moves main from
	scripts string
}

// newW374World seeds demo's origin with the verification conf (stack none:
// the conf's command is the whole bar), installs the pf-* links the skills
// run and the scope engine where verify.py looks for it, and files the
// ticket on develop.
func newW374World(t *testing.T, ticket string) (*w374DevelopWorld, string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed (the scope engine's --json needs it)")
	}
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	seed := filepath.Join(w.root, "seed-demo")
	if err := os.MkdirAll(filepath.Join(seed, ".polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".polyforge", "verify-scope.conf"), []byte("stack=none\nfull_command=sh tests/answer.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "-c", "user.name=o", "-c", "user.email=o@invalid", "commit", "-q", "-m", "verify-scope conf")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	inst := newV1InstallRepos(t, []api.ProjectRepo{{Name: "demo", URL: origin}})
	inst.keepDeliver = true
	// no step command link: every line the develop skills run is a
	// workflow script (facts.py, sync.py, verify.py, merge.py) from the lane
	// snapshot
	// verify.py's default engine is the shared tree's scripts/verify-scope:
	// the lane snapshot every capsule carries holds it, nothing to install
	scripts := filepath.Join(w.root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"sync-out.py": w374SyncOut, "merge-out.py": w374MergeOut, "publish-check.py": w374PublishOut, "intent.py": w374Intent, "sync-check.py": w374SyncCheck, "review-route.py": w374ReviewRoute, "plan.md": w374Plan, "self-review.md": "# self-review\n\nu1 red then green.\n"} {
		if err := os.WriteFile(filepath.Join(scripts, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content := ticket
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: v1Project, Goal: "w374: the develop lane end to end", Content: &content, BundleName: strPtr("develop"),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("create answer: %v (%s)", err, raw)
	}
	inst.trackWI(item.ID)
	return &w374DevelopWorld{w: w, inst: inst, seed: seed, scripts: scripts}, item.ID, "polyforge/" + wiPathName(t, inst, item.ID)
}

// mainMoves commits files on demo's main (through the seed clone) and
// pushes: the world acting while the lane runs.
func (x *w374DevelopWorld) mainMoves(files map[string]string, msg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s && ", x.seed)
	for p, c := range files {
		fmt.Fprintf(&b, "mkdir -p %s && printf '%%s' %s > %s && ", filepath.Dir(p), shellQuote(c), p)
	}
	fmt.Fprintf(&b, "git add -A && git -c user.name=o -c user.email=o@invalid commit -q -m %s && git push -q origin HEAD:refs/heads/main", shellQuote(msg))
	return b.String()
}

// reviewAction is the scripted review agent: the skill's three facts.py
// lines as written, then the route (review-route.py with mode).
func (x *w374DevelopWorld) reviewAction(mode string, world ...string) map[string]any {
	act := map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output_file": "artifacts/review-out.json", "no_started_at": true,
		"run": []string{w374FactsPlanLine, w374FactsReplayLine, w374FactsCarriesLine,
			"python3 -I " + filepath.Join(x.scripts, "review-route.py") + " " + shellQuote(mode)}}
	if len(world) > 0 {
		act["world"] = world
	}
	return act
}

// buildAction is the scripted build's dispatch: commands, then the output.
func buildAction(run []string, world ...string) map[string]any {
	act := map[string]any{"do": "envelope", "id": "build", "outcome": "completed", "output_file": w374BuildOut, "no_started_at": true, "run": run}
	if len(world) > 0 {
		act["world"] = world
	}
	return act
}

// planFiles copies the scripted analysis's plan and self-review into the
// capsule (one command: an end_turn action runs commands only).
func (x *w374DevelopWorld) planFiles() string {
	return "cp " + filepath.Join(x.scripts, "plan.md") + " artifacts/plan.md && cp " + filepath.Join(x.scripts, "self-review.md") + " artifacts/self-review.md"
}

// syncAction is the scripted sync agent: its check first (sync-check.py),
// the skill's line, then its hand-off's summary restated from the result
// file (extra commands run between the two: the resolution of a real
// conflict and the continue).
func (x *w374DevelopWorld) syncAction(between ...string) map[string]any {
	run := append([]string{x.syncCheck(), w374SyncLine}, between...)
	run = append(run, "python3 -I "+filepath.Join(x.scripts, "sync-out.py"))
	return map[string]any{"do": "envelope", "id": "sync", "outcome": "completed", "output_file": "artifacts/sync-out.json", "no_started_at": true, "run": run}
}

// syncCheck is the scripted sync agent's check-first command line.
func (x *w374DevelopWorld) syncCheck() string {
	return "python3 -I " + filepath.Join(x.scripts, "sync-check.py")
}

// intent answers the command line that writes the owner message a
// scripted agent's status carries out (w374Intent) into
// artifacts/<step>-intent.json (fakepi's --status / --to / --per-owner).
func (x *w374DevelopWorld) intent(kind, step string, to ...string) string {
	return strings.TrimSpace("python3 -I " + filepath.Join(x.scripts, "intent.py") + " " + kind + " " + step + " " + strings.Join(to, " "))
}

// verifyAction is the scripted verification agent running the skill's
// line and handing off on the given verdict (a lie when it says pass over
// a red result file: done).
func verifyAction(output map[string]any, extra ...string) map[string]any {
	return map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "output": output, "no_started_at": true,
		"files": map[string]string{"artifacts/verify/report.md": "# verification\n\nscripted report\n"},
		"run":   append([]string{w374VerifyLine}, extra...)}
}

// publishAction is the scripted publish agent: its checks first
// (publish-check.py: the result file, the verification's waive of that
// round, review's done), then — when they hold — the push and the PR
// exactly as the skill says, the publish.json the merge step reads and the
// delivery note; a check that does not hold is the gate's `fail` (fakepi
// hands a gate's fail verdict off as --status fail).
func (x *w374DevelopWorld) publishAction(branch string, world ...string) map[string]any {
	check := "python3 -I " + filepath.Join(x.scripts, "publish-check.py")
	pub := `set -e; cap="$PWD"; cd repos/demo; git fetch -q origin; ` +
		`if grep -q '"proceed": true' "$cap/artifacts/publish-check.json"; then ` +
		`r=$(git ls-remote origin refs/heads/$b | cut -f1); ` +
		`if [ -z "$r" ] || git merge-base --is-ancestor "$r" HEAD; then git push -q origin HEAD:refs/heads/$b; else echo 'branch moved outside the lane' >&2; exit 1; fi; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`printf '# PR body\n\nconflict resolved in sync: %s\n' "$(python3 -I -c 'import json; print(", ".join(f for r in json.load(open("'"$cap"'/artifacts/sync/result.json"))["details"]["repos"] for f in r.get("resolved", [])) or "none")')" > "$cap/artifacts/publish-pr-demo.md"; ` +
		`if [ -n "$url" ]; then gh pr edit "$url" --body-file "$cap/artifacts/publish-pr-demo.md"; else url=$(gh pr create --base main --head "$b" --title "w374 publish" --body-file "$cap/artifacts/publish-pr-demo.md"); fi; ` +
		`[ -n "$url" ] || url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`h=$(git rev-parse HEAD); ` +
		`printf '{"repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}]}' "$b" "$url" "$h" > "$cap/artifacts/publish.json"; ` +
		`cp "$cap/artifacts/publish-pr-demo.md" "$cap/artifacts/delivery.md"; ` +
		`printf '{"verdict":"pass","repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}],"delivery_note":"scripted publish"}' "$b" "$url" "$h" > "$cap/artifacts/publish-output.json"; ` +
		`else cp "$cap/artifacts/publish-check.json" "$cap/artifacts/publish-output.json"; fi`
	act := map[string]any{"do": "envelope", "id": "publish", "outcome": "completed", "output_file": "artifacts/publish-output.json", "no_started_at": true,
		"run": literalBranch(branch, check, pub)}
	if len(world) > 0 {
		act["world"] = literalBranch(branch, world...)
	}
	return act
}

// mergeAction is the scripted merge agent: the skill's line, the landing
// record registered when it landed (w374RegisterLanding), then the route
// (merge-out.py) — done on a landing, the gate's fail on a refusal.
func (x *w374DevelopWorld) mergeAction() map[string]any {
	return map[string]any{"do": "envelope", "id": "merge", "outcome": "completed", "output_file": "artifacts/merge-out.json", "no_started_at": true,
		"run": []string{w374MergeLine, w374RegisterLanding, "python3 -I " + filepath.Join(x.scripts, "merge-out.py")}}
}

// runExits answers the exit codes the fake journal recorded for the
// scripted commands containing cmd, in order.
func w374RunExits(t *testing.T, capDir, cmd string) []int {
	t.Helper()
	var out []int
	for _, e := range fakeJournal(t, capDir) {
		if e["type"] == "run" && strings.Contains(fmt.Sprint(e["cmd"]), cmd) {
			out = append(out, int(e["exit_code"].(float64)))
		}
	}
	return out
}

// w374Check answers a step-result file's details.checks[] entry of that id
// (nil when absent).
func w374Check(res map[string]any, id string) map[string]any {
	d, _ := res["details"].(map[string]any)
	cs, _ := d["checks"].([]any)
	for _, c := range cs {
		if m, _ := c.(map[string]any); m != nil && m["id"] == id {
			return m
		}
	}
	return nil
}

func w374ReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// w374Ledger reads the capsule's call ledger (runtime-state/step-calls.jsonl).
func w374Ledger(t *testing.T, capDir string) []stepcall.Line {
	t.Helper()
	lines, err := stepcall.Read(capDir)
	if err != nil {
		t.Fatalf("the call ledger: %v", err)
	}
	return lines
}

// w374Section is one hand-off section of artifacts/handoff.md: its stamped
// status and its whole text (header and body).
type w374Section struct {
	status string
	text   string
}

// w374Sections answers step's hand-off sections of artifacts/handoff.md,
// oldest first (a section opens with the daemon's `## <step> · round N ·
// dispatch M · <status>` line).
func w374Sections(t *testing.T, capDir, step string) []w374Section {
	t.Helper()
	doc := v1ReadString(filepath.Join(capDir, "artifacts", "handoff.md"))
	var out []w374Section
	for _, part := range strings.Split(doc, "\n## ") {
		first, _, _ := strings.Cut(part, "\n")
		if !strings.HasPrefix(first, step+" · round ") {
			continue
		}
		fields := strings.Split(first, " · ")
		out = append(out, w374Section{status: strings.TrimSpace(fields[len(fields)-1]), text: part})
	}
	return out
}

func TestW374DevelopLaneEndToEnd(t *testing.T) {
	x, wi, branch := newW374World(t, w374Ticket)
	inst, w := x.inst, x.w
	// the world's conflicting answer.txt on main: pushed while review
	// passes, before sync runs (an add/add conflict with the branch's file)
	conflict := x.mainMoves(map[string]string{"answer.txt": "forty-two\n"}, "main: the answer in words")
	// the sync agent's (wrong) resolution: it keeps main's words and drops
	// the 42 — the verification right after is red on it
	resolve := `cd repos/demo && printf 'forty-two\n' > answer.txt && git add answer.txt`
	// main moves again, cleanly, while review judges the fixed tree: the
	// second sync makes a merge commit (its after is not the reviewed head)
	other := x.mainMoves(map[string]string{"other.txt": "other\n"}, "main: another file")
	// and once more right after the second publish, conflicting with the
	// branch's NOTES.md: the forge refuses to merge the PR, the merge FAILs
	// to sync, whose verify-first then accepts its own earlier after
	notes := x.mainMoves(map[string]string{"NOTES.md": "Main notes.\n"}, "main: notes of its own")
	resolveNotes := `cd repos/demo && printf 'Notes. The answer is 42.\nMain notes.\n' > NOTES.md && git add NOTES.md`

	const planQuestion = "confirm_plan: Acceptance AC1 (tests/answer.sh passes); unit u1 writes answer.txt test-first. Proceed with this plan?"
	const ownerWords = "按你的建议来"

	inst.setPlanFull(t, wi, []map[string]any{
		// 1. the confirm_plan stop: the plan written, build's own pf step ask,
		// the turn ends; the owner's reply is relayed into this dispatch,
		// which then builds u1 (after the reply) and completes
		{"do": "envelope", "id": "build", "end_turn": true, "run": []string{x.planFiles(), "pf step ask " + shellQuote(planQuestion)},
			"on_message": buildAction(w374Build("u1 red then green; asked confirm_plan, the owner said " + ownerWords + ": read as proceed with the plan as written"))},
		// 2. review hands off fail with a classed blocking finding (rewind 1)
		x.reviewAction("fail:ac_gap:answer.txt states the number without its meaning"),
		// 3. build resumes and fixes it (NOTES.md documents the answer)
		buildAction([]string{`cd repos/demo && echo 'the answer is 42' >> answer.txt && printf 'Notes. The answer is 42.\n' > NOTES.md && git add -A && ` + w374Commit + ` -m 'u1: say what 42 means'`,
			w374Units, w374Output("u1; answer.txt now says what 42 means (review s2#2)")}),
		// 4. review passes (no sweep follows); meanwhile main moves
		x.reviewAction("facts", conflict),
		// 5. sync: sync.py leaves the real conflict in progress (exit 1),
		// the agent resolves it in place and continues the same command
		x.syncAction(resolve, w374SyncContinue),
		// 6. verification: the result file says fail (no 42 any more);
		// the agent lies: it hands off done
		verifyAction(map[string]any{"verdict": "pass", "summary": "a lying verification: the result file says fail"}),
		// 7. publish checks the result file first: fail to build (rewind 2)
		x.publishAction(branch),
		// 8. build fixes the resolution (keeps both)
		buildAction([]string{`cd repos/demo && printf '42\nforty-two\nthe answer is 42\n' > answer.txt && git add -A && ` + w374Commit + ` -m 'u1: keep the 42 with main''s words (publish s7#1)'`,
			w374Units, w374Output("u1; the sync resolution lost the 42, restored (publish s7#1)")}),
		// 9. review passes; main moves cleanly meanwhile
		x.reviewAction("facts", other),
		// 10. sync: its check on the reviewed tree, a clean merge commit
		x.syncAction(),
		// 11. verification passes for real (u1's run green again)
		verifyAction(map[string]any{"verdict": "pass", "summary": "light verification passed"}),
		// 12. publish pushes and opens the PR; main then moves, conflicting
		x.publishAction(branch, notes),
		// 13. merge: the forge refuses (a conflict with the moved main):
		// merge.py exit 1, fail to sync (rewind 3)
		x.mergeAction(),
		// 14-16. sync (its check on its own earlier after; the real
		// conflict in NOTES.md resolved in place and continued),
		// verification and publish again
		x.syncAction(resolveNotes, w374SyncContinue),
		verifyAction(map[string]any{"verdict": "pass", "summary": "light verification passed over the second resolution"}),
		x.publishAction(branch),
		// 17. merge lands the PR at the verified head, registers the
		// landing record and hands off done
		x.mergeAction(),
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	// D1. the confirm_plan stop is build's own ask, right after the plan:
	// awaiting the owner, answered in free text, relayed into the dispatch
	waitUntil(t, 120*time.Second, "build's confirm_plan ask (awaiting_owner)", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) > 0
	})
	if ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0]); ev["step"] != "build" || ev["question"] != planQuestion {
		t.Fatalf("SYMPTOM: the first stop must be build's confirm_plan ask: %v", ev)
	}
	if out := d.pf(t, "answer", wi, "--text", ownerWords, "--by", "usr_root"); !strings.Contains(out, "reply to ask") {
		t.Fatalf("pf answer: %s", out)
	}
	fmt.Println("PASS D1 confirm_plan stop asked by build (pf step ask), answered in free text")

	waitWorkflowStatus(t, d, wi, "completed", 600*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x2 := readExecution(t, capDir)

	// nothing was refused: every `pf step` call of the run (each hand-off,
	// each hub write) was accepted at the call — the daemon judges no
	// build content
	ledger := w374Ledger(t, capDir)
	for _, l := range ledger {
		if l.Kind == stepcall.KindResult && l.Outcome != stepcall.OutcomeOK {
			t.Fatalf("SYMPTOM: a pf step call was not accepted: %+v", l)
		}
	}
	// the reply precedes u1's test commit (review's confirm_plan duty)
	var replyAt string
	asks := 0
	for _, l := range v26dAsks(t, capDir) {
		switch {
		case l.Kind == "ask" && l.Step == "build":
			asks++
		case l.Kind == "reply" && replyAt == "":
			replyAt = l.At
		}
	}
	env := v1ReadString(filepath.Join(capDir, "artifacts", "u1.env"))
	tc := strings.TrimPrefix(strings.SplitN(env, "\n", 2)[0], "T=")
	repo := filepath.Join(capDir, "repos", "demo")
	committed, err1 := time.Parse(time.RFC3339, strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%cI", tc)))
	replied, err2 := time.Parse(time.RFC3339Nano, replyAt)
	if asks != 1 || err1 != nil || err2 != nil || committed.Before(replied.Truncate(time.Second)) {
		t.Fatalf("one confirm_plan ask, its reply before u1's test commit: asks %d, reply %q (%v), test commit %s at %v (%v)", asks, replyAt, err2, tc, committed, err1)
	}
	fmt.Println("PASS D2 the owner's reply precedes the first test commit")

	// D3. units.json records u1's run
	var units []map[string]any
	if err := json.Unmarshal([]byte(v1ReadString(filepath.Join(capDir, "artifacts", "build", "units.json"))), &units); err != nil || len(units) != 1 ||
		units[0]["id"] != "u1" || units[0]["test_commit"] != tc || units[0]["marker"] != "^ANSWER-PASS$" {
		t.Fatalf("artifacts/build/units.json must record u1 (test commit %s): %v %v", tc, units, err)
	}
	fmt.Println("PASS D3 build recorded its unit's run in units.json")

	// D4. review ran the skill's lines: the demo repo has no verification
	// container, so u1's run replayed on the host (as verify.py runs the
	// bar), red at its test commit; its fail with a class rewound build;
	// D5. no sweep after a pass
	if hs := stepcall.StepHandoffs(ledger, "review"); len(hs) != 3 || hs[0].Handoff.Status != stepcall.StatusFail || !strings.Contains(hs[0].Handoff.Reason, "answer.txt states the number without its meaning") ||
		hs[1].Handoff.Status != stepcall.StatusDone || hs[2].Handoff.Status != stepcall.StatusDone {
		t.Fatalf("review's hand-offs: its classed fail, then two passes: %+v", hs)
	}
	if exits := w374RunExits(t, capDir, w374FactsPlanLine); len(exits) != 3 {
		t.Fatalf("facts.py plan-conformance must run in each of the three reviews: %v", exits)
	}
	if exits := w374RunExits(t, capDir, w374FactsReplayLine); len(exits) != 3 || exits[0] != 0 || exits[1] != 0 || exits[2] != 0 {
		t.Fatalf("facts.py build --replay exits (u1 red at its test commit each time): %v", exits)
	}
	bf := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "facts", "build.json"))
	bd, _ := bf["details"].(map[string]any)
	bu, _ := bd["units"].([]any)
	u1, _ := func() (map[string]any, bool) {
		if len(bu) != 1 {
			return nil, false
		}
		m, ok := bu[0].(map[string]any)
		return m, ok
	}()
	rp, _ := u1["replay"].(map[string]any)
	if bf["command"] != "facts.py" || bf["verdict"] != "pass" || bd["replay"] == nil || rp == nil || rp["red"] != true || rp["host"] != true {
		t.Fatalf("artifacts/facts/build.json: u1 replayed red on the host: %v", bf)
	}
	for _, a := range x2.Steps["build"].Attempts {
		if c, _ := a["cause"].(string); strings.Contains(c, "sweep") {
			t.Fatalf("SYMPTOM: a sweep ran (develop declares none since PR 12): %v", x2.Steps["build"].Attempts)
		}
	}
	for _, e := range d.eventsOf(t, wi) {
		if strings.HasPrefix(e.Kind, "sweep") {
			t.Fatalf("SYMPTOM: a sweep event: %s %s", e.Kind, e.Body)
		}
	}
	fmt.Println("PASS D4 review's classed fail rewinds build (facts.py ran); D5 no sweep")

	// the sync agent: its check accepted the reviewed tree (the heads
	// stamped on review's latest section) twice, then — after the merge
	// fail — its own earlier after;
	// sync.py left a real conflict twice (exit 1), each resolved in place
	// and continued (exit 0), and merged main cleanly once
	checks, _ := os.ReadFile(filepath.Join(capDir, "artifacts", "sync-checks.log"))
	if got := strings.Fields(string(checks)); strings.Join(got, " ") != "reviewed reviewed own-after" {
		t.Fatalf("SYMPTOM: sync's check-first readings (review, review, its own after) = %v", got)
	}
	if exits := w374RunExits(t, capDir, w374SyncLine); len(exits) != 3 || exits[0] != 1 || exits[1] != 0 || exits[2] != 1 {
		t.Fatalf("sync.py exits (a real conflict, a clean merge, a real conflict): %v", exits)
	}
	if exits := w374RunExits(t, capDir, w374SyncContinue); len(exits) != 2 || exits[0] != 0 || exits[1] != 0 {
		t.Fatalf("sync.py --continue exits: %v", exits)
	}
	sres := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "sync", "result.json"))
	srepos, _ := sres["details"].(map[string]any)["repos"].([]any)
	s0, _ := srepos[0].(map[string]any)
	if s0 == nil || fmt.Sprint(s0["resolved"]) != "[NOTES.md]" || fmt.Sprint(s0["conflicts"]) != "[]" || s0["after"] != strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD")) {
		t.Fatalf("the last sync result lists the hand-resolved file and its merge: %v", s0)
	}
	// each sync handed off done, and its section of artifacts/handoff.md
	// (the result file is the last run's) restates what it resolved: the
	// first one answer.txt, the last one NOTES.md
	if syncs := w374Sections(t, capDir, "sync"); len(syncs) != 3 || !strings.HasSuffix(syncs[0].status, "done") || !strings.Contains(syncs[0].text, "resolved in sync: answer.txt") ||
		!strings.Contains(syncs[1].text, "resolved in sync: none") || !strings.Contains(syncs[2].text, "resolved in sync: NOTES.md") {
		t.Fatalf("the sync sections of artifacts/handoff.md (three done, the first restating the resolved answer.txt): %+v", syncs)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "answer.txt")); !strings.Contains(string(b), "42") || !strings.Contains(string(b), "forty-two") {
		t.Fatalf("the final answer.txt must keep both sides: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "NOTES.md")); !strings.Contains(string(b), "The answer is 42") || !strings.Contains(string(b), "Main notes.") {
		t.Fatalf("the final NOTES.md must keep both sides: %q", b)
	}
	if body, _ := os.ReadFile(filepath.Join(capDir, "artifacts", "delivery.md")); !strings.Contains(string(body), "conflict resolved in sync: NOTES.md") {
		t.Fatalf("the PR body (delivery note) lists the file sync resolved: %q", body)
	}
	fmt.Println("PASS V1 sync checked first, resolved real conflicts in place and continued; the PR body lists them")

	// the lying verification and publish's check: the result file said
	// fail, publish handed off fail to build naming it; verify.py ran three
	// times (red
	// over the wrong resolution, then green twice), the last result this
	// round's
	if exits := w374RunExits(t, capDir, w374VerifyLine); len(exits) != 3 || exits[0] != 1 || exits[1] != 0 || exits[2] != 0 {
		t.Fatalf("verify.py exits (red over the wrong resolution, then green): %v", exits)
	}
	res := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "verify", "result.json"))
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if res["verdict"] != "pass" || res["round"] == "" || res["heads"].(map[string]any)["demo"] != head {
		t.Fatalf("the verification's result file: verdict %v round %v heads %v (HEAD %s)", res["verdict"], res["round"], res["heads"], head)
	}
	// the build's unit ran as written in the verification (verify.py
	// --unit-runs, workflow contract v2 PR 12)
	if c := w374Check(res, "demo:unit-run:u1"); c == nil || c["status"] != "pass" || fmt.Sprint(c["argv"]) != "[sh tests/answer.sh]" {
		t.Fatalf("the verification must run u1's recorded run green: %v", c)
	}
	var rewinds []string
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "rewind" {
			rewinds = append(rewinds, string(e.Body))
		}
	}
	if n, _ := x2.RewindStats["count"].(float64); n != 3 || len(rewinds) != 3 {
		t.Fatalf("three charged rewinds (review FAIL, publish's verify-first FAIL, merge's refused FAIL), got rewind_stats %v, rewinds %v", x2.RewindStats, rewinds)
	}
	if !strings.Contains(rewinds[1], `"gate":"publish"`) || !strings.Contains(rewinds[1], `"target":"build"`) {
		t.Fatalf("the second rewind must be publish's verify-first FAIL back to build: %s", rewinds[1])
	}
	if !strings.Contains(rewinds[2], `"gate":"merge"`) || !strings.Contains(rewinds[2], `"target":"sync"`) {
		t.Fatalf("the third rewind must be merge's FAIL back to sync: %s", rewinds[2])
	}
	var pubSpawn struct {
		SpawnN float64 `json:"spawn_n"`
	}
	_ = json.Unmarshal([]byte(rewinds[1]), &pubSpawn)
	if vs := stepcall.StepHandoffs(ledger, "verification"); len(vs) != 3 || vs[0].Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the lying verification handed off done over its red result file: %+v", vs)
	}
	pubFail := stepcall.HandoffOf(ledger, "publish", int64(pubSpawn.SpawnN))
	if pubFail == nil || pubFail.Handoff.Status != stepcall.StatusFail || pubFail.Handoff.PerOwner != "" || !strings.Contains(pubFail.Handoff.Reason, "artifacts/verify/result.json says fail") {
		t.Fatalf("publish's fail (dispatch %v, charged: no owner message) must name the red result file: %+v", pubSpawn.SpawnN, pubFail)
	}
	fmt.Println("PASS P1a publish failed to build on the red result file the verification lied about")

	// the forge refused the merge once (exit 1: main moved and conflicts),
	// then merge.py landed the re-synced, re-verified head once
	if exits := w374RunExits(t, capDir, w374MergeLine); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("merge.py exits (refused by the forge, then landed): %v", exits)
	}
	if w.prState("demo", "1") != "MERGED" || w.calls("gh pr merge") != 2 || w.calls("gh pr merge https://github.com/fake/demo/pull/1 --merge --match-head-commit "+head) != 1 {
		t.Fatalf("the merge step merges the published PR at its verified head (one refused attempt before): state %q, merges %d (head %s)", w.prState("demo", "1"), w.calls("gh pr merge"), head)
	}
	// the conflicting main (the seed's last push) is merged into the landed head
	gitRun(t, repo, "merge-base", "--is-ancestor", strings.TrimSpace(gitRun(t, x.seed, "rev-parse", "HEAD")), "HEAD")
	// merge failed once (charged), then registered the landing record
	// merge.py wrote — once, in the round it handed off done, before its
	// done (merge declares wrap_attrs: [integration])
	mh := stepcall.StepHandoffs(ledger, "merge")
	var wraps []stepcall.Line
	for _, l := range ledger {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHubPrefix+"wrap-attrs" && l.Outcome == stepcall.OutcomeOK {
			wraps = append(wraps, l)
		}
	}
	if len(mh) != 2 || mh[0].Handoff.Status != stepcall.StatusFail || mh[1].Handoff.Status != stepcall.StatusDone ||
		len(wraps) != 1 || wraps[0].Step != "merge" || wraps[0].Round != mh[1].Round || wraps[0].Seq > mh[1].Seq {
		t.Fatalf("merge's hand-offs (fail, then done after its one wrap-attrs registration in that round): hand-offs %+v, wrap-attrs %+v", mh, wraps)
	}
	fmt.Println("PASS P1b merge failed to sync on the forge's refusal; sync accepted its own after, merged main again, and the PR landed")

	// the landing record: the wrap carries what merge.py wrote and merge
	// registered — and no waiver (the run has none)
	mc, _ := os.ReadFile(filepath.Join(w.state, "demo", "prs", "1", "merge"))
	got, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	repos, _ := in["repos"].([]any)
	r0, _ := func() (map[string]any, bool) {
		if len(repos) != 1 {
			return nil, false
		}
		m, ok := repos[0].(map[string]any)
		return m, ok
	}()
	commits, _ := in["merge_commits"].(map[string]any)
	demo, _ := commits["demo"].([]any)
	if in["state"] != "landed" || r0 == nil || !strings.HasSuffix(fmt.Sprint(r0["pr"]), "/pull/1") || r0["head"] != head || r0["light_verified_head"] != head || r0["accepted"] != nil ||
		len(demo) != 1 || demo[0] != strings.TrimSpace(string(mc)) || r0["merge_commit"] != demo[0] || in["waived"] != nil || got.Attrs["waivers"] != nil {
		t.Fatalf("the wrap must carry the landing (state landed, the PR, head %s = the light-verified head, the merge commit %s, no waiver): attrs %v", head, mc, got.Attrs)
	}
	fmt.Println("PASS D6 published, merged by the merge step's merge.py, landed")
}

// w374RedWorld scripts the lane up to a red verification the agent cannot
// place: build completes in one dispatch (no stop), review passes while
// the world adds a failing check to main's conf, sync merges it in, and
// verify.py is red on tests/extra.sh — a red the change never touched.
func w374RedWorld(t *testing.T) (*w374DevelopWorld, string, string, []map[string]any) {
	t.Helper()
	x, wi, branch := newW374World(t, w374TicketNoStop)
	extra := x.mainMoves(map[string]string{
		".polyforge/verify-scope.conf": "stack=none\nfull_command=sh tests/answer.sh && sh tests/extra.sh\n",
		"tests/extra.sh":               "exit 1\n",
	}, "main: an extra check that is red")
	prefix := []map[string]any{
		buildAction(w374Build("u1 red then green", x.planFiles())),
		x.reviewAction("facts", extra),
		x.syncAction(),
	}
	return x, wi, branch, prefix
}

// TestV2DevelopVerificationRedAskedThenWaived: a red the verification
// agent cannot place (main brought a failing check) is asked, never passed
// and never rewound: the agent runs `pf step ask` and ends its turn, the
// step parks awaiting the owner — on the heavy-admission step its process
// ends at the ask — and the owner's 放行 resumes the session; the agent
// hands off `waive --per-owner <that reply's at>`. No rewind is charged,
// publish proceeds on the waive stamped on the verification's section of
// the result's round (the owner's words quoted) beside the red result
// file, and the wrap records the waiver in attrs.waivers (the round, the
// owner's words verbatim, the agent's reason) — the landing record names
// none.
func TestV2DevelopVerificationRedAskedThenWaived(t *testing.T) {
	x, wi, branch, prefix := w374RedWorld(t)
	question := "verification is red on tests/extra.sh (exit 1), a check main brought in that this change never touched: run again, waive it, or fix it?"
	x.inst.setPlanFull(t, wi, append(prefix,
		// the verification agent cannot place the red: pf step ask, then
		// the turn ends with no hand-off
		map[string]any{"do": "envelope", "id": "verification", "end_turn": true, "run": []string{w374VerifyLine, "pf step ask " + shellQuote(question)}},
		// the owner's reply resumes the session: the hand-off `waive`
		// citing the reply (its at read from asks.jsonl)
		map[string]any{"do": "envelope", "id": "verification", "outcome": "gate_failed", "no_started_at": true,
			"output":      map[string]any{"verdict": "fail", "fail_reason": "tests/extra.sh exit 1 (main's check, not this change's); the owner waived it", "summary": "red, waived by the owner"},
			"run":         []string{x.intent("waive", "verification")},
			"intent_file": "artifacts/verification-intent.json"},
		// publish reads the waive of that round beside the red result file
		x.publishAction(branch),
		x.mergeAction(),
	), nil)
	d := startDeliverDaemon(t, x.inst, x.w, wi)

	waitUntil(t, 300*time.Second, "the awaiting_owner event of the verification's pf step ask", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) > 0
	})
	ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0])
	if ev["step"] != "verification" || ev["question"] != question {
		t.Fatalf("awaiting_owner body: %v", ev)
	}
	capDir := d.capsuleDir(wi)
	if exits := w374RunExits(t, capDir, w374VerifyLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("verify.py exits (red on main's extra check): %v", exits)
	}
	res := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "verify", "result.json"))
	if res["verdict"] != "fail" || !strings.Contains(fmt.Sprint(res["summary"]), "check(s) failed") {
		t.Fatalf("the result file must say fail: %v", res)
	}
	if exits := w374RunExits(t, capDir, "pf step ask"); len(exits) != 1 || exits[0] != 0 {
		t.Fatalf("pf step ask exits: %v", exits)
	}
	if x2 := readExecution(t, capDir); x2.CurrentStep != "verification" || x2.WorkflowStatus == "completed" || x2.WorkflowStatus == "failed" {
		t.Fatalf("the step must be parked on its ask, not routed: current %s status %s", x2.CurrentStep, x2.WorkflowStatus)
	}
	fmt.Println("PASS P1c a red the agent cannot place is asked, not passed and not rewound")

	d.pf(t, "answer", wi, "--text", "放行，这个红是 main 带进来的检查，与本票无关", "--by", "usr_root")
	waitWorkflowStatus(t, d, wi, "completed", 300*time.Second)
	x.inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x2 := readExecution(t, capDir)

	// the verification's one hand-off: waive, in the result's round,
	// citing the owner's reply
	var replyAt string
	for _, l := range v26dAsks(t, capDir) {
		if l.Kind == "reply" && l.Step == "verification" {
			replyAt = l.At
		}
	}
	vs := stepcall.StepHandoffs(w374Ledger(t, capDir), "verification")
	if replyAt == "" || len(vs) != 1 || vs[0].Handoff.Status != stepcall.StatusWaive || vs[0].Handoff.PerOwner != replyAt || fmt.Sprint(vs[0].Round) != fmt.Sprint(res["round"]) {
		t.Fatalf("the verification must hand off waive in the result's round %v, citing the reply %q: %+v", res["round"], replyAt, vs)
	}
	if n, _ := x2.RewindStats["count"].(float64); n != 0 {
		t.Fatalf("a waived FAIL charges no rewind: %v", x2.RewindStats)
	}
	kinds := d.eventKinds(t, wi)
	if !contains(kinds, "owner_waived") || !contains(kinds, "owner_message") {
		t.Fatalf("the reply and the waiver must be on the event record: %v", kinds)
	}
	if res := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "verify", "result.json")); res["verdict"] != "fail" {
		t.Fatalf("the result file stays the command's (fail): %v", res["verdict"])
	}
	check := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "publish-check.json"))
	cw, _ := check["waived"].(map[string]any)
	if check["proceed"] != true || cw == nil || cw["round"] != fmt.Sprint(res["round"]) || cw["per_owner"] != replyAt || !strings.Contains(fmt.Sprint(cw["words"]), "放行") {
		t.Fatalf("publish must have read the waive of the result's round (the owner's words its header quotes) beside the red result: %v", check)
	}
	head := strings.TrimSpace(gitRun(t, filepath.Join(capDir, "repos", "demo"), "rev-parse", "HEAD"))
	got, err := x.inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	ws, _ := got.Attrs["waivers"].([]any)
	w0 := func() map[string]any {
		if len(ws) != 1 {
			return nil
		}
		m, _ := ws[0].(map[string]any)
		return m
	}()
	repos, _ := in["repos"].([]any)
	r0 := func() map[string]any {
		if len(repos) != 1 {
			return nil
		}
		m, _ := repos[0].(map[string]any)
		return m
	}()
	if w0 == nil || w0["gate"] != "verification" || fmt.Sprint(w0["round"]) != fmt.Sprint(res["round"]) || w0["owner_message_at"] != replyAt ||
		!strings.Contains(fmt.Sprint(w0["owner_words"]), "放行") || !strings.Contains(fmt.Sprint(w0["agent_reason"]), "waive") {
		t.Fatalf("the wrap must record the waiver of the result's round in attrs.waivers, with the owner's words and the agent's reading (reply %s): %v", replyAt, got.Attrs)
	}
	if in["state"] != "landed" || in["waived"] != nil || r0 == nil || r0["head"] != head || r0["light_verified_head"] != head {
		t.Fatalf("the landing must carry the heads the red run tested and no waiver of its own: %v", got.Attrs)
	}
	if x.w.prState("demo", "1") != "MERGED" {
		t.Fatalf("the PR must be merged: %q", x.w.prState("demo", "1"))
	}
	fmt.Println("PASS P1d waive through pf step ask and the owner's reply: no rewind, the waive of that round, publish proceeds, the wrap's attrs.waivers names it")
}

// TestV2DevelopSyncRewindKeepsBuildsWork: a real conflict the sync agent
// decides is not its to resolve goes back to build without losing what
// build does next — the agent asks the owner, and on the reply runs
// the sync script's `--abort` line (the merge in progress aborted, the sync's record
// dropped) before it hands off `rewind --to build --per-owner <the
// reply's at>`; build adapts the change to the default branch and commits
// on a clean tree; the next sync starts over from build's commit (nothing
// discarded, its `before` that commit) and the lane lands it.
func TestV2DevelopSyncRewindKeepsBuildsWork(t *testing.T) {
	x, wi, branch := newW374World(t, w374TicketNoStop)
	// main rewrites the answer the change builds on: an add/add conflict
	premise := x.mainMoves(map[string]string{"answer.txt": "42\nforty-two\n"}, "main: the answer, spelled out")
	question := "answer.txt conflicts with main's rewrite of the answer this change builds on; resolving the hunks would change what AC1 means — send it back to build?"
	adapt := `cd repos/demo && printf '42\nforty-two\n' > answer.txt && git add -A && ` + w374Commit + ` -m 'u1: adapt to main''s answer (sync s3)'`
	x.inst.setPlanFull(t, wi, []map[string]any{
		buildAction(w374Build("u1 red then green", x.planFiles())),
		x.reviewAction("facts", premise),
		// sync: the real conflict (exit 1), not the agent's to resolve: it
		// asks; on the owner's reply it aborts the sync, then hands off
		// rewind to build citing the reply
		{"do": "envelope", "id": "sync", "end_turn": true, "run": []string{x.syncCheck(), w374SyncLine, "pf step ask " + shellQuote(question)},
			"on_message": map[string]any{"do": "envelope", "id": "sync", "outcome": "intent", "no_started_at": true,
				"run":         []string{w374AbortLine, x.intent("rewind", "sync", "build")},
				"intent_file": "artifacts/sync-intent.json"}},
		// build, resumed by the owner's send-back, adapts and commits
		buildAction([]string{adapt, w374Units, w374Output("u1 adapted to main's answer (the owner's send-back from sync)")}),
		x.reviewAction("facts"),
		x.syncAction(),
		verifyAction(map[string]any{"verdict": "pass", "summary": "light verification passed"}),
		x.publishAction(branch),
		x.mergeAction(),
	}, nil)
	d := startDeliverDaemon(t, x.inst, x.w, wi)

	waitUntil(t, 300*time.Second, "the sync agent's ask", func() bool {
		for _, e := range v26cAwaiting(t, d.dataDir, wi) {
			if v26cBody(e)["step"] == "sync" {
				return true
			}
		}
		return false
	})
	d.pf(t, "answer", wi, "--text", "yes — send it back, build should adapt to main's answer", "--by", "usr_root")
	waitWorkflowStatus(t, d, wi, "completed", 300*time.Second)
	x.inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	repo := filepath.Join(capDir, "repos", "demo")

	if exits := w374RunExits(t, capDir, w374AbortLine); len(exits) != 1 || exits[0] != 0 {
		t.Fatalf("the sync agent aborted the sync before handing back: the abort line exits %v", exits)
	}
	if exits := w374RunExits(t, capDir, w374SyncLine); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("sync.py exits (the real conflict, then a clean merge over build's commit): %v", exits)
	}
	fix := strings.TrimSpace(gitRun(t, repo, "log", "--format=%H", "--grep=adapt to main", "-1"))
	if fix == "" {
		t.Fatal("build's adapting commit is not on the branch")
	}
	gitRun(t, repo, "merge-base", "--is-ancestor", fix, "HEAD")
	if refs := strings.TrimSpace(gitRun(t, repo, "for-each-ref", "refs/polyforge/discarded/")); refs != "" {
		t.Fatalf("nothing build committed may be discarded: %s", refs)
	}
	sres := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "sync", "result.json"))
	srepos, _ := sres["details"].(map[string]any)["repos"].([]any)
	s0, _ := srepos[0].(map[string]any)
	if s0 == nil || s0["before"] != fix || s0["discarded"] != nil || sres["verdict"] != "pass" {
		t.Fatalf("the next sync started over from build's commit, nothing discarded: %v", sres)
	}
	x2 := readExecution(t, capDir)
	var replyAt string
	for _, l := range v26dAsks(t, capDir) {
		if l.Kind == "reply" && l.Step == "sync" {
			replyAt = l.At
		}
	}
	if hs := stepcall.StepHandoffs(w374Ledger(t, capDir), "sync"); replyAt == "" || len(hs) != 2 || hs[0].Handoff.Status != stepcall.StatusRewind || hs[0].Handoff.To != "build" ||
		hs[0].Handoff.PerOwner != replyAt || hs[1].Handoff.Status != stepcall.StatusDone {
		t.Fatalf("sync's hand-offs: rewind to build citing the owner's reply %q, then done: %+v", replyAt, hs)
	}
	kinds := d.eventKinds(t, wi)
	if !contains(kinds, "owner_rewind") {
		t.Fatalf("the sync agent's rewind is the owner's send-back: %v", kinds)
	}
	rws, _ := x2.RewindStats["rewinds"].([]any)
	if len(rws) != 1 || rws[0].(map[string]any)["class"] != "owner" || rws[0].(map[string]any)["gate"] != "sync" {
		t.Fatalf("the only rewind is the owner's send-back from sync (class owner: charged to no budget): %v", x2.RewindStats)
	}
	got, err := x.inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	if in, _ := got.Attrs["integration"].(map[string]any); in["state"] != "landed" {
		t.Fatalf("the change lands: %v", got.Attrs)
	}
	fmt.Println("PASS V2 sync aborted before handing back; build's commit kept and landed")
}
