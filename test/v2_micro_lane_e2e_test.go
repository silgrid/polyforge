// carries: internal/polyforge/driver/*, internal/polyforge/daemon/*
// carries: workflows/micro/*, workflows/_shared/skills/*, workflows/_shared/steps/publish.md, workflows/_shared/scripts/*

package test

// v2_micro_lane_e2e_test.go (workflow contract 4): the micro lane end to
// end through the real daemon + Go driver and the fake harness
// (test/fakepi as pi, the fake `gh`, a fake container engine no evidence
// line may reach): fix → review → sync → evidence → publish → merge, every
// step an agent step scripted to run the real pre-written command its
// skill names (the scripts `facts.py micro-bounds`, `sync.py`, `run.py
// --isolation host --plan-from` and `merge.py` as `"$PF_PYTHON"
// "$PF_SHARED_DIR/scripts/…"` from the lane snapshot, /
// #474) against a
// fixture repository that opted into micro, to check first what the step
// before it handed off — from the result files the commands write and the
// daemon's records a step reads (the call ledger
// runtime-state/step-calls.jsonl, artifacts/handoff.md), never
// execution.json — and to end each dispatch with one `pf step handoff`.
//
// TestV2MicroLaneEndToEnd:
//
//   - fix edits docs/guide.md, writes its evidence plan
//     (artifacts/fix/evidence.json) and runs micro-bounds (in bounds);
//     review re-runs micro-bounds itself and hands off done; sync checks
//     HEAD is the head review's hand-off stamped and merges main in;
//   - evidence: `run.py --plan-from` exits 1 on the host (the change left a broken
//     link) and the scripted agent LIES done; publish verifies the run
//     result first and hands off fail to fix (charged) — the red never
//     reaches a PR;
//   - fix repairs the link, the lane runs again, publish opens the PR; the
//     world moves the PR head (the branch pushed back to its first parent);
//     `merge.py` refuses it (exit 1) and merge hands off fail to sync, whose
//     verify-first accepts its own earlier after; the next round re-runs
//     the plan, publishes the head again and lands the PR at it; merge
//     registers the landing record merge.py wrote (`pf step hub wrap-attrs
//     integration`) and the wrap carries it: its verified head is the run's.
//
// TestV2MicroOutOfBoundsReviewFailsThenOwnerCancels:
//
//   - fix commits a path outside the declaration and hands off done without
//     judging its bounds; review's own micro-bounds run is out of bounds:
//     review hands off fail to fix;
//   - fix runs micro-bounds (exit 1), asks the owner (`pf step ask`) to
//     re-file on develop and ends its turn; the owner answers in free text;
//     fix hands off cancel citing the reply (--per-owner): the work item
//     ends canceled, nothing pushed.
//
// TestV2MicroSyncRewindToFixAborts: a real conflict the sync agent decides
// is not its to resolve goes back to fix — it asks, and on the owner's reply
// runs the sync skill's abort line before it hands off rewind to fix citing the reply;
// fix adapts on a clean tree, the next sync starts over from fix's commit
// (nothing discarded) and the lane lands it.
//
// TestV2MicroEvidenceWaivedByIntent: main brings a red check the change
// never touched; evidence asks the owner, the owner says 放行, evidence
// hands off waive citing the reply: no rewind, publish proceeds on the
// waive hand-off of the run result's round (the owner's words as
// handoff.md's header quotes them), and the wrap's attrs.waivers carries
// the owner's words.
//
// TestV2MicroEvidenceFailsOnASyncArgvMismatch: the sync
// agent runs its line with one argument more than the skill writes; the
// evidence agent checks the sync result file's argv against the sync
// skill's lines (read from the lane snapshot) before it runs anything and
// hands off fail naming the mismatch — one charged rewind to fix — and the
// lane, run again as the skills write it, lands.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestV2Micro' -count=1 -v

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
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The skills' command lines, verbatim (a line here that drifts from the
// skill is the driver's prompt test's to catch).
const (
	v2mBoundsLine       = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/facts/micro-bounds.json`
	v2mReviewBoundsLine = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/review/micro-bounds.json`
	v2mRunLine          = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/run.py" --isolation host --plan-from artifacts/fix/evidence.json --json --out artifacts/run/result.json`
	v2mMergeLine        = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish artifacts/publish.json --verified artifacts/run/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json`
	v2mWrapLine         = "pf step hub wrap-attrs integration @artifacts/merge/landing.json"
	v2mAbortLine        = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --abort --json --out artifacts/sync/result.json`
)

// v2mConf is the fixture repository's micro declaration: the docs are
// text-only, and one declared micro check re-runs on delivery.
const v2mConf = "micro_path=docs/**\nmicro_check=sh checks/links.sh\n"

// v2mLinks is the declared check: red while the guide carries a broken
// link marker.
const v2mLinks = "#!/bin/sh\nif grep -q 'broken-link' docs/guide.md; then echo 'docs/guide.md: broken-link'; exit 1; fi\necho links ok\n"

// v2mPlan is fix's evidence plan: the one declared check, in its repo.
const v2mPlan = `[{"repo":"demo","line":"sh checks/links.sh"}]`

// v2mTicket is the micro ticket (its acceptance section lets auto-pull
// take it).
const v2mTicket = "Reword the guide's first line.\n\n## Acceptance\n\n- AC1 docs/guide.md says the new words\n"

// v2mReviewOut is the python the scripted review routes its own
// micro-bounds run with: in bounds → pass; out of bounds → the FAIL with
// one blocking finding naming the refusal. It records the verdict and the
// numbered findings in artifacts/review.md (what publish reads) and the
// route in artifacts/review-out.json (fakepi: a fail verdict is the gate's
// `fail` hand-off, its fail_reason the --reason).
const v2mReviewOut = `import json
r = json.load(open("artifacts/review/micro-bounds.json"))
if r["verdict"] == "pass":
    out = {"verdict": "pass", "findings": []}
else:
    reason = r["details"].get("reason", r["summary"])
    out = {"verdict": "fail", "fail_reason": "out of bounds: " + reason,
           "findings": [{"severity": "blocking", "detail": "micro-bounds: " + reason}]}
md = "# review\n\nverdict: " + out["verdict"] + "\n\n"
for i, f in enumerate(out["findings"]):
    md += "%d. [%s] %s\n" % (i + 1, f["severity"], f["detail"])
open("artifacts/review.md", "w").write(md)
json.dump(out, open("artifacts/review-out.json", "w"))
`

