// carries: internal/polyforge/driver/integrate*, internal/polyforge/daemon/keyedfile.go
// carries: workflows/_shared/scripts/integrate.py, workflows/_shared/scripts/gate.py, workflows/_shared/scripts/revert.py, workflows/_shared/scripts/pfstep/integration.py, workflows/_shared/scripts/pfstep/gitmerge/*
// carries: internal/polyforge/daemon/exclusive.go, internal/aihub/core/attrs.go, workflows/integrate/*
// carries: internal/polyforge/pendingwrites/*, internal/polyforge/stepcall/*, internal/polyforge/daemon/stephub.go

package test

// w404_integrate_e2e_test.go (workflow contract v2 change
// 13, on contract 4): the integration lane end to end
// through the real aihub, the real daemon and Go driver, the lane's four
// agent steps played by fakepi — each step's scripted agent runs the
// skill's pre-written command lines verbatim (`integrate.py collect`,
// `revert.py --ops-out` (the revert script), the gate
// script `gate.py --capsule --pins` / `--triage`, each
// from the capsule's lane snapshot under the daemon's PF_PYTHON,
// `integrate.py close --rulings --decision --calls --ops-out --record-out`,
// each reading the project's pool itself through the step's hub; the pf
// under test reached through the symlinks tools/install.sh lays out),
// writes the files the skill names (rulings.json, gate-decision.json, the
// ops files), registers the hub writes with the skill's `pf step hub batch`
// / `wrap-attrs` lines and hands off as the skill routes (w404Step, a
// python glue; fakepi's `pf step handoff`); the real release gate
// (gate.py: the tree's own scope engine planning `sh tests/check.sh`, every
// command through a fake docker that runs it here)
// and the lane's fake gh (internal/polyforge/driver/testdata/integrate/
// fake-gh.sh: PRs as files). Each work item lands as the develop lane
// leaves it: its PR merged into the local bare origin's main (a --no-ff
// merge) and its wrap carrying the hand-off `integration: {state: landed,
// repos, merge_commits}`. No real remote, container or GitHub is touched;
// the green tag lands on the bare origin; every aihub write of the batch
// is a hub write a step registered, made by the daemon when the step hands
// off done (runtime-state/step-calls.jsonl's exec lines).
//
//   - batch 1 (filed as a person files one: `pf file create --bundle
//     integrate`, then `pf submit`; while it runs a second create on the
//     lane is refused CORE-409 OPEN_ON_BUNDLE, naming it — the lane's
//     `exclusive: true`, through the daemon's proxy and aihub's exclusive
//     create): two landed items — collect pins main
//     and takes both, revert copies the pins, full-gate's `gate.py
//     --mark-green` is green once on the pinned head and tags it
//     green/<YYYYMMDD>-<n>, close's moves (integrate.py close --ops-out,
//     registered) move both records to merged with the batch and the tag,
//     the batch's record (--record-out, registered as the wrap attr
//     integration_batch) rides its wrap; the lane merged no PR;
//   - batch 2: an item breaking the gate and an unrelated one landed —
//     the gate red, `gate.py --triage` re-runs the failing test by the
//     conf's rerun= template, finds it green at the newest green tag and
//     attributes it to the breaking item by the bisection; the agent's
//     file write files one develop ticket (high priority, the stable key,
//     an acceptance naming the red unit — AC3) and its
//     record write (`integrate.py records`, a blame entry) records the
//     round on the source with the gated head as red_heads;
//     nothing tagged; the record is red and names the ticket (read from the
//     call ledger); both items stay landed at aihub;
//   - batch 3, main not moved since: the pool carries the red batch's
//     head (read from its wrap at aihub), nothing is gated, no gate runs —
//     the batch ends empty;
//   - batch 4a, the fix ticket canceled: collect finds the hold stuck and
//     asks the owner (`pf step ask`, the awaiting_owner state); the owner's
//     words (`pf answer --text`, relayed into the same process) say fix —
//     a new ticket (its key stable across batches) filed and registered on
//     the held item by its record write (`integrate.py records`, a fix
//     entry; collect-ops.json); batch 4b finds the hold
//     open on it and asks nobody;
//   - batch 4, the new ticket canceled too: the hold is stuck again, the
//     owner's words say revert — collect's rulings.json names it, the
//     revert step reverts the item from the hold's record under the merge
//     lock (a revert PR opened and merged by the fake gh), its record moves
//     to reverted (revert.py --ops-out, registered), full-gate gates the
//     revert's merge green and tags it, close moves the innocent item to
//     merged and records the ruling (the owner's words verbatim) and the
//     revert;
//   - batch 5, a breaking item landed again: full-gate puts the red triage
//     to the owner (`pf step ask`), the owner's words say revert — the
//     gate writes the revert into gate-decision.json and hands off fail
//     citing the owner's reply (--per-owner: an owner-routed rewind,
//     charged to nothing), the revert step's next round reads the item from
//     gate-decision.json, the revert lands and the gate runs again on the
//     revert's merge, green, in the same batch.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW404IntegrateLaneEndToEnd$' -count=1 -v -timeout 25m

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w404Docker is the gate's fake container engine (the release tests'
// shape): the image built from stdin, a container whose --workdir execs
// run here with the image's PATH only.
const w404Docker = `#!/bin/sh
case "$1" in
  version) echo 29.0 ;;
  image) case "$2" in
      ls) [ -e "$CALLLOG.gateimage" ] && echo sha256:gate ;;
      inspect) echo '["PATH=/usr/bin:/bin"]' ;;
    esac ;;
  run) echo "docker run $*" >> "$CALLLOG"; echo gatecid ;;
  rm) echo "docker rm $*" >> "$CALLLOG" ;;
  exec) shift; wd=""; envf=$(mktemp)
    while [ $# -gt 0 ]; do case "$1" in
      --workdir) wd=$2; shift 2 ;;
      --env) printf '%s\n' "$2" >> "$envf"; shift 2 ;;
      *) break ;;
    esac; done
    shift
    if [ -z "$wd" ]; then
      rm -f "$envf"
      case "$*" in *pf-probe:*) exec "$@" ;; esac
      exit 0
    fi
    echo "docker exec $*" >> "$CALLLOG"
    mkdir -p "$CALLLOG.gatetmp" "$CALLLOG.gatehome"
    while IFS= read -r kv; do
      case "$kv" in TMPDIR=*) kv="TMPDIR=$CALLLOG.gatetmp/t" ;; esac
      set -- "$kv" "$@"
    done < "$envf"
    rm -f "$envf"
    cd "$wd" || exit 125
    exec env -i PATH=/usr/bin:/bin HOME="$CALLLOG.gatehome" "$@" ;;
  build)
    for last; do :; done
    if [ "$last" = - ]; then cat > "$CALLLOG.gatedockerfile"; : > "$CALLLOG.gateimage"; exit 0; fi ;;
esac
exit 0
`

// w404Check is the gate's one command: green unless the tree carries a
// file named broken (a Go-test-shaped output, so the failing unit is
// located and its file found).
const w404Check = `#!/bin/sh
if [ -e broken ]; then
  echo "--- FAIL: TestBroken (0.00s)"
  echo "    broken_test.go:3: the tree carries broken"
  echo "FAIL"
  echo "FAIL	example.com/demo/pkg	0.01s"
  exit 1
fi
echo "--- PASS: TestBroken (0.00s)"
echo "PASS"
echo "ok  	example.com/demo/pkg	0.01s"
`