// v2mEvidenceCheck is the python an honest evidence agent checks sync's
// hand-off with first (the evidence skill's 开工前 1,): the
// sync result file's argv must be the arguments of the sync skill's start
// or continue line, read from the skill itself in the lane snapshot
// ($PF_SHARED_DIR/skills/sync.md), and every repo's conflicts empty. It
// writes artifacts/evidence-check.json — and artifacts/evidence-check.ok
// when the check holds — and exits 1 on a mismatch.
const v2mEvidenceCheck = `import json, os, re, shlex, sys
skill = open(os.path.join(os.environ["PF_SHARED_DIR"], "skills", "sync.md"), encoding="utf-8").read()
prefix = '"$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py"'
lines = [shlex.split(m)[2:] for m in re.findall(r"\x60(" + re.escape(prefix) + r"[^\x60]*)\x60", skill)]
allowed = [a for a in lines if "--abort" not in a]
r = json.load(open("artifacts/sync/result.json"))
why = ""
if r.get("argv") not in allowed:
    why = "sync hand-off mismatch: argv %s is none of the sync skill's lines %s" % (r.get("argv"), allowed)
elif any((x.get("conflicts") or []) for x in (r.get("details") or {}).get("repos") or []):
    why = "sync hand-off mismatch: a conflict is still pending"
json.dump({"ok": not why, "why": why, "allowed": allowed}, open("artifacts/evidence-check.json", "w"))
if why:
    if os.path.exists("artifacts/evidence-check.ok"):
        os.remove("artifacts/evidence-check.ok")
else:
    open("artifacts/evidence-check.ok", "w").close()
sys.exit(1 if why else 0)
`

// v2mEvidenceOut is the python an honest evidence agent routes with: a
// failed check of sync's hand-off is the gate's FAIL, else the run result.
const v2mEvidenceOut = `import json
c = json.load(open("artifacts/evidence-check.json"))
r = json.load(open("artifacts/run/result.json")) if c["ok"] else {}
if not c["ok"]:
    out = {"verdict": "fail", "fail_reason": c["why"], "summary": "sync hand-off mismatch"}
elif r["verdict"] == "pass":
    out = {"verdict": "pass", "summary": r["summary"]}
else:
    out = {"verdict": "fail", "fail_reason": r["summary"], "summary": "red"}
json.dump(out, open("artifacts/evidence-out.json", "w"))
`

// v2mSyncCheck is the python the scripted sync agent verifies first with
// (the sync skill): per task repo, HEAD is the head review's latest
// hand-off stamped (the call ledger's accepted review hand-off: its
// `heads`, the end commits handoff.md's header shows) or this step's own
// earlier after (the result file a merge FAIL sent the lane back over).
// Each run appends its reading per repo to artifacts/sync-checks.log; a
// mismatch exits 1.
const v2mSyncCheck = `import json, os, subprocess, sys
judged = {}
for line in open("runtime-state/step-calls.jsonl"):
    line = line.strip()
    if not line:
        continue
    l = json.loads(line)
    if l.get("kind") == "result" and l.get("verb") == "handoff" and l.get("outcome") == "ok" and l.get("step") == "review":
        judged = (l.get("handoff") or {}).get("heads") or {}
prior = {}
if os.path.exists("artifacts/sync/result.json"):
    for r in (json.load(open("artifacts/sync/result.json")).get("details") or {}).get("repos") or []:
        prior[r["name"]] = r.get("after") or ""
seen = []
for n in sorted(os.listdir("repos")):
    head = subprocess.run(["git", "-C", "repos/" + n, "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()
    if head and judged.get(n) == head:
        seen.append("reviewed")
    elif head and prior.get(n) == head:
        seen.append("own-after")
    else:
        seen.append("mismatch")
open("artifacts/sync-checks.log", "a").write(",".join(seen) + "\n")
sys.exit(1 if "mismatch" in seen else 0)
`

// v2mPublishCheck is the python the scripted publish agent verifies first
// with (the publish skill): the run result names HEAD (waived or not) and
// says pass — or evidence's latest accepted hand-off on the call ledger
// is a waive of the run result's round whose cited owner message, as
// handoff.md's header quotes it, accepts the red; and review's latest
// hand-off is done with artifacts/review.md giving the verdict pass.
const v2mPublishCheck = `import json, os, subprocess
v = json.load(open("artifacts/run/result.json"))
last = {}
for line in open("runtime-state/step-calls.jsonl"):
    line = line.strip()
    if not line:
        continue
    l = json.loads(line)
    if l.get("kind") == "result" and l.get("verb") == "handoff" and l.get("outcome") == "ok" and l.get("handoff"):
        last[l["step"]] = l
ev = last.get("evidence") or {}
evh = ev.get("handoff") or {}
waived = None
if evh.get("status") == "waive" and str(ev.get("round")) == str(v.get("round")):
    doc = open("artifacts/handoff.md").read()
    mark = "**The owner message \x60--per-owner\x60 cites**\n\n- " + evh.get("per_owner", "") + " · "
    i = doc.rfind(mark)
    words = doc[i:].split("~~~\n")[1].strip() if i >= 0 else ""
    if any(w in words for w in ("放行", "ship it", "merge anyway")):
        waived = {"round": ev.get("round"), "per_owner": evh.get("per_owner"), "words": words}
review = ((last.get("review") or {}).get("handoff") or {}).get("status")
review_md = open("artifacts/review.md").read() if os.path.exists("artifacts/review.md") else ""
head = subprocess.check_output(["git", "-C", "repos/demo", "rev-parse", "HEAD"]).decode().strip()
if v["heads"].get("demo") != head:
    out = {"verdict": "fail", "fail_reason": "verify-first: artifacts/run/result.json names another head than HEAD"}
elif v["verdict"] != "pass" and not waived:
    out = {"verdict": "fail", "fail_reason": "verify-first: artifacts/run/result.json says " + v["verdict"] + " and evidence's latest hand-off is no owner's waiver of its round (it handed off " + str(evh.get("status")) + ")"}
elif review != "done" or "verdict: pass" not in review_md:
    out = {"verdict": "fail", "fail_reason": "verify-first: review's latest hand-off is " + str(review) + " and artifacts/review.md does not give the verdict pass"}
else:
    out = {"verdict": "pass", "proceed": True, "waived": waived}
json.dump(out, open("artifacts/publish-check.json", "w"))
`

// v2mMergeOut is the python the scripted merge agent routes with, on the
// result merge.py wrote (its verdict is the exit code's): a pass is done
// (the landing record is merge.py's, registered by the skill's wrap-attrs
// line), a refusal the FAIL naming each repo's why.
const v2mMergeOut = `import json
r = json.load(open("artifacts/merge/result.json"))
if r["verdict"] == "pass":
    out = {"verdict": "pass", "summary": r["summary"]}
else:
    out = {"verdict": "fail", "fail_reason": r["summary"] + "; " + "; ".join(x.get("why", "") for x in r["details"]["repos"])}
json.dump(out, open("artifacts/merge-out.json", "w"))
`

// v2mIntent is the python a scripted agent records the owner's ruling
// with, for fakepi to hand off (its intent_file: kind cancel → --status
// cancel, rewind → --status rewind --to, waive → --status waive):
// reply_at — the hand-off's --per-owner — is the `at` of the owner's last
// reply or message to the step in runtime-state/asks.jsonl (what a real
// agent reads; the scenario cannot know the stamp in advance). argv: step,
// kind, reason[, to].
const v2mIntent = `import json, sys
step, kind, reason = sys.argv[1], sys.argv[2], sys.argv[3]
at = ""
for line in open("runtime-state/asks.jsonl"):
    line = line.strip()
    if not line:
        continue
    l = json.loads(line)
    if l.get("kind") in ("reply", "message") and l.get("step") == step:
        at = l["at"]
intent = {"kind": kind, "reply_at": at, "reason": reason}
if len(sys.argv) > 4:
    intent["to"] = sys.argv[4]
json.dump(intent, open("artifacts/" + step + "-intent.json", "w"))
`

// v2MicroWorld is one scenario's world: the deliver world (a bare origin,
// the fake gh), the fake container engine, the `pf-*` links beside them
// and the scripted agents' python helpers (outside the capsule).
type v2MicroWorld struct {
	w       *deliverWorld
	inst    *v1Install
	seed    string
	scripts string
	docker  string // the fake engine's call log
}