// w404Step is the scripted integrate agent's glue (python3, on the fake
// bin dir as `w404-step`): for one step it runs the skill's pre-written
// command lines exactly as the skill writes them (reusing this round's
// result when it exists, as the skill says), writes the files the skill
// names (rulings.json, gate-decision.json, the ops files) and registers
// the hub writes with the skill's own `pf step hub batch` / `wrap-attrs`
// lines — the mechanical reading a real agent does — then leaves the
// hand-off's summary (and, on the owner's revert, the fail and the reply
// it cites) in files fakepi's `pf step handoff` reads (plan `output_file`
// / `intent_file`). The decisions a scenario scripts are flags: `collect
// --ask` puts the stuck holds to the owner (`pf step ask`) and ends there,
// `collect --revert <wi>` / `--fix <wi>` / `--clear <wi>` are the owner's
// rulings on a stuck hold (the owner's words and their stamp read off
// runtime-state/asks.jsonl), `full-gate --ask` puts a red triage to the
// owner and `full-gate --decide revert:<wi>` is the owner's revert on it.
// It never reads runtime-state/execution.json. Where the skill states a
// rule the glue must not paraphrase, the glue reads the skill off the
// capsule's lane snapshot (revert's id rule).
const w404Step = `#!/usr/bin/env python3
import glob, json, os, subprocess, sys

ART = "artifacts/integrate"
WI = os.environ.get("PF_WI_ID", "")
ROUND = os.environ.get("PF_ROUND", "")
BT = chr(96)  # a backtick: the Go raw string carrying this script cannot hold one

def load(name):
    p = os.path.join(ART, name)
    if not os.path.exists(p):
        return None
    with open(p) as f:
        return json.load(f)

def write(name, obj):
    with open(os.path.join(ART, name), "w") as f:
        json.dump(obj, f)

def run(line):
    # the skill's line, verbatim, as a real agent's shell runs it
    return subprocess.run(line, shell=True, cwd=os.getcwd()).returncode

def must(line):
    # a registration line: the step goes on only once it was accepted
    rc = run(line)
    if rc != 0:
        raise SystemExit("%s: exit %d" % (line, rc))

def fresh(name):
    r = load(name)
    return r is not None and r.get("round") == ROUND

def round_no():
    return int(ROUND) if ROUND.isdigit() else ROUND

def owner_said(step):
    # the owner's latest message to this step (a reply or message line of
    # runtime-state/asks.jsonl): the stamp a ruling cites, and its words
    last = None
    with open("runtime-state/asks.jsonl") as f:
        for text in f:
            try:
                l = json.loads(text)
            except ValueError:
                continue
            if l.get("step") == step and l.get("kind") in ("reply", "message"):
                last = l
    if last is None:
        raise SystemExit("no owner message to %s in runtime-state/asks.jsonl" % step)
    return last["at"], last.get("text", "")

def flag(args, name):
    out = []
    for i, a in enumerate(args):
        if a == name and i + 1 < len(args):
            out.append(args[i + 1])
    return out

def collect(args):
    if not fresh("batch.json"):
        run('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" collect --json --out artifacts/integrate/batch.json')
    b = load("batch.json"); d = b["details"]
    stuck = [h for h in d.get("holds", []) if h.get("stuck")]
    if "--ask" in args:
        lines = ["Stuck holds need your ruling (revert / clear / fix):"]
        for h in stuck:
            lines.append("- %s (%s): %s; red rounds %d; fix tickets %s; PRs %s" % (h["wi"], h.get("name", ""), h["reason"], h.get("red_rounds", 0), ", ".join(h.get("fix_tickets") or []), ", ".join(h.get("prs") or [])))
        subprocess.run(["pf", "step", "ask", "\n".join(lines)], check=True)
        return
    holds = {h["wi"]: h for h in d.get("holds", [])}
    ruled = flag(args, "--revert") + flag(args, "--fix") + flag(args, "--clear")
    at, words = owner_said("collect") if ruled else ("", "")
    reverts, rulings, ops, clears = [], [], [], []
    for wi in flag(args, "--revert"):
        reverts.append(wi); rulings.append({"item": wi, "ruling": "revert", "words": words, "at": at})
    for wi in flag(args, "--fix"):
        # the skill's fix ruling: a ticket under the key stable across
        # batches, then its registration on the held item (the hold's
        # fix_tickets, every one, plus it)
        h = holds[wi]
        fixes = list(h.get("fix_tickets") or [])
        key = "hold-fix/%s/%d" % (wi, len(fixes))
        sources = "".join("- %s\n" % s for s in [wi] + (h.get("prs") or []))
        content = ("%s was blamed by a red batch; its fix tickets (%s) released nothing: %s\n\n## Sources\n%s\n## Acceptance\n- the full gate is green on the fixed head for the failures attributed to %s\n"
                   % (wi, ", ".join(fixes), h["reason"], sources, wi))
        ops.append({"verb": "file", "key": key, "lane": "develop", "goal": "integrate: a new fix for %s, which still holds the green tag" % h.get("name", wi),
                    "content": content, "priority": "high", "labels": ["integration-fix"]})
        ops.append({"item": wi, "fix": ["key:" + key]})
        rulings.append({"item": wi, "ruling": "fix", "words": words, "at": at})
    for wi in flag(args, "--clear"):
        clears.append({"item": wi, "clear": at})
        rulings.append({"item": wi, "ruling": "clear", "words": words, "at": at})
    write("rulings.json", {"round": round_no(), "reverts": reverts, "hold_rulings": rulings})
    if ops or clears:
        write("collect-records.json", ops + clears)
        run('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" records --in artifacts/integrate/collect-records.json --ops-out artifacts/integrate/collect-ops.json --json --out artifacts/integrate/collect-records-result.json')
        rr = load("collect-records-result.json")
        if rr["exit_code"] != 0:
            raise SystemExit("integrate.py records exit %d: %s" % (rr["exit_code"], rr["summary"]))
        must("pf step hub batch @artifacts/integrate/collect-ops.json")
    write("collect-output.json", {"summary": b["summary"]})

def skill_allows_holds():
    # the revert skill's id rule, as the capsule's lane snapshot words it:
    # held items count only when the skill names details.holds
    for p in glob.glob("workflows/*/skills/revert.md"):
        with open(p) as f:
            if "details.holds" in " ".join(f.read().split()):
                return True
    return False

def revert(args):
    # the items to revert, read from files: collect's rulings, plus the
    # owner's revert after a red gate (full-gate's decision)
    items = list((load("rulings.json") or {}).get("reverts") or [])
    for wi in (load("gate-decision.json") or {}).get("reverts") or []:
        if wi not in items:
            items.append(wi)
    bd = load("batch.json")["details"]
    allowed = {it["id"] for it in bd.get("items", [])}
    if skill_allows_holds():
        allowed |= {h["wi"] for h in bd.get("holds", [])}
    dropped = [i for i in items if i not in allowed]
    if dropped:
        subprocess.run(["pf", "step", "ask", "these ids are no item of the batch: " + ", ".join(dropped)], check=False)
    items = [i for i in items if i in allowed]
    r = load("revert.json")
    done_before = {e["wi"] for e in (r or {"details": {"reverts": []}})["details"]["reverts"] if e.get("done")}
    if not (fresh("revert.json") and all(i in done_before for i in items) and load("revert-ops.json") is not None):
        line = '"$PF_PYTHON" "$PF_SHARED_DIR/scripts/revert.py" --batch artifacts/integrate/batch.json'
        for i in items:
            line += " --item " + i
        run(line + " --ops-out artifacts/integrate/revert-ops.json --json --out artifacts/integrate/revert.json")
        r = load("revert.json")
    if r["exit_code"] != 0:
        raise SystemExit("revert.py exit %d: %s" % (r["exit_code"], r["summary"]))
    must("pf step hub batch @artifacts/integrate/revert-ops.json")
    write("revert-output.json", {"summary": r["summary"]})

def decision(result, reverts=()):
    # full-gate's decision file, written before every hand-off
    write("gate-decision.json", {"round": round_no(), "result": result, "reverts": list(reverts), "reruns": 0})

def fullgate(args):
    decide = (flag(args, "--decide") or [""])[0]
    rv = load("revert.json"); heads = rv["heads"]
    if not rv["details"].get("gated"):
        decision("empty")
        write("full-gate-output.json", {"summary": "nothing gated"}); return
    g = load("gate.json")
    if not (fresh("gate.json") and g.get("heads") == heads):
        run("\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/gate.py\" --isolation container --capsule . --pins artifacts/integrate/revert.json --mark-green --summary \"integration batch $PF_WI_ID\" --json --out artifacts/integrate/gate.json")
        g = load("gate.json")
    if g["exit_code"] == 0:
        decision("green")
        write("full-gate-output.json", {"summary": g["summary"]}); return
    if g["exit_code"] != 1:
        raise SystemExit("gate exit %d: %s" % (g["exit_code"], g["summary"]))
    t = load("triage.json")
    if not (fresh("triage.json") and t.get("heads") == heads):
        run("\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/gate.py\" --isolation container --triage --capsule . --pins artifacts/integrate/revert.json --gate artifacts/integrate/gate.json --batch artifacts/integrate/batch.json --mark-green --summary \"integration batch $PF_WI_ID\" --json --out artifacts/integrate/triage.json")
        t = load("triage.json")
    if "--ask" in args and t["exit_code"] == 1:
        units = ["%s %s (%s, %s)" % (u["repo"], u["unit"], u["class"], u.get("item", "")) for u in t["details"]["units"]]
        subprocess.run(["pf", "step", "ask", "The gate is red: " + "; ".join(units) + ". revert <item> / rerun / close?"], check=True)
        return
    if t["exit_code"] == 0:
        decision("green")
        write("full-gate-output.json", {"summary": t["summary"]}); return
    if t["exit_code"] != 1:
        raise SystemExit("triage exit %d: %s" % (t["exit_code"], t["summary"]))
    if decide.startswith("revert:"):
        # the owner's revert: the decision names it, the hand-off is a fail
        # citing the owner's reply, no hub writes (a fail drops them)
        wi = decide.split(":", 1)[1]
        at, words = owner_said("full-gate")
        decision("red", [wi])
        write("full-gate-output.json", {"verdict": "fail", "fail_reason": "the owner rules %s reverted: %s" % (wi, words), "summary": t["summary"]})
        write("full-gate-intent.json", {"kind": "one_more_round", "reply_at": at})
        return
    items = {it["id"]: it for it in load("batch.json")["details"]["items"]}
    by_item = {}
    for u in t["details"]["units"]:
        if u["class"] == "attributed":
            by_item.setdefault(u["item"], []).append(u)
    files, moves = [], []
    for item, us in sorted(by_item.items()):
        key = "%s/fix/%s/0" % (WI, item)
        it = items.get(item, {})
        prs = [r.get("pr") for r in it.get("repos", []) if r.get("pr")]
        names = sorted({u["unit"] for u in us})
        excerpt = "\n".join(u.get("excerpt", "") for u in us)
        content = "The change made the gate red.\n\n%s\n\nSource PRs: %s\n\n## Sources\n" % (excerpt, " ".join(prs))
        content += "".join("- %s\n" % s for s in [item] + prs)
        content += "\n## Units\n" + "".join("- %s\n" % n for n in names)
        content += "\n## Acceptance\n" + "".join("- %s%s%s passes on the fixed head (the full gate, %s, sh tests/check.sh)\n" % (BT, n, BT, us[0]["repo"]) for n in names)
        files.append({"verb": "file", "key": key, "lane": "develop", "goal": "%s: %s fails since %s" % (us[0]["repo"], ", ".join(names), item),
                      "content": content, "priority": "high", "labels": ["integration-fix"]})
        moves.append({"item": item, "blame": ["key:" + key]})
    decision("red")
    if files:
        write("full-gate-records.json", files + moves)
        run('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" records --in artifacts/integrate/full-gate-records.json --pins artifacts/integrate/revert.json --ops-out artifacts/integrate/full-gate-ops.json --json --out artifacts/integrate/full-gate-records-result.json')
        rr = load("full-gate-records-result.json")
        if rr["exit_code"] != 0:
            raise SystemExit("integrate.py records exit %d: %s" % (rr["exit_code"], rr["summary"]))
        must("pf step hub batch @artifacts/integrate/full-gate-ops.json")
    write("full-gate-output.json", {"summary": t["summary"]})

def close(args):
    if not (fresh("close.json") and load("close-ops.json") is not None and load("integration-batch.json") is not None):
        run('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/integrate.py" close --batch artifacts/integrate/batch.json --revert artifacts/integrate/revert.json --gate artifacts/integrate/gate.json --triage artifacts/integrate/triage.json --rulings artifacts/integrate/rulings.json --decision artifacts/integrate/gate-decision.json --calls runtime-state/step-calls.jsonl --ops-out artifacts/integrate/close-ops.json --record-out artifacts/integrate/integration-batch.json --json --out artifacts/integrate/close.json')
    c = load("close.json")
    if c["exit_code"] != 0:
        raise SystemExit("integrate.py close exit %d: %s" % (c["exit_code"], c["summary"]))
    must("pf step hub batch @artifacts/integrate/close-ops.json")
    must("pf step hub wrap-attrs integration_batch @artifacts/integrate/integration-batch.json")
    write("close-output.json", {"summary": c["summary"]})

{"collect": collect, "revert": revert, "full-gate": fullgate, "close": close}[sys.argv[1]](sys.argv[2:])
`