// newV2MicroWorld seeds demo's origin with the micro declaration, the
// declared check and a verification image (the fake engine runs each
// line here), installs the pf-* links the skills run and files the
// ticket on micro.
func newV2MicroWorld(t *testing.T) (*v2MicroWorld, string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed (the scripted agents route with it)")
	}
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	seed := filepath.Join(w.root, "seed-demo")
	for p, c := range map[string]string{
		".polyforge/verify-scope.conf": v2mConf,
		".polyforge/Dockerfile.verify": "FROM scratch\n",
		"checks/links.sh":              v2mLinks,
		"docs/guide.md":                "# Guide\n\nThe first line.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(seed, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(seed, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "-c", "user.name=o", "-c", "user.email=o@invalid", "commit", "-q", "-m", "opt into micro")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	inst := newV1InstallRepos(t, []api.ProjectRepo{{Name: "demo", URL: origin}})
	inst.keepDeliver = true
	x := &v2MicroWorld{w: w, inst: inst, seed: seed, scripts: filepath.Join(w.root, "scripts"), docker: filepath.Join(w.root, "docker.log")}
	// no step command link: every line the micro skills run is a workflow
	// script (facts.py, sync.py, run.py, merge.py) from the lane snapshot
	if err := os.WriteFile(filepath.Join(w.bin, "docker"), []byte(w404Docker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(x.scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"sync-check.py": v2mSyncCheck, "review-out.py": v2mReviewOut, "evidence-out.py": v2mEvidenceOut, "evidence-check.py": v2mEvidenceCheck,
		"publish-check.py": v2mPublishCheck, "merge-out.py": v2mMergeOut, "intent.py": v2mIntent} {
		if err := os.WriteFile(filepath.Join(x.scripts, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content := v2mTicket
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: v1Project, Goal: "v2 micro: reword the guide", Content: &content, BundleName: strPtr("micro"),
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
	return x, item.ID, "polyforge/" + wiPathName(t, inst, item.ID)
}

// start runs the scenario's daemon: the deliver world's environment plus
// the fake engine's call log.
func (x *v2MicroWorld) start(t *testing.T, wi string) *v1Daemon {
	t.Helper()
	d := newV1Daemon(t, x.inst, x.w.daemonEnv(), optEnv("CALLLOG="+x.docker))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	return d
}

// mainMoves commits files on demo's main (through the seed clone) and
// pushes: the world acting while the lane runs.
func (x *v2MicroWorld) mainMoves(files map[string]string, msg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s && ", x.seed)
	for p, c := range files {
		fmt.Fprintf(&b, "mkdir -p %s && printf '%%s' %s > %s && ", filepath.Dir(p), shellQuote(c), p)
	}
	fmt.Fprintf(&b, "git add -A && git -c user.name=o -c user.email=o@invalid commit -q -m %s && git push -q origin HEAD:refs/heads/main", shellQuote(msg))
	return b.String()
}

func (x *v2MicroWorld) py(name string, args ...string) string {
	c := "python3 -I " + filepath.Join(x.scripts, name)
	for _, a := range args {
		c += " " + shellQuote(a)
	}
	return c
}

// fixEdit is the scripted fix's edit: the guide's first line, committed.
func fixEdit(line string) string {
	return `cd repos/demo && printf '# Guide\n\n%s\n' ` + shellQuote(line) + ` > docs/guide.md && git add -A && ` + w374Commit + ` -m 'docs: the guide'`
}

// fixPlan writes fix's evidence plan.
const fixPlan = `mkdir -p artifacts/fix && printf '%s' '` + v2mPlan + `' > artifacts/fix/evidence.json`

// fixAction is the scripted fix: the edit, the plan, its own micro-bounds
// run (the skill's line), then done.
func (x *v2MicroWorld) fixAction(line string, world ...string) map[string]any {
	act := map[string]any{"do": "envelope", "id": "fix", "outcome": "completed",
		"output": map[string]any{"summary": "the guide's first line: " + line + "; micro-bounds: in bounds"},
		"run":    []string{fixEdit(line), fixPlan, v2mBoundsLine}}
	if len(world) > 0 {
		act["world"] = world
	}
	return act
}

// reviewAction is the scripted review: its own micro-bounds run (the line
// its 开工前 section gives), routed by review-out.py (a fail verdict is the
// gate's fail).
func (x *v2MicroWorld) reviewAction(world ...string) map[string]any {
	act := map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output_file": "artifacts/review-out.json",
		"run": []string{v2mReviewBoundsLine, x.py("review-out.py")}}
	if len(world) > 0 {
		act["world"] = world
	}
	return act
}

// syncAction is the scripted sync: its verify-first (sync-check.py: HEAD
// is the head review's hand-off stamped or its own earlier after, each
// reading logged in artifacts/sync-checks.log), then the skill's line.
func (x *v2MicroWorld) syncAction() map[string]any {
	return x.syncActionLine(w374SyncLine)
}

// syncActionLine is syncAction running line instead of the skill's.
func (x *v2MicroWorld) syncActionLine(line string) map[string]any {
	return map[string]any{"do": "envelope", "id": "sync", "outcome": "completed",
		"output": map[string]any{"summary": "sync.py: the default branch merged in"},
		"run":    []string{x.py("sync-check.py"), line}}
}

// evidenceAction is the scripted evidence agent: the skill's line, then
// the given output (a lie when it says pass over a red result), or — when
// output is nil — the honest one: sync's hand-off checked first
// (evidence-check.py: the result's argv is the sync skill's line), the
// skill's line only when the check holds, then the route.
func (x *v2MicroWorld) evidenceAction(output map[string]any) map[string]any {
	act := map[string]any{"do": "envelope", "id": "evidence", "outcome": "completed", "run": []string{v2mRunLine}}
	if output != nil {
		act["output"] = output
	} else {
		act["run"] = []string{x.py("evidence-check.py"), "test -f artifacts/evidence-check.ok && " + v2mRunLine, x.py("evidence-out.py")}
		act["output_file"] = "artifacts/evidence-out.json"
	}
	return act
}

// publishAction is the scripted publish: its verify-first
// (publish-check.py), then — when the checks hold — the push and the PR as
// the skill says, publish.json and the delivery note; a check that does
// not hold is the gate's fail (its fail_reason the --reason).
func (x *v2MicroWorld) publishAction(branch string, world ...string) map[string]any {
	pub := `set -e; cap="$PWD"; cd repos/demo; git fetch -q origin; ` +
		`if grep -q '"proceed": true' "$cap/artifacts/publish-check.json"; then ` +
		`r=$(git ls-remote origin refs/heads/$b | cut -f1); ` +
		`if [ -z "$r" ] || git merge-base --is-ancestor "$r" HEAD; then git push -q origin HEAD:refs/heads/$b; else echo 'branch moved outside the lane' >&2; exit 1; fi; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`printf '# PR body\n\nmicro: the guide\n' > "$cap/artifacts/publish-pr-demo.md"; ` +
		`[ -n "$url" ] || url=$(gh pr create --base main --head "$b" --title "v2 micro publish" --body-file "$cap/artifacts/publish-pr-demo.md"); ` +
		`[ -n "$url" ] || url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`h=$(git rev-parse HEAD); ` +
		`printf '{"repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}]}' "$b" "$url" "$h" > "$cap/artifacts/publish.json"; ` +
		`cp "$cap/artifacts/publish-pr-demo.md" "$cap/artifacts/delivery.md"; ` +
		`printf '{"verdict":"pass","summary":"published %s at %s"}' "$url" "$h" > "$cap/artifacts/publish-output.json"; ` +
		`else cp "$cap/artifacts/publish-check.json" "$cap/artifacts/publish-output.json"; fi`
	act := map[string]any{"do": "envelope", "id": "publish", "outcome": "completed", "output_file": "artifacts/publish-output.json",
		"run": literalBranch(branch, x.py("publish-check.py"), pub)}
	if len(world) > 0 {
		act["world"] = literalBranch(branch, world...)
	}
	return act
}

// mergeAction is the scripted merge: the skill's micro line, on exit 0
// the skill's wrap-attrs line registering the landing record merge.py
// wrote (merge declares wrap_attrs: [integration], so its done is refused
// without it), then the route — done on a pass, the FAIL on a refusal.
func (x *v2MicroWorld) mergeAction() map[string]any {
	return map[string]any{"do": "envelope", "id": "merge", "outcome": "completed", "output_file": "artifacts/merge-out.json",
		"run": []string{v2mMergeLine + " && " + v2mWrapLine, x.py("merge-out.py")}}
}

// v2mRewinds answers the bodies of the work item's rewind events.
func v2mRewinds(t *testing.T, d *v1Daemon, wi string) []string {
	var out []string
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "rewind" {
			out = append(out, string(e.Body))
		}
	}
	return out
}

// v2mHandoffs answers the step's hand-off results on the capsule's call
// ledger, in order (accepted and refused).
func v2mHandoffs(t *testing.T, capDir, step string) []stepcall.Line {
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

// v2mStatuses answers the step's hand-off statuses in order, a refused
// call as "refused: <error>".
func v2mStatuses(t *testing.T, capDir, step string) []string {
	t.Helper()
	var out []string
	for _, l := range v2mHandoffs(t, capDir, step) {
		if l.Outcome != stepcall.OutcomeOK || l.Handoff == nil {
			out = append(out, "refused: "+l.Error)
			continue
		}
		out = append(out, l.Handoff.Status)
	}
	return out
}

// v2mOwnerAt answers the `at` of the owner's last reply or message to
// step on the capsule's ask record ("" when none).
func v2mOwnerAt(t *testing.T, capDir, step string) string {
	t.Helper()
	asks, err := capsule.ReadAsks(capDir)
	if err != nil {
		t.Fatalf("the ask record: %v", err)
	}
	at := ""
	for _, a := range asks {
		if (a.Kind == capsule.AskKindReply || a.Kind == capsule.AskKindMessage) && a.Step == step {
			at = a.At
		}
	}
	return at
}

func TestV2MicroLaneEndToEnd(t *testing.T) {
	x, wi, branch := newV2MicroWorld(t)
	inst, w := x.inst, x.w
	// main moves (no conflict) while review runs: sync merges it in
	other := x.mainMoves(map[string]string{"docs/other.md": "other\n"}, "main: another page")
	// and again while the second review runs: the second sync makes a merge
	// commit, so its after is not the reviewed head
	third := x.mainMoves(map[string]string{"docs/third.md": "third\n"}, "main: a third page")
	// the world moves the PR head outside the lane right after publish:
	// someone pushes the branch back to its first parent (the local HEAD,
	// the run's verified head, is untouched)
	moved := `cd repos/demo && git push -q -f origin HEAD~1:refs/heads/$b`
	inst.setPlanFull(t, wi, []map[string]any{
		// 1. fix: the edit leaves a broken link; in bounds
		x.fixAction("The new first line, see broken-link."),
		// 2. review: its own micro-bounds run, in bounds: pass
		x.reviewAction(other),
		// 3. sync: main's page merged in
		x.syncAction(),
		// 4. evidence: run.py is red (the broken link); the agent lies done
		x.evidenceAction(map[string]any{"verdict": "pass", "summary": "a lying evidence step: the result file says fail"}),
		// 5. publish verifies the run result first: fail to fix (rewind 1)
		x.publishAction(branch),
		// 6. fix repairs the link
		x.fixAction("The new first line."),
		// 7-9. review (main moves again), sync (a merge commit), evidence
		// (green, honest)
		x.reviewAction(third), x.syncAction(), x.evidenceAction(nil),
		// 10. publish pushes and opens the PR; the world moves its head
		x.publishAction(branch, moved),
		// 11. merge: merge.py refuses the moved head: fail to sync (rewind 2)
		x.mergeAction(),
		// 12-14. sync, evidence and publish over the moved head
		x.syncAction(), x.evidenceAction(nil), x.publishAction(branch),
		// 15. merge lands the PR at the re-run head and registers the
		// landing record
		x.mergeAction(),
	}, nil)
	d := x.start(t, wi)

	waitWorkflowStatus(t, d, wi, "completed", 600*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x2 := readExecution(t, capDir)

	// every step an agent step: the spawn journal names all six, in order
	var order []string
	for _, e := range spawnJournal(t, capDir) {
		order = append(order, fmt.Sprint(e["step_id"]))
	}
	if got := strings.Join(order, ","); got != "fix,review,sync,evidence,publish,fix,review,sync,evidence,publish,merge,sync,evidence,publish,merge" {
		t.Fatalf("the dispatch order: %s", got)
	}
	// fix's own and review's own micro-bounds runs: in bounds every time
	if exits := w374RunExits(t, capDir, v2mBoundsLine); len(exits) != 2 || exits[0] != 0 || exits[1] != 0 {
		t.Fatalf("fix's micro-bounds exits: %v", exits)
	}
	if exits := w374RunExits(t, capDir, v2mReviewBoundsLine); len(exits) != 2 || exits[0] != 0 || exits[1] != 0 {
		t.Fatalf("review's own micro-bounds exits: %v", exits)
	}
	mb := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "facts", "micro-bounds.json"))
	if det, _ := mb["details"].(map[string]any); mb["verdict"] != "pass" || det["in_bounds"] != true {
		t.Fatalf("fix's micro-bounds result: %v", mb)
	}
	fmt.Println("PASS M1 fix and review judged the bounds with facts.py micro-bounds")

	// evidence: run.py red, then green twice, each line on the host
	// (--isolation host: no container, the repo's Dockerfile.verify aside)
	if exits := w374RunExits(t, capDir, v2mRunLine); len(exits) != 3 || exits[0] != 1 || exits[1] != 0 || exits[2] != 0 {
		t.Fatalf("run.py exits (red over the broken link, then green): %v", exits)
	}
	if n := strings.Count(v1ReadString(x.docker), "docker exec"); n != 0 {
		t.Fatalf("the evidence lines run on the host, never in a container: %d execs\n%s", n, v1ReadString(x.docker))
	}
	// the honest evidence agent checked sync's hand-off by its argv
	if ec := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "evidence-check.json")); ec["ok"] != true {
		t.Fatalf("the evidence's check of sync's argv: %v", ec)
	}
	run := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "run", "result.json"))
	repo := filepath.Join(capDir, "repos", "demo")
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if run["verdict"] != "pass" || run["round"] == "" || run["heads"].(map[string]any)["demo"] != head || run["command"] != "run.py" {
		t.Fatalf("the run result: command %v verdict %v round %v heads %v (HEAD %s)", run["command"], run["verdict"], run["round"], run["heads"], head)
	}
	if r0 := run["details"].(map[string]any)["runs"].([]any)[0].(map[string]any); r0["image"] != nil || r0["container"] != nil {
		t.Fatalf("a host run names no image or container: %v", r0)
	}
	rewinds := v2mRewinds(t, d, wi)
	if n, _ := x2.RewindStats["count"].(float64); n != 2 || len(rewinds) != 2 {
		t.Fatalf("two charged rewinds (publish's verify-first FAIL, merge's moved head), got %v / %v", x2.RewindStats, rewinds)
	}
	if !strings.Contains(rewinds[0], `"gate":"publish"`) || !strings.Contains(rewinds[0], `"target":"fix"`) {
		t.Fatalf("the first rewind must be publish's verify-first FAIL back to fix: %s", rewinds[0])
	}
	// every hand-off was accepted (none refused): publish's first is its
	// fail naming the red run result, the dispatch the rewind names
	var pubSpawn struct {
		SpawnN float64 `json:"spawn_n"`
	}
	_ = json.Unmarshal([]byte(rewinds[0]), &pubSpawn)
	for step, want := range map[string]string{"fix": "done,done", "review": "done,done", "sync": "done,done,done", "evidence": "done,done,done", "publish": "fail,done,done", "merge": "fail,done"} {
		if got := strings.Join(v2mStatuses(t, capDir, step), ","); got != want {
			t.Fatalf("%s's hand-offs on the call ledger = %q, want %q", step, got, want)
		}
	}
	pub := v2mHandoffs(t, capDir, "publish")[0]
	if pub.Spawn != int64(pubSpawn.SpawnN) || !strings.Contains(pub.Handoff.Reason, "artifacts/run/result.json says fail") {
		t.Fatalf("publish's fail (dispatch %d, the rewind's %v) must name the red run result: %q", pub.Spawn, pubSpawn.SpawnN, pub.Handoff.Reason)
	}
	if w.calls("gh pr create") != 1 {
		t.Fatalf("the red never reached a PR before the repair: %d creates", w.calls("gh pr create"))
	}
	fmt.Println("PASS M2 publish FAILed to fix on the red run result the evidence step lied about")

	if !strings.Contains(rewinds[1], `"gate":"merge"`) || !strings.Contains(rewinds[1], `"target":"sync"`) {
		t.Fatalf("the second rewind must be merge's FAIL back to sync: %s", rewinds[1])
	}
	// sync's verify-first: the reviewed tree twice, then — after the merge
	// FAIL — its own earlier after
	checks, _ := os.ReadFile(filepath.Join(capDir, "artifacts", "sync-checks.log"))
	if got := strings.Join(strings.Fields(string(checks)), " "); got != "reviewed reviewed own-after" {
		t.Fatalf("sync's verify-first readings (review, review, its own after) = %q", got)
	}
	// merge.py exits 1 (no landing record, nothing registered), then 0 and
	// the skill's wrap-attrs line registers its landing record
	if exits := w374RunExits(t, capDir, v2mMergeLine); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("merge.py && its wrap-attrs line exits (the moved head refused, then landed and registered): %v", exits)
	}
	if w.prState("demo", "1") != "MERGED" || w.calls("gh pr merge") != 1 || w.calls("gh pr merge https://github.com/fake/demo/pull/1 --merge --match-head-commit "+head) != 1 {
		t.Fatalf("the merge step merges the PR once at the run's head: state %q merges %d (head %s)", w.prState("demo", "1"), w.calls("gh pr merge"), head)
	}
	fmt.Println("PASS M3 merge FAILed to sync on the moved PR head, then landed it once re-run")

	got, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	repos, _ := in["repos"].([]any)
	var r0 map[string]any
	if len(repos) == 1 {
		r0, _ = repos[0].(map[string]any)
	}
	if in["state"] != "landed" || r0 == nil || r0["head"] != head || r0["light_verified_head"] != head || got.Attrs["waivers"] != nil {
		t.Fatalf("the wrap must carry the landing record merge.py wrote at the run's head %s, no waiver: %v", head, got.Attrs)
	}
	landing := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "merge", "landing.json"))
	if lr, _ := landing["repos"].([]any); len(lr) != 1 || lr[0].(map[string]any)["head"] != head || landing["state"] != "landed" {
		t.Fatalf("the registered landing record is the one merge.py wrote: %v", landing)
	}
	fmt.Println("PASS M4 landed: the landing's verified head is the evidence run's")
}

func TestV2MicroOutOfBoundsReviewFailsThenOwnerCancels(t *testing.T) {
	x, wi, branch := newV2MicroWorld(t)
	outside := `cd repos/demo && mkdir -p src && printf 'package app\n' > src/app.go && git add -A && ` + w374Commit + ` -m 'src: a code file'`
	question := "The ticket needs src/app.go, outside demo's micro_path (docs/**): this is not a micro change. Cancel it and re-file the work on the develop lane?"
	x.inst.setPlanFull(t, wi, []map[string]any{
		// 1. fix commits a path outside the declaration and hands off done
		// without judging its bounds (it skips its skill's line)
		{"do": "envelope", "id": "fix", "outcome": "completed", "output": map[string]any{"summary": "the guide and a code file"},
			"run": []string{fixEdit("The new first line."), outside, fixPlan}, "files": map[string]string{"artifacts/facts/micro-bounds.json": `{"verdict":"pass"}`}},
		// 2. review's own micro-bounds run: out of bounds: fail to fix
		x.reviewAction(),
		// 3. fix judges its bounds (exit 1), asks the owner to re-file on
		// develop and ends its turn; on the owner's reply it hands off
		// cancel citing it
		{"do": "envelope", "id": "fix", "end_turn": true, "run": []string{v2mBoundsLine, "pf step ask " + shellQuote(question)},
			"on_message": map[string]any{"do": "envelope", "id": "fix", "outcome": "intent",
				"run":         []string{x.py("intent.py", "fix", "cancel", "the owner agrees: cancel here and re-file on develop")},
				"intent_file": "artifacts/fix-intent.json"}},
	}, nil)
	d := x.start(t, wi)

	waitUntil(t, 300*time.Second, "the awaiting_owner event of fix's pf step ask", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) > 0
	})
	capDir := d.capsuleDir(wi)
	rewinds := v2mRewinds(t, d, wi)
	if len(rewinds) != 1 || !strings.Contains(rewinds[0], `"gate":"review"`) || !strings.Contains(rewinds[0], `"target":"fix"`) {
		t.Fatalf("review's FAIL on the out-of-bounds change rewinds to fix: %v", rewinds)
	}
	if rv := v2mHandoffs(t, capDir, "review"); len(rv) != 1 || rv[0].Outcome != stepcall.OutcomeOK || rv[0].Handoff.Status != stepcall.StatusFail ||
		!strings.Contains(rv[0].Handoff.Reason, "out of bounds") || !strings.Contains(rv[0].Handoff.Reason, "src/app.go") {
		t.Fatalf("review's hand-off is its fail naming the refused path: %+v", rv)
	}
	if exits := w374RunExits(t, capDir, v2mReviewBoundsLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("review's own micro-bounds run is out of bounds (exit 1): %v", exits)
	}
	rb := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "review", "micro-bounds.json"))
	if det, _ := rb["details"].(map[string]any); rb["verdict"] != "fail" || !strings.Contains(fmt.Sprint(det["reason"]), "src/app.go") {
		t.Fatalf("review's micro-bounds result names the refused path: %v", rb)
	}
	fmt.Println("PASS O1 review FAILed to fix on its own out-of-bounds micro-bounds run")

	ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0])
	if ev["step"] != "fix" || ev["question"] != question {
		t.Fatalf("awaiting_owner body: %v", ev)
	}
	if exits := w374RunExits(t, capDir, v2mBoundsLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("fix's micro-bounds run is out of bounds (exit 1): %v", exits)
	}
	out := d.pf(t, "answer", wi, "--text", "同意，这张票取消，我去 develop 重新立", "--by", "usr_root")
	if !strings.Contains(out, "recorded for step fix") {
		t.Fatalf("pf answer: %s", out)
	}
	x.inst.waitWIStatus(t, wi, "canceled", 120*time.Second)
	if kinds := d.eventKinds(t, wi); !contains(kinds, "owner_message") {
		t.Fatalf("the owner's message is on the event record: %v", kinds)
	}
	// fix's cancel is on the call ledger: the hand-off of its asking
	// dispatch, citing the owner's reply
	reply := v2mOwnerAt(t, capDir, "fix")
	if got := strings.Join(v2mStatuses(t, capDir, "fix"), ","); got != "done,cancel" {
		t.Fatalf("fix's hand-offs on the call ledger = %q, want done,cancel", got)
	}
	if fx := v2mHandoffs(t, capDir, "fix"); reply == "" || fx[1].Handoff.PerOwner != reply || fx[1].Spawn != 3 {
		t.Fatalf("fix's cancel (dispatch %d) must cite the owner's reply %q: per_owner %q", fx[1].Spawn, reply, fx[1].Handoff.PerOwner)
	}
	if n, _ := readExecution(t, capDir).RewindStats["count"].(float64); n != 1 {
		t.Fatalf("the cancel charges nothing: %v", readExecution(t, capDir).RewindStats)
	}
	if st := x.w.prState("demo", "1"); st != "" {
		t.Fatalf("nothing is published: PR state %q", st)
	}
	if out := gitRun(t, filepath.Join(x.w.root, "demo.git"), "branch", "--list", branch); strings.TrimSpace(out) != "" {
		t.Fatalf("nothing is pushed: %q", out)
	}
	fmt.Println("PASS O2 out of bounds: fix asked the owner to re-file, and handed off cancel citing the owner's reply")
}