type w404World struct {
	*deliverWorld
	inst   *v1Install
	origin string
	seed   string
	prs    int
	calls  string
}

func newW404World(t *testing.T) *w404World {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed (the scope engine's --json and the scripted agent's glue need it)")
	}
	dw := newDeliverWorld(t)
	w := &w404World{deliverWorld: dw, calls: filepath.Join(dw.root, "docker.log")}
	w.origin = dw.origin("demo")
	w.seed = filepath.Join(dw.root, "seed-demo")
	gh, err := os.ReadFile(filepath.Join(v1Root(t), "internal", "polyforge", "driver", "testdata", "integrate", "fake-gh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"gh": string(gh), "docker": w404Docker, "w404-step": w404Step} {
		if err := os.WriteFile(filepath.Join(dw.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := v1Root(t)
	engine, _ := os.ReadFile(filepath.Join(root, "workflows", "_shared", "scripts", "verify-scope"))
	gateID, _ := os.ReadFile(filepath.Join(root, "tools", "gate-id"))
	up := "# upgrade\n"
	w.write(map[string]string{
		"workflows/_shared/scripts/verify-scope": string(engine),
		"tools/gate-id":                          string(gateID),
		".polyforge/Dockerfile.verify":           "FROM scratch\n",
		".polyforge/verify-scope.conf":           "stack=none\nfull_command=sh tests/check.sh\nrerun=full-1|sh tests/check.sh {unit}\n",
		".polyforge/integrate.conf":              "append_only=docs/upgrade.md\npin=docs/upgrade.md pin.txt\n",
		"tests/check.sh":                         w404Check,
		"pkg/broken_test.go":                     "package pkg\n\nfunc TestBroken(t *testing.T) {}\n",
		"docs/upgrade.md":                        up,
		"pin.txt":                                w404Sha(up) + "\n",
		"code.txt":                               "one\n",
	})
	_ = os.Chmod(filepath.Join(w.seed, "workflows", "_shared", "scripts", "verify-scope"), 0o755)
	gitRun(t, w.seed, "add", "-A")
	gitRun(t, w.seed, "commit", "-q", "-m", "the gate tree")
	gitRun(t, w.seed, "tag", "-a", "-m", "{}", "green/20261101-1")
	gitRun(t, w.seed, "push", "-q", "origin", "HEAD:refs/heads/main", "refs/tags/green/20261101-1")
	w.inst = newV1InstallRepos(t, []api.ProjectRepo{{Name: "demo", URL: w.origin}})
	w.inst.keepDeliver = true
	return w
}

func w404Sha(s string) string {
	out, err := exec.Command("sh", "-c", "printf %s \"$0\" | sha256sum | cut -c1-64", s).Output()
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(out))
}

func (w *w404World) write(files map[string]string) {
	for rel, s := range files {
		p := filepath.Join(w.seed, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
}

// land is one work item as the develop lane leaves it: a develop work
// item, its branch off origin's main (a section appends the upgrade log
// and moves its pin), its PR merged into main (a --no-ff merge, as the
// forge makes it), and its wrap carrying the landed hand-off with the
// merge commit.
func (w *w404World) land(name, section string, files map[string]string) (wi, pr, merge string) {
	t := w.t
	gitRun(t, w.seed, "fetch", "-q", "origin")
	gitRun(t, w.seed, "checkout", "-q", "-B", name, "origin/main")
	if section != "" {
		raw, _ := os.ReadFile(filepath.Join(w.seed, "docs/upgrade.md"))
		up := string(raw) + "\n## " + section + "\n"
		files["docs/upgrade.md"], files["pin.txt"] = up, w404Sha(up)+"\n"
	}
	w.write(files)
	gitRun(t, w.seed, "add", "-A")
	gitRun(t, w.seed, "commit", "-q", "-m", name)
	gitRun(t, w.seed, "push", "-q", "origin", "HEAD:refs/heads/"+name)
	head := strings.TrimSpace(gitRun(t, w.seed, "rev-parse", "HEAD"))
	w.prs++
	gitRun(t, w.seed, "checkout", "-q", "-B", "landing", "origin/main")
	gitRun(t, w.seed, "merge", "-q", "--no-ff", "-m", fmt.Sprintf("Merge pull request #%d from %s", w.prs, name), name)
	gitRun(t, w.seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	merge = strings.TrimSpace(gitRun(t, w.seed, "rev-parse", "HEAD"))
	d := filepath.Join(w.state, "demo", "prs", fmt.Sprint(w.prs))
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	pr = fmt.Sprintf("https://github.com/fake/demo/pull/%d", w.prs)
	for k, v := range map[string]string{"head": name, "base": "main", "state": "MERGED", "url": pr, "merge": merge} {
		_ = os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644)
	}
	content := "Landed change " + name + ".\n\n## Acceptance\n\n- AC1 it lands\n"
	raw, err := w.inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: v1Project, Goal: "w404 " + name,
		Content: &content, BundleName: strPtr("develop")})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	var item struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &item)
	wi = item.ID
	w.inst.trackWI(wi)
	var b [32]byte
	_, _ = rand.Read(b[:])
	sid := hex.EncodeToString(b[:])
	if _, err := w.inst.client.SessionClaim(api.SessionClaimRequest{WorkItemID: wi, SessionID: sid, MachineID: "w404-developer"}); err != nil {
		t.Fatalf("claim %s: %v", name, err)
	}
	in := map[string]any{"state": "landed",
		"repos":         []any{map[string]any{"repo": "demo", "pr": pr, "head": head, "light_verified_head": head, "merge_commit": merge}},
		"merge_commits": map[string]any{"demo": []any{merge}}}
	if err := w.inst.client.SessionEnd(wi, api.SessionEndRequest{SessionID: sid, Status: api.StatusWrapped, Attrs: map[string]any{"integration": in}}); err != nil {
		t.Fatalf("wrap %s: %v", name, err)
	}
	return wi, pr, merge
}

func (w *w404World) integration(t *testing.T, wi string) map[string]any {
	t.Helper()
	got, err := w.inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	return in
}

// w404BatchGoal is the goal a batch is filed with.
const w404BatchGoal = "integrate: gate the landed work items"

// filer runs pf as the person filing batches does: a cli.toml mapping the
// project (`pf file create` places only a mapped project) and the daemon's
// own socket — no stand-in proxy (v1BusinessEnv interposes one only for a
// command given an aihub URL), so the create reaches aihub through the
// daemon's data-plane proxy, as on a machine; it answers the exit code,
// stdout and stderr. The argv runs exactly as given: unlike v1BusinessCmd
// it appends no --allow-no-acceptance to a submit (the lane
// declares acceptance none, so a batch is submitted as the README files
// it), and a submit carrying the switch is a test defect.
func (w *w404World) filer(t *testing.T, d *v1Daemon, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(w.inst.pfPath, append([]string{"--config", mappingConfigPath(t, d.workspace, []string{v1Project})}, args...)...)
	cmd.Env = v1BusinessEnv(t, append(os.Environ(), "PF_DAEMON_SOCKET="+d.sock))
	if slices.Contains(cmd.Args, "--allow-no-acceptance") {
		t.Fatalf("pf %v: the batch is filed with --allow-no-acceptance, not as the README files it", args)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

// startBatch files a batch as a person does — `pf file create --bundle
// integrate`, then `pf submit <batch>`, as the lane's README writes them,
// with no --allow-no-acceptance (the batch lists no acceptance) — and
// answers its work item.
func (w *w404World) startBatch(t *testing.T, d *v1Daemon) string {
	t.Helper()
	create := []string{"--json", "file", "create", "--project", v1Project, "--bundle", "integrate", "--goal", w404BatchGoal}
	if slices.Contains(create, "--allow-no-acceptance") {
		t.Fatalf("pf %v: not the README's create", create)
	}
	code, out, errOut := w.filer(t, d, create...)
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &wi); code != 0 || err != nil || wi.ID == "" {
		t.Fatalf("pf file create --bundle integrate: exit %d, %v\n%s\n%s", code, err, out, errOut)
	}
	w.inst.trackWI(wi.ID)
	submit := []string{"submit", wi.ID}
	if slices.Contains(submit, "--allow-no-acceptance") {
		t.Fatalf("pf %v: not the README's submit", submit)
	}
	if code, out, errOut := w.filer(t, d, submit...); code != 0 {
		t.Fatalf("pf submit %s: exit %d\n%s\n%s", wi.ID, code, out, errOut)
	}
	return wi.ID
}

// secondBatchRefused files another batch while open is unfinished: the
// create is refused CORE-409 OPEN_ON_BUNDLE, naming open, and nothing is
// filed.
func (w *w404World) secondBatchRefused(t *testing.T, d *v1Daemon, open string) {
	t.Helper()
	code, out, errOut := w.filer(t, d, "--json", "file", "create", "--project", v1Project, "--bundle", "integrate", "--goal", w404BatchGoal)
	var refusal struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Extra  struct {
			Reason string `json:"reason"`
		} `json:"extra"`
	}
	got, err := w.inst.client.GetWorkItem(open)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(errOut), "\n") // the bundle-steps advisory, then the typed envelope
	if code != 1 || json.Unmarshal([]byte(lines[len(lines)-1]), &refusal) != nil ||
		refusal.Code != "CORE-409" || refusal.Extra.Reason != "OPEN_ON_BUNDLE" || !strings.Contains(refusal.Detail, api.DisplayName(got.Slug, got.ID)+" of "+v1Project+" is ") ||
		!strings.Contains(refusal.Detail, "declares exclusive: true") {
		t.Fatalf("a second batch while %s is open: exit %d\nstdout %s\nstderr %s", open, code, out, errOut)
	}
	items, err := w.inst.client.ListWorkItems(aihubclient.WorkItemFilter{Project: v1Project, Bundle: "integrate", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID != open && it.Status != api.StatusWrapped {
			t.Fatalf("the refused create filed %s (%s)", it.ID, it.Status)
		}
	}
}

// w404Action is one step's scripted agent: the glue runs the skill's
// command lines, writes its files and registers its hub writes; fakepi
// then hands off with the summary the glue left (output_file) — done, or
// on the owner's revert after a red gate a fail citing the owner's reply
// (the glue's fail verdict and the reply's stamp, intent_file); extra are
// the scenario's decisions (the glue's flags).
func w404Action(step string, extra ...string) map[string]any {
	return map[string]any{"do": "envelope", "id": step, "outcome": "completed",
		"run":         []string{"w404-step " + step + " " + strings.Join(extra, " ")},
		"output_file": "artifacts/integrate/" + step + "-output.json", "intent_file": "artifacts/integrate/" + step + "-intent.json"}
}

// w404Plan is the four steps' plan, every decision mechanical (a green,
// red or empty batch runs through on it).
func w404Plan() []map[string]any {
	return []map[string]any{w404Action("collect"), w404Action("revert"), w404Action("full-gate"), w404Action("close")}
}

// record answers the batch's integration_batch record from its wrap.
func (w *w404World) record(t *testing.T, batch string) map[string]any {
	t.Helper()
	got, err := w.inst.client.GetWorkItem(batch)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := got.Attrs["integration_batch"].(map[string]any)
	return rec
}

// w404Ledger answers the batch capsule's call ledger
// (runtime-state/step-calls.jsonl) lines of one kind and step, in order.
func w404Ledger(t *testing.T, capDir, kind, step string) []stepcall.Line {
	t.Helper()
	lines, err := stepcall.Read(capDir)
	if err != nil {
		t.Fatalf("the call ledger: %v", err)
	}
	var out []stepcall.Line
	for _, l := range lines {
		if l.Kind == kind && l.Step == step {
			out = append(out, l)
		}
	}
	return out
}

// w404Made answers the hub writes of one verb a step registered that the
// daemon made (the ledger's exec lines, outcome done), in order.
func w404Made(t *testing.T, capDir, step, verb string) []stepcall.Line {
	t.Helper()
	var out []stepcall.Line
	for _, l := range w404Ledger(t, capDir, stepcall.KindExec, step) {
		if l.Verb == stepcall.VerbHubPrefix+verb && l.Outcome == stepcall.ExecDone {
			out = append(out, l)
		}
	}
	return out
}

// w404Handoffs answers a step's accepted hand-offs on the call ledger, in
// order.
func w404Handoffs(t *testing.T, capDir, step string) []*stepcall.Handoff {
	t.Helper()
	var out []*stepcall.Handoff
	for _, l := range w404Ledger(t, capDir, stepcall.KindResult, step) {
		if l.Verb == stepcall.VerbHandoff && l.Outcome == stepcall.OutcomeOK && l.Handoff != nil {
			out = append(out, l.Handoff)
		}
	}
	return out
}

// w404Result reads one of the batch capsule's artifacts/integrate/ files.
func w404Result(t *testing.T, capDir, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "artifacts", "integrate", name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// w404OwnerReply answers the owner's one reply to step in the capsule's
// asks record: what a step's --per-owner cites.
func w404OwnerReply(t *testing.T, capDir, step string) capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(capDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []capsule.AskLine
	for _, l := range lines {
		if l.Step == step && (l.Kind == capsule.AskKindReply || l.Kind == capsule.AskKindMessage) {
			out = append(out, l)
		}
	}
	if len(out) != 1 {
		t.Fatalf("one owner reply to %s: %+v", step, out)
	}
	return out[0]
}

func TestW404IntegrateLaneEndToEnd(t *testing.T) {
	w := newW404World(t)
	a, _, _ := w.land("pa", "a", map[string]string{"a.txt": "a\n"})
	b, _, _ := w.land("pb", "b", map[string]string{"b.txt": "b\n"})

	sep := string(os.PathListSeparator)
	d := newV1Daemon(t, w.inst, optEnv("PATH="+w.bin+sep+v1BinDir+sep+os.Getenv("PATH"), "FAKE_GH_STATE="+w.state, "FAKE_GH_ORIGINS="+w.root,
		"CALLLOG="+w.calls, "GIT_COMMITTER_NAME=pf-e2e", "GIT_COMMITTER_EMAIL=pf-e2e@invalid", "GIT_AUTHOR_NAME=pf-e2e", "GIT_AUTHOR_EMAIL=pf-e2e@invalid"))
	d.start()

	// --- batch 1: the landed items gated once on the pinned head, green
	main1 := strings.TrimSpace(gitRun(t, w.origin, "rev-parse", "main"))
	b1 := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b1) })
	w.secondBatchRefused(t, d, b1)
	fmt.Println("PASS I0 a second batch of the project refused while the first is open (exclusive: true)")
	w.inst.setPlanFull(t, b1, w404Plan(), nil)
	waitWorkflowStatus(t, d, b1, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b1, "wrapped", 60*time.Second)
	if strings.TrimSpace(gitRun(t, w.origin, "rev-parse", "main")) != main1 {
		t.Fatal("the lane moved main: it merges nothing")
	}
	if calls, _ := os.ReadFile(filepath.Join(w.state, "calls.log")); strings.Contains(string(calls), "pr merge") {
		t.Fatalf("the lane merged a PR: %s", calls)
	}
	tag := strings.TrimSpace(gitRun(t, w.origin, "tag", "--points-at", main1))
	if !strings.HasPrefix(tag, "green/") {
		t.Fatalf("main's pinned head carries a green tag, got %q", tag)
	}
	if msg := gitRun(t, w.origin, "cat-file", "-p", tag); !strings.Contains(msg, `"runner":"gate.py"`) || !strings.Contains(msg, `"commit":"`+main1+`"`) {
		t.Fatalf("the tag is the release gate's record of main's head:\n%s", msg)
	}
	if !strings.Contains(v1ReadString(w.calls), "docker exec") {
		t.Fatal("the gate ran inside the (fake) container")
	}
	for _, wi := range []string{a, b} {
		in := w.integration(t, wi)
		if in["state"] != "merged" || in["green_tag"] != tag || in["batch"] == nil {
			t.Fatalf("%s merged with the batch and the tag: %v", wi, in)
		}
	}
	if rec := w.record(t, b1); rec["result"] != "green" || len(rec["items"].([]any)) != 2 || rec["reverts"] != nil {
		t.Fatalf("the batch's record rides its wrap: %v", rec)
	}
	// the moves were the close step's registered hub writes (integrate.py
	// close --ops-out), made by the daemon on its done: on the call ledger
	if moves := w404Made(t, d.capsuleDir(b1), "close", stepcall.HubAttrs); len(moves) != 2 {
		t.Fatalf("two moves of close made on the call ledger, got %+v", moves)
	}
	x := readExecution(t, d.capsuleDir(b1))
	for _, step := range []string{"collect", "revert", "full-gate", "close"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s done: %+v", step, st)
		}
	}
	fmt.Println("PASS I1 two landed items gated on the pinned head, green tag, records merged by registered hub writes, no PR merged by the lane")

	// --- batch 2: a red gate triaged, attributed, a fix ticket filed by a hub write
	r, prR, _ := w.land("pr", "", map[string]string{"broken": "x\n", "pkg/broken.go": "package pkg\n"})
	k, _, _ := w.land("pk", "", map[string]string{"code.txt": "one-k\n"})
	main2 := strings.TrimSpace(gitRun(t, w.origin, "rev-parse", "main"))
	b2 := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b2) })
	w.inst.setPlanFull(t, b2, w404Plan(), nil)
	waitWorkflowStatus(t, d, b2, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b2, "wrapped", 60*time.Second)
	rin := w.integration(t, r)
	heads, _ := rin["red_heads"].(map[string]any)
	if rin["state"] != "landed" || fmt.Sprint(rin["red_rounds"]) != "1" || len(rin["fix_tickets"].([]any)) != 1 || heads["demo"] != main2 || rin["batch"] != b2 {
		t.Fatalf("the breaking item stays landed with one red round, its fix ticket and the gated head: %v", rin)
	}
	if in := w.integration(t, k); in["state"] != "landed" {
		t.Fatalf("the innocent item is landed (main is red): %v", in)
	}
	var triage struct {
		Details struct {
			Units []struct {
				Class, Item, Why, Unit, File, Rerun string
			} `json:"units"`
		} `json:"details"`
	}
	if raw, err := os.ReadFile(filepath.Join(d.capsuleDir(b2), "artifacts", "integrate", "triage.json")); err != nil || json.Unmarshal(raw, &triage) != nil {
		t.Fatalf("the triage result: %v", err)
	}
	if u := triage.Details.Units; len(u) != 1 || u[0].Class != "attributed" || u[0].Item != r || !strings.Contains(u[0].Why, "bisection: first red at the merge") ||
		u[0].Unit != "TestBroken" || u[0].File != "pkg/broken_test.go" || u[0].Rerun != "sh tests/check.sh TestBroken" {
		t.Fatalf("TestBroken re-run by the conf's template, red, attributed to the breaking item by the bisection: %+v", u)
	}
	fixRef := rin["fix_tickets"].([]any)[0].(string)
	ticket, err := w.inst.client.GetWorkItem(fixRef)
	if err != nil || ticket.Priority != "high" || ticket.BundleName == nil || *ticket.BundleName != "develop" || ticket.Status != api.StatusQueued ||
		ticket.Content == nil || !strings.Contains(*ticket.Content, prR) || !strings.Contains(*ticket.Content, "the tree carries broken") ||
		!strings.Contains(*ticket.Content, "integration-ticket-key: "+b2+"/fix/"+r+"/0") {
		t.Fatalf("the fix ticket: develop, high, not submitted, linking the source PR, carrying the excerpt and its stable key: %+v %v", ticket, err)
	}
	// AC3: the acceptance names the red unit passing on the fixed head
	if c := *ticket.Content; !strings.Contains(c, "## Acceptance\n- `TestBroken` passes on the fixed head") {
		t.Fatalf("the fix ticket's acceptance names the red check: %s", c)
	}
	if tags := strings.TrimSpace(gitRun(t, w.origin, "tag", "--points-at", main2)); tags != "" {
		t.Fatalf("a red head is never tagged: %q", tags)
	}
	rec := w.record(t, b2)
	if rec["result"] != "red" || rec["main"].(map[string]any)["demo"] != main2 || fmt.Sprint(rec["fix_tickets"]) != "["+fixRef+"]" {
		t.Fatalf("the red batch's record names the head it judged and the ticket it filed: %v", rec)
	}
	// the ticket and the attribution were full-gate's registered hub
	// writes (full-gate-ops.json), made by the daemon in order: the file,
	// then the move that names it by key
	files, moves := w404Made(t, d.capsuleDir(b2), "full-gate", stepcall.HubFile), w404Made(t, d.capsuleDir(b2), "full-gate", stepcall.HubAttrs)
	if len(files) != 1 || files[0].Ref != fixRef {
		t.Fatalf("the fix ticket's file write made on the call ledger, filing the ticket the item names: %+v", files)
	}
	if len(moves) != 1 || moves[0].Seq < files[0].Seq {
		t.Fatalf("the attribution's move made after the ticket: %+v", moves)
	}
	fmt.Println("PASS I2 red gate triaged and attributed, fix ticket filed by a hub write with its acceptance, the round recorded")

	// the pool after the red batch: a red batch moves nothing, both items
	// stay landed at aihub
	landed, err := w.inst.client.ListWorkItems(aihubclient.WorkItemFilter{Project: v1Project, Status: api.StatusWrapped, Attrs: []string{"integration.state:landed"}, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 2 {
		t.Fatalf("the pool holds the two landed items: %+v", landed)
	}
	fmt.Println("PASS I3 the two items still landed at aihub after the red batch")

	// --- batch 3: main has not moved since the red gate (read from batch
	// 2's wrap at aihub) — nothing is gated, no gate runs
	execs := strings.Count(v1ReadString(w.calls), "docker exec")
	b3 := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b3) })
	w.inst.setPlanFull(t, b3, w404Plan(), nil)
	waitWorkflowStatus(t, d, b3, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b3, "wrapped", 60*time.Second)
	if rec := w.record(t, b3); rec["result"] != "empty" || !strings.Contains(fmt.Sprint(rec["empty"]), "the head the last red gate judged") {
		t.Fatalf("the batch is empty: %v", rec)
	}
	if n := strings.Count(v1ReadString(w.calls), "docker exec"); n != execs {
		t.Fatalf("no gate ran: %d -> %d docker execs", execs, n)
	}
	fmt.Println("PASS I4 main unmoved since the red gate: the batch gates nothing")

	// --- batch 4a: the fix ticket canceled, the hold is stuck — collect
	// asks the owner; the owner's words say fix: a new ticket, registered
	// on the held item by a move (collect-ops.json; main unmoved: empty)
	if _, err := w.inst.client.CancelWorkItem(fixRef); err != nil {
		t.Fatalf("cancel the fix ticket: %v", err)
	}
	b4a := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b4a) })
	w.inst.setPlanFull(t, b4a, []map[string]any{
		{"do": "envelope", "id": "collect", "end_turn": true, "run": []string{"w404-step collect --ask"},
			"on_message": w404Action("collect", "--fix", r)},
		w404Action("revert"), w404Action("full-gate"), w404Action("close"),
	}, nil)
	waitUntil(t, 180*time.Second, "collect's ask on the stuck hold", func() bool { return len(v26cAwaiting(t, d.dataDir, b4a)) == 1 })
	if out := d.pf(t, "answer", b4a, "--text", "fix "+r+" — file another ticket", "--by", "usr_root"); !strings.Contains(out, "delivered to the step's agent") {
		t.Fatalf("pf answer: %s", out)
	}
	waitWorkflowStatus(t, d, b4a, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b4a, "wrapped", 60*time.Second)
	rin = w.integration(t, r)
	fixes, _ := rin["fix_tickets"].([]any)
	if rin["state"] != "landed" || len(fixes) != 2 || fixes[0] != fixRef {
		t.Fatalf("the new fix ticket is registered on the held item beside the first: %v", rin)
	}
	newFix := fixes[1].(string)
	nt, err := w.inst.client.GetWorkItem(newFix)
	if err != nil || nt.Status != api.StatusQueued || nt.Priority != "high" || nt.BundleName == nil || *nt.BundleName != "develop" || nt.Content == nil ||
		!strings.Contains(*nt.Content, "integration-ticket-key: hold-fix/"+r+"/1") || !strings.Contains(*nt.Content, "## Acceptance\n- the full gate is green") {
		t.Fatalf("the hold's fix ticket: develop, high, queued, its acceptance and its stable key: %+v %v", nt, err)
	}
	if files := w404Made(t, d.capsuleDir(b4a), "collect", stepcall.HubFile); len(files) != 1 || files[0].Ref != newFix {
		t.Fatalf("collect's file write made on the call ledger, filing the ticket the hold names: %+v", files)
	}
	if rec := w.record(t, b4a); rec["result"] != "empty" || rec["hold_rulings"].(map[string]any)[r].(map[string]any)["ruling"] != "fix" ||
		rec["hold_rulings"].(map[string]any)[r].(map[string]any)["words"] != "fix "+r+" — file another ticket" {
		t.Fatalf("the record carries the fix ruling, the owner's words verbatim: %v", rec)
	}
	fmt.Println("PASS I5a a stuck hold's fix ruling files a ticket and registers it on the held item")

	// --- batch 4b: the registered ticket is open, so the hold is not stuck:
	// nobody is asked (review of change 13, blocker 2: an unregistered ticket
	// left the hold stuck and every later batch asked again)
	b4b := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b4b) })
	w.inst.setPlanFull(t, b4b, w404Plan(), nil)
	waitWorkflowStatus(t, d, b4b, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b4b, "wrapped", 60*time.Second)
	var bb struct {
		Details struct {
			Holds []struct {
				WI     string `json:"wi"`
				Stuck  bool   `json:"stuck"`
				Reason string `json:"reason"`
			} `json:"holds"`
		} `json:"details"`
	}
	if raw, err := os.ReadFile(filepath.Join(d.capsuleDir(b4b), "artifacts", "integrate", "batch.json")); err != nil || json.Unmarshal(raw, &bb) != nil {
		t.Fatalf("batch 4b's collect result: %v", err)
	}
	if h := bb.Details.Holds; len(h) != 1 || h[0].WI != r || h[0].Stuck || !strings.Contains(h[0].Reason, newFix) {
		t.Fatalf("the next batch finds the hold open on its registered ticket: %+v", h)
	}
	if n := len(v26cAwaiting(t, d.dataDir, b4b)); n != 0 {
		t.Fatalf("nobody is asked about an open hold: %d asks", n)
	}
	fmt.Println("PASS I5b the next batch finds the hold open on the registered ticket: nobody is asked")

	// --- batch 4: the new ticket canceled too, the hold is stuck again —
	// collect asks the owner; the owner's words say revert
	if _, err := w.inst.client.CancelWorkItem(newFix); err != nil {
		t.Fatalf("cancel the new fix ticket: %v", err)
	}
	b4 := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b4) })
	w.inst.setPlanFull(t, b4, []map[string]any{
		{"do": "envelope", "id": "collect", "end_turn": true, "run": []string{"w404-step collect --ask"},
			"on_message": w404Action("collect", "--revert", r)},
		w404Action("revert"), w404Action("full-gate"), w404Action("close"),
	}, nil)
	waitUntil(t, 180*time.Second, "collect's ask (awaiting_owner)", func() bool { return len(v26cAwaiting(t, d.dataDir, b4)) == 1 })
	ask := v26cBody(v26cAwaiting(t, d.dataDir, b4)[0])
	if text, _ := ask["question"].(string); !strings.Contains(text, r) || !strings.Contains(text, "revert / clear / fix") {
		t.Fatalf("the ask names the stuck hold and the ways out: %v", ask)
	}
	if out := d.pf(t, "answer", b4, "--text", "revert "+r+" — its fix went nowhere", "--by", "usr_root"); !strings.Contains(out, "delivered to the step's agent") {
		t.Fatalf("pf answer: %s", out)
	}
	waitWorkflowStatus(t, d, b4, "completed", 300*time.Second)
	w.inst.waitWIStatus(t, b4, "wrapped", 60*time.Second)
	main4 := strings.TrimSpace(gitRun(t, w.origin, "rev-parse", "main"))
	if main4 == main2 {
		t.Fatal("the revert moved main")
	}
	if ls := gitRun(t, w.origin, "ls-tree", "--name-only", "main"); strings.Contains(ls, "broken") {
		t.Fatalf("main no longer carries the reverted change: %s", ls)
	}
	if calls, _ := os.ReadFile(filepath.Join(w.state, "calls.log")); !strings.Contains(string(calls), "pr merge") {
		t.Fatalf("the revert step merged the revert PR through gh: %s", calls)
	}
	rin = w.integration(t, r)
	if rin["state"] != "reverted" || rin["batch"] != b4 || len(rin["revert_prs"].([]any)) != 1 {
		t.Fatalf("the reverted item's record (the revert step's registered move): %v", rin)
	}
	if moves := w404Made(t, d.capsuleDir(b4), "revert", stepcall.HubAttrs); len(moves) != 1 {
		t.Fatalf("the revert step's move (revert.py --ops-out) made on the call ledger: %+v", moves)
	}
	if tag4 := strings.TrimSpace(gitRun(t, w.origin, "tag", "--points-at", main4)); !strings.HasPrefix(tag4, "green/") {
		t.Fatalf("the revert's merge gated green and tagged: %q", tag4)
	}
	if in := w.integration(t, k); in["state"] != "merged" || in["batch"] != b4 {
		t.Fatalf("the innocent item merged with the batch that reverted the red one: %v", in)
	}
	rec = w.record(t, b4)
	rulings, _ := rec["hold_rulings"].(map[string]any)
	reverts, _ := rec["reverts"].([]any)
	if rec["result"] != "green" || rulings[r] == nil || rulings[r].(map[string]any)["ruling"] != "revert" || rulings[r].(map[string]any)["words"] != "revert "+r+" — its fix went nowhere" ||
		len(reverts) != 1 || reverts[0].(map[string]any)["wi"] != r {
		t.Fatalf("the record carries the ruling (the owner's words verbatim) and the revert: %v", rec)
	}
	x = readExecution(t, d.capsuleDir(b4))
	if st := x.Steps["collect"]; st == nil || st.Status != "done" || len(st.Attempts) != 1 {
		t.Fatalf("collect answered the owner in its one dispatch: %+v", st)
	}
	fmt.Println("PASS I5 a stuck hold asked, the owner's words reverted it under the merge lock, the revert gated green")

	// --- batch 5: a red gate the owner rules reverted — the gate's decision
	// file names the item, its FAIL (citing the owner's reply) rewinds to
	// the revert step, the gate runs again on the revert
	r2, _, _ := w.land("pr2", "", map[string]string{"broken": "y\n"})
	b5 := w.startBatch(t, d)
	t.Cleanup(func() { killOrphans(b5) })
	w.inst.setPlanFull(t, b5, []map[string]any{
		w404Action("collect"), w404Action("revert"),
		// full-gate puts the red triage to the owner and ends its turn (it
		// holds heavy admission: no warm window, the reply resumes it)
		{"do": "envelope", "id": "full-gate", "end_turn": true, "run": []string{"w404-step full-gate --ask"}},
		w404Action("full-gate", "--decide", "revert:"+r2),
		w404Action("revert"), w404Action("full-gate"), w404Action("close"),
	}, nil)
	waitUntil(t, 180*time.Second, "full-gate's ask on the red gate", func() bool { return len(v26cAwaiting(t, d.dataDir, b5)) == 1 })
	if q, _ := v26cBody(v26cAwaiting(t, d.dataDir, b5)[0])["question"].(string); !strings.Contains(q, "attributed, "+r2) {
		t.Fatalf("the ask names the red unit and its source: %q", q)
	}
	// full-gate holds heavy admission: its process ended at the ask, the
	// reply resumes the session (a new dispatch carrying the words)
	if out := d.pf(t, "answer", b5, "--text", "revert "+r2, "--by", "usr_root"); !strings.Contains(out, "recorded for step full-gate") || !strings.Contains(out, "resumes the session") {
		t.Fatalf("pf answer: %s", out)
	}
	waitWorkflowStatus(t, d, b5, "completed", 400*time.Second)
	w.inst.waitWIStatus(t, b5, "wrapped", 60*time.Second)
	capB5 := d.capsuleDir(b5)
	x = readExecution(t, capB5)
	if st := x.Steps["revert"]; st == nil || st.Status != "done" {
		t.Fatalf("the revert step done: %+v", st)
	}
	// the gate handed off fail citing the owner's reply (its round's
	// message), the reason naming the item; after the revert, done
	reply := w404OwnerReply(t, capB5, "full-gate")
	if hs := w404Handoffs(t, capB5, "full-gate"); len(hs) != 2 || hs[0].Status != stepcall.StatusFail || hs[0].PerOwner != reply.At ||
		!strings.Contains(hs[0].Reason, r2) || hs[1].Status != stepcall.StatusDone {
		t.Fatalf("full-gate's hand-offs: the owner's revert (fail --per-owner the reply), then done: %+v (the reply %+v)", hs, reply)
	}
	// the revert step's second round read the item from the gate's
	// decision file and reverted it (its result file and its move)
	if hs := w404Handoffs(t, capB5, "revert"); len(hs) != 2 || hs[0].Status != stepcall.StatusDone || hs[1].Status != stepcall.StatusDone {
		t.Fatalf("the revert step's two rounds handed off done: %+v", hs)
	}
	var rv struct {
		Details struct {
			Reverts []struct {
				WI   string `json:"wi"`
				Done bool   `json:"done"`
			} `json:"reverts"`
		} `json:"details"`
	}
	w404Result(t, capB5, "revert.json", &rv)
	if e := rv.Details.Reverts; len(e) != 1 || e[0].WI != r2 || !e[0].Done {
		t.Fatalf("the revert step's second round (the gate's decision as its input) reverted the item: %+v", e)
	}
	if moves := w404Made(t, capB5, "revert", stepcall.HubAttrs); len(moves) != 1 || moves[0].Round != 2 {
		t.Fatalf("the revert's move made in the revert step's second round: %+v", moves)
	}
	// the FAIL cites the owner's message in the gate's round: the rewind is
	// the owner's, charged to no budget
	rws, _ := x.RewindStats["rewinds"].([]any)
	if len(rws) != 1 {
		t.Fatalf("one rewind, the gate's FAIL to revert: %v", x.RewindStats)
	}
	if rw := rws[0].(map[string]any); rw["gate"] != "full-gate" || rw["to_step"] != "revert" || rw["class"] != "owner" {
		t.Fatalf("the owner-routed rewind (charged to nothing): %v", rw)
	}
	if st := x.Steps["full-gate"]; st == nil || st.Status != "done" {
		t.Fatalf("full-gate done after the revert: %+v", st)
	}
	if rin := w.integration(t, r2); rin["state"] != "reverted" || rin["batch"] != b5 {
		t.Fatalf("the item the owner reverted on the red gate: %v", rin)
	}
	main5 := strings.TrimSpace(gitRun(t, w.origin, "rev-parse", "main"))
	if ls := gitRun(t, w.origin, "ls-tree", "--name-only", "main"); strings.Contains(ls, "broken") {
		t.Fatalf("main no longer carries the change: %s", ls)
	}
	if tag5 := strings.TrimSpace(gitRun(t, w.origin, "tag", "--points-at", main5)); !strings.HasPrefix(tag5, "green/") {
		t.Fatalf("the revert's merge gated green in the same batch: %q", tag5)
	}
	rec = w.record(t, b5)
	if rec["result"] != "green" || len(rec["reverts"].([]any)) != 1 {
		t.Fatalf("the batch closed green with its revert on record: %v", rec)
	}
	fmt.Println("PASS I6 the owner's revert on a red gate: the owner-routed FAIL rewinds to revert, the revert lands and the gate runs again")
}