func TestV2MicroEvidenceWaivedByIntent(t *testing.T) {
	x, wi, branch := newV2MicroWorld(t)
	// main brings a red check the change never touched: the declared
	// check now also needs docs/index.md
	red := x.mainMoves(map[string]string{"checks/links.sh": v2mLinks + "test -f docs/index.md || { echo 'docs/index.md: missing'; exit 1; }\n"}, "main: the link check needs an index")
	question := "evidence is red on sh checks/links.sh: docs/index.md is missing — a check main brought in that this change never touched: run again, waive it, or fix it?"
	x.inst.setPlanFull(t, wi, []map[string]any{
		x.fixAction("The new first line."),
		x.reviewAction(red),
		x.syncAction(),
		// evidence: red on main's check; the agent asks and ends its turn
		// (a step holding the heavy admission leaves no warm process: the
		// owner's reply resumes its session as the next dispatch) ...
		{"do": "envelope", "id": "evidence", "end_turn": true, "run": []string{v2mRunLine, "pf step ask " + shellQuote(question)}},
		// ... which reads the reply and hands off waive citing it, its
		// reading as --reason (this round's result is reused, not run again)
		{"do": "envelope", "id": "evidence", "outcome": "gate_failed",
			"output":      map[string]any{"verdict": "fail", "fail_reason": "sh checks/links.sh exit 1: docs/index.md is missing (main's check, not this change's); the owner said 放行: ship it despite main's red check", "summary": "red, waived by the owner"},
			"run":         []string{x.py("intent.py", "evidence", "waive", "the owner said 放行")},
			"intent_file": "artifacts/evidence-intent.json"},
		// publish reads evidence's waive hand-off of the red run result's
		// round
		x.publishAction(branch),
		x.mergeAction(),
	}, nil)
	d := x.start(t, wi)

	waitUntil(t, 300*time.Second, "the awaiting_owner event of evidence's pf step ask", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) > 0
	})
	if ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0]); ev["step"] != "evidence" {
		t.Fatalf("awaiting_owner body: %v", ev)
	}
	if out := d.pf(t, "answer", wi, "--text", "放行，这个红是 main 带进来的检查，与本票无关", "--by", "usr_root"); !strings.Contains(out, "recorded for step evidence") {
		t.Fatalf("pf answer: %s", out)
	}
	waitWorkflowStatus(t, d, wi, "completed", 300*time.Second)
	x.inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x2 := readExecution(t, capDir)

	// evidence's hand-off on the call ledger: its asking dispatch handed
	// off nothing, the resumed one waive citing the owner's reply
	reply := v2mOwnerAt(t, capDir, "evidence")
	if got := strings.Join(v2mStatuses(t, capDir, "evidence"), ","); got != "waive" {
		t.Fatalf("evidence's hand-offs on the call ledger = %q, want waive", got)
	}
	evh := v2mHandoffs(t, capDir, "evidence")[0]
	if reply == "" || evh.Handoff.PerOwner != reply {
		t.Fatalf("evidence's waive must cite the owner's reply %q: %+v", reply, evh.Handoff)
	}
	if n, _ := x2.RewindStats["count"].(float64); n != 0 {
		t.Fatalf("a waived FAIL charges no rewind: %v", x2.RewindStats)
	}
	if kinds := d.eventKinds(t, wi); !contains(kinds, "owner_waived") {
		t.Fatalf("the owner's waiver must be on the event record: %v", kinds)
	}
	run := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "run", "result.json"))
	if run["verdict"] != "fail" {
		t.Fatalf("the result file stays the command's (fail): %v", run["verdict"])
	}
	if exits := w374RunExits(t, capDir, v2mRunLine); len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("run.py ran once (red), the resumed conversation reused its result: %v", exits)
	}
	if fmt.Sprint(evh.Round) != fmt.Sprint(run["round"]) {
		t.Fatalf("the waiver belongs to the run result's round: waive hand-off round %d, run round %v", evh.Round, run["round"])
	}
	check := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "publish-check.json"))
	if cw, _ := check["waived"].(map[string]any); check["proceed"] != true || cw == nil || cw["per_owner"] != reply || !strings.Contains(fmt.Sprint(cw["words"]), "放行") {
		t.Fatalf("publish must have read evidence's waive hand-off and the owner's words its header quotes beside the red run result: %v", check)
	}
	head := strings.TrimSpace(gitRun(t, filepath.Join(capDir, "repos", "demo"), "rev-parse", "HEAD"))
	got, err := x.inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	// the wrap's attrs.waivers: the daemon's record of the waiver — the
	// owner's words verbatim, the agent's reading, the round of the red run
	ws, _ := got.Attrs["waivers"].([]any)
	var w0 map[string]any
	if len(ws) == 1 {
		w0, _ = ws[0].(map[string]any)
	}
	if w0 == nil || w0["gate"] != "evidence" || w0["owner_message_at"] != reply || !strings.Contains(fmt.Sprint(w0["owner_words"]), "放行") ||
		!strings.Contains(fmt.Sprint(w0["agent_reason"]), "放行") || fmt.Sprint(w0["round"]) != fmt.Sprint(run["round"]) {
		t.Fatalf("the wrap must carry the daemon's waiver of evidence with the owner's words and the agent's reading: %v", got.Attrs)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	repos, _ := in["repos"].([]any)
	var r0 map[string]any
	if len(repos) == 1 {
		r0, _ = repos[0].(map[string]any)
	}
	if in["state"] != "landed" || r0 == nil || r0["head"] != head || r0["light_verified_head"] != head {
		t.Fatalf("the landing must carry the heads the red run tested: %v", got.Attrs)
	}
	if x.w.prState("demo", "1") != "MERGED" {
		t.Fatalf("the PR must be merged: %q", x.w.prState("demo", "1"))
	}
	fmt.Println("PASS W1 waive on evidence citing the owner: no rewind, publish proceeds on the waive hand-off, the wrap's waivers carry the owner's words")
}

func TestV2MicroSyncRewindToFixAborts(t *testing.T) {
	x, wi, branch := newV2MicroWorld(t)
	// main rewrites the guide's first line the change rewords: a conflict
	premise := x.mainMoves(map[string]string{"docs/guide.md": "# Guide\n\nMain's own first line.\n"}, "main: the guide's first line")
	question := "docs/guide.md conflicts with main's rewrite of the very line this ticket rewords; resolving the hunk would pick one wording over the other — send it back to fix?"
	x.inst.setPlanFull(t, wi, []map[string]any{
		x.fixAction("The new first line."),
		x.reviewAction(premise),
		// sync: the real conflict (exit 1), not the agent's to resolve: it
		// asks; on the owner's reply it aborts the sync, then hands off
		// rewind to fix citing the reply
		{"do": "envelope", "id": "sync", "end_turn": true, "run": []string{w374SyncLine, "pf step ask " + shellQuote(question)},
			"on_message": map[string]any{"do": "envelope", "id": "sync", "outcome": "intent",
				"run":         []string{v2mAbortLine, x.py("intent.py", "sync", "rewind", "the owner sends it back: fix adapts to main's line", "fix")},
				"intent_file": "artifacts/sync-intent.json"}},
		// fix, resumed by the owner's send-back, adapts to main's line
		x.fixAction("Main's own first line."),
		x.reviewAction(), x.syncAction(), x.evidenceAction(nil), x.publishAction(branch), x.mergeAction(),
	}, nil)
	d := x.start(t, wi)

	waitUntil(t, 300*time.Second, "the sync agent's ask", func() bool {
		for _, e := range v26cAwaiting(t, d.dataDir, wi) {
			if v26cBody(e)["step"] == "sync" {
				return true
			}
		}
		return false
	})
	d.pf(t, "answer", wi, "--text", "对，退回 fix，按 main 的写法改", "--by", "usr_root")
	waitWorkflowStatus(t, d, wi, "completed", 300*time.Second)
	x.inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	repo := filepath.Join(capDir, "repos", "demo")

	if exits := w374RunExits(t, capDir, v2mAbortLine); len(exits) != 1 || exits[0] != 0 {
		t.Fatalf("the sync agent aborted the sync before handing back: the abort line exits %v", exits)
	}
	if exits := w374RunExits(t, capDir, w374SyncLine); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("sync.py exits (the real conflict, then a clean merge over fix's commit): %v", exits)
	}
	if refs := strings.TrimSpace(gitRun(t, repo, "for-each-ref", "refs/polyforge/discarded/")); refs != "" {
		t.Fatalf("nothing fix committed may be discarded: %s", refs)
	}
	sres := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "sync", "result.json"))
	srepos, _ := sres["details"].(map[string]any)["repos"].([]any)
	var s0 map[string]any
	if len(srepos) == 1 {
		s0, _ = srepos[0].(map[string]any)
	}
	if s0 == nil || s0["discarded"] != nil || sres["verdict"] != "pass" {
		t.Fatalf("the next sync started over from fix's commit, nothing discarded: %v", sres)
	}
	gitRun(t, repo, "merge-base", "--is-ancestor", fmt.Sprint(s0["before"]), "HEAD")
	if !strings.Contains(gitRun(t, repo, "log", "-1", "--format=%s", fmt.Sprint(s0["before"])), "docs: the guide") {
		t.Fatalf("the next sync's before is fix's adapting commit: %v", s0["before"])
	}
	// the next sync's verify-first: HEAD is the head review's second
	// hand-off stamped
	if checks := strings.Join(strings.Fields(v1ReadString(filepath.Join(capDir, "artifacts", "sync-checks.log"))), " "); checks != "reviewed" {
		t.Fatalf("the next sync's verify-first reading (the reviewed head) = %q", checks)
	}
	// the sync agent's rewind on the call ledger: to fix, citing the
	// owner's reply; the next sync's done
	reply := v2mOwnerAt(t, capDir, "sync")
	if got := strings.Join(v2mStatuses(t, capDir, "sync"), ","); got != "rewind,done" {
		t.Fatalf("sync's hand-offs on the call ledger = %q, want rewind,done", got)
	}
	if sh := v2mHandoffs(t, capDir, "sync")[0].Handoff; reply == "" || sh.To != "fix" || sh.PerOwner != reply {
		t.Fatalf("sync's rewind must name fix and cite the owner's reply %q: %+v", reply, sh)
	}
	x2 := readExecution(t, capDir)
	if kinds := d.eventKinds(t, wi); !contains(kinds, "owner_rewind") {
		t.Fatalf("the sync agent's rewind is the owner's send-back: %v", kinds)
	}
	rws, _ := x2.RewindStats["rewinds"].([]any)
	if len(rws) != 1 || rws[0].(map[string]any)["class"] != "owner" || rws[0].(map[string]any)["gate"] != "sync" || rws[0].(map[string]any)["to_step"] != "fix" {
		t.Fatalf("the only rewind is the owner's send-back from sync to fix (class owner): %v", x2.RewindStats)
	}
	got, err := x.inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	if in, _ := got.Attrs["integration"].(map[string]any); in["state"] != "landed" {
		t.Fatalf("the change lands: %v", got.Attrs)
	}
	fmt.Println("PASS S1 micro sync aborted before handing back to fix; fix's commit kept and landed")
}

func TestV2MicroEvidenceFailsOnASyncArgvMismatch(t *testing.T) {
	x, wi, branch := newV2MicroWorld(t)
	// the sync skill's start line with one argument more: the result file
	// it writes is no result of the skill's line
	deviant := w374SyncLine + " --deadline 19m"
	x.inst.setPlanFull(t, wi, []map[string]any{
		x.fixAction("The new first line."),
		x.reviewAction(),
		x.syncActionLine(deviant),
		// evidence checks sync's argv first: fail to fix (charged), nothing run
		x.evidenceAction(nil),
		// the lane again as the skills write it: it lands
		x.fixAction("The new first line."),
		x.reviewAction(), x.syncAction(), x.evidenceAction(nil), x.publishAction(branch), x.mergeAction(),
	}, nil)
	d := x.start(t, wi)
	waitWorkflowStatus(t, d, wi, "completed", 600*time.Second)
	x.inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)

	if got := strings.Join(v2mStatuses(t, capDir, "evidence"), ","); got != "fail,done" {
		t.Fatalf("evidence's hand-offs = %q, want fail (the argv mismatch) then done", got)
	}
	ev := v2mHandoffs(t, capDir, "evidence")[0]
	if !strings.Contains(ev.Handoff.Reason, "sync hand-off mismatch: argv") || !strings.Contains(ev.Handoff.Reason, "19m") {
		t.Fatalf("evidence's fail must name sync's argv against the skill's lines: %q", ev.Handoff.Reason)
	}
	// the deviant line ran (and synced); evidence's check refused its
	// result, so the evidence line did not run then (its gate exits 1),
	// and ran green after the honest sync
	if exits := w374RunExits(t, capDir, deviant); len(exits) != 1 || exits[0] != 0 {
		t.Fatalf("the deviant sync line: %v", exits)
	}
	if exits := w374RunExits(t, capDir, "evidence-check.py"); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("evidence's check of sync's argv (refused, then held): %v", exits)
	}
	if exits := w374RunExits(t, capDir, "test -f artifacts/evidence-check.ok && "+v2mRunLine); len(exits) != 2 || exits[0] != 1 || exits[1] != 0 {
		t.Fatalf("the evidence line runs only over a sync result of the skill's line: %v", exits)
	}
	rewinds := v2mRewinds(t, d, wi)
	if len(rewinds) != 1 || !strings.Contains(rewinds[0], `"gate":"evidence"`) || !strings.Contains(rewinds[0], `"target":"fix"`) {
		t.Fatalf("one charged rewind, evidence's FAIL back to fix: %v", rewinds)
	}
	if x.w.prState("demo", "1") != "MERGED" {
		t.Fatalf("the lane run as the skills write it lands: %q", x.w.prState("demo", "1"))
	}
	fmt.Println("PASS A1 a sync result of another line than the skill's is evidence's FAIL; the skills' lines land")
}
