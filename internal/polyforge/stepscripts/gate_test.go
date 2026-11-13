package stepscripts

// gate_test.go: the full test gate — the workflow script
// workflows/_shared/scripts/gate.py (; pf-gate's successor),
// run as the integrate lane's full-gate skill runs it, `python3 gate.py
// --isolation container --capsule … --pins …` / `--triage` (invoke runs it
// by NameGate: scripts_test.go, its "containers" the fake docker's, run
// here) — on the batch fixture (integrate_test.go): the gate on the pinned
// head, a red gate triaged — a flaky unit re-runs green and the gate runs
// once more; a real red is bisected to the merge that broke it — and the
// invocations the script refuses. Its library half is
// workflows/_shared/scripts/tests (test_gate_*.py); the clone mode, the
// release's, is test/w405_*.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// NameGate is the gate script, as its result files name it.
const NameGate = "gate.py"

// gateIsolation is the gate's isolation as every caller writes it (invoke
// puts it first on a gate run's arguments).
var gateIsolation = []string{"--isolation", "container"}

// gateResult is what these tests read of one repo's gate run.
type gateResult struct {
	Commit     string `json:"commit,omitempty"`
	Incomplete string `json:"incomplete,omitempty"`
	Passed     bool   `json:"passed"`
}

// gateRepoDetail is one repo's gate in a gate result's details.
type gateRepoDetail struct {
	Head     string      `json:"head"`
	Branch   string      `json:"branch,omitempty"`
	Result   *gateResult `json:"result"`
	Tag      string      `json:"tag,omitempty"`
	TagError string      `json:"tag_error,omitempty"`
}

// gateDetails is a gate result's details.
type gateDetails struct {
	Mode       string                     `json:"mode"`
	Pins       string                     `json:"pins,omitempty"`
	Gated      []string                   `json:"gated"`
	Repos      map[string]*gateRepoDetail `json:"repos"`
	Tags       map[string]string          `json:"tags,omitempty"`
	Failed     []string                   `json:"failed,omitempty"`
	Incomplete map[string]string          `json:"incomplete,omitempty"`
	LogDir     string                     `json:"log_dir,omitempty"`
}

// triageUnitDetail is one triaged unit of a triage's details.
type triageUnitDetail struct {
	Repo      string `json:"repo"`
	Command   string `json:"command"`
	Unit      string `json:"unit"`
	Kind      string `json:"kind"`
	File      string `json:"file,omitempty"`
	Class     string `json:"class"`
	Item      string `json:"item,omitempty"`
	Root      string `json:"root,omitempty"`
	Why       string `json:"why,omitempty"`
	Base      string `json:"base,omitempty"`
	Outside   string `json:"outside,omitempty"`
	Rerun     string `json:"rerun,omitempty"`
	Log       string `json:"log,omitempty"`
	Signature string `json:"signature,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
}

// triageDetails is a triage's details (gate.py --triage).
type triageDetails struct {
	Pins      string             `json:"pins"`
	Gate      string             `json:"gate"`
	Repos     []string           `json:"repos"`
	Baselines map[string]string  `json:"baselines"`
	Units     []triageUnitDetail `json:"units"`
	Classes   map[string]int     `json:"classes"`
	AllFlaky  bool               `json:"all_flaky"`
	Regate    *gateDetails       `json:"regate,omitempty"`
	LogDir    string             `json:"log_dir,omitempty"`
	GateRun   string             `json:"gate_run"`
}

// gateRunID answers a gate run's identity: the sha256 of its result file's
// bytes, hex.
func gateRunID(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// sortedStrings answers m's keys, sorted.
func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gate runs `gate.py --capsule --pins <batch.json>` with extra flags.
func (f *batchFixture) gate(extra ...string) (int, *Result, string) {
	f.t.Helper()
	args := append([]string{"--capsule", f.dir, "--pins", f.art("batch.json"), "--json", "--out", f.art("gate.json")}, extra...)
	exit, _, errb := invoke(f.t, NameGate, args, batchEnv(), newFakeRuntime(f.t))
	if _, err := os.Stat(f.art("gate.json")); err != nil {
		return exit, nil, errb
	}
	return exit, readResult(f.t, f.art("gate.json")), errb
}

// triageArgs are `gate.py --triage`'s arguments over the fixture's batch
// and gate (pool "" = the pool read through the step's hub).
func (f *batchFixture) triageArgs(pool string, extra ...string) []string {
	args := []string{"--triage", "--capsule", f.dir, "--pins", f.art("batch.json"), "--gate", f.art("gate.json"), "--batch", f.art("batch.json")}
	if pool != "" {
		args = append(args, "--pool", pool)
	}
	return append(append(args, "--json", "--out", f.art("triage.json")), extra...)
}

// triage runs `gate.py --triage` over the fixture's batch, gate and pool.
func (f *batchFixture) triage(pool string, extra ...string) (int, *Result, string) {
	f.t.Helper()
	exit, _, errb := invoke(f.t, NameGate, f.triageArgs(pool, extra...), batchEnv(), newFakeRuntime(f.t))
	if _, err := os.Stat(f.art("triage.json")); err != nil {
		return exit, nil, errb
	}
	return exit, readResult(f.t, f.art("triage.json")), errb
}

// TestGateTriageAttributesTheRedToItsMerge: two items landed after the
// green tag, the second breaking TestBroken; the gate is red, the triage
// re-runs the unit red at the head, green at the baseline (the green
// tag), and — the scope engine selecting everything for this stack=none
// tree, so both changes are candidates — bisects the default branch to
// the second item's merge: attributed to it.
func TestGateTriageAttributesTheRedToItsMerge(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	mB := f.land("wi_b", "p#2", map[string]string{"broken": "x\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	exit, res, errb := f.gate()
	if exit != 1 || res == nil || res.Verdict != VerdictFail || res.Command != NameGate {
		t.Fatalf("the gate: exit %d %+v\n%s", exit, res, errb)
	}
	var gd gateDetails
	detailsOf(t, res, &gd)
	if strings.Join(gd.Failed, ",") != "app" || gd.Repos["app"].Result.Passed || gd.Repos["app"].Result.Commit != head || !strings.Contains(res.Summary, "the gate failed in app: 1 of 1 commands failed") || len(res.Logs) != 1 {
		t.Fatalf("gate details: %+v summary %q logs %v", gd, res.Summary, res.Logs)
	}
	exit, res, errb = f.triage(pool)
	if exit != 1 || res == nil || res.Verdict != VerdictFail {
		t.Fatalf("the triage: exit %d %+v\n%s", exit, res, errb)
	}
	var td triageDetails
	detailsOf(t, res, &td)
	if strings.Join(td.Repos, ",") != "app" || td.Baselines["app"] != "green/20261101-1" || td.AllFlaky || td.Regate != nil || td.Classes["attributed"] != 1 || len(td.Units) != 1 {
		t.Fatalf("triage details: %+v", td)
	}
	u := td.Units[0]
	if u.Repo != "app" || u.Unit != "TestBroken" || u.File != "pkg/broken_test.go" || u.Rerun != "sh tests/check.sh TestBroken" || u.Class != "attributed" || u.Item != "wi_b" || u.Root != "wi_b" ||
		!strings.Contains(u.Why, "bisection: first red at the merge "+mB[:12]+" of p#2") || !strings.Contains(u.Base, "green/20261101-1") || u.Log == "" || !strings.Contains(u.Excerpt, "the tree carries broken") {
		t.Fatalf("unit: %+v", u)
	}
	if res.Heads["app"] != head || !strings.Contains(res.Summary, "the red stands: 1 unit(s) in app (1 attributed)") {
		t.Fatalf("heads %v summary %q", res.Heads, res.Summary)
	}
	// the clone is left at the red head
	if got := gitT(t, f.repo(), "rev-parse", "HEAD"); got != head {
		t.Fatalf("the clone is at %s, not the red head %s", got, head)
	}
}

// TestGateTriageFlakyRunsTheGateAgain: the one failure re-runs green at
// the same head (flaky), so the triage runs the full gate once more and,
// with --mark-green, tags the pass.
func TestGateTriageFlakyRunsTheGateAgain(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_f", "p#5", map[string]string{"flaky": "x\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	if exit, res, errb := f.gate(); exit != 1 || res == nil {
		t.Fatalf("the gate: exit %d\n%s", exit, errb)
	}
	exit, res, errb := f.triage(pool, "--mark-green", "--summary", "batch wi_batch")
	if exit != 0 || res == nil || res.Verdict != VerdictPass {
		t.Fatalf("the triage: exit %d %+v\n%s", exit, res, errb)
	}
	var td triageDetails
	detailsOf(t, res, &td)
	if !td.AllFlaky || td.Classes["flaky"] != 1 || len(td.Units) != 1 || td.Units[0].Unit != "TestFlaky" || td.Units[0].Class != "flaky" || td.Regate == nil {
		t.Fatalf("triage details: %+v", td)
	}
	// the triage names the gate run it triaged: its result file's digest
	if raw, err := os.ReadFile(f.art("gate.json")); err != nil || td.GateRun != gateRunID(raw) || len(td.GateRun) != 64 {
		t.Fatalf("the triage names the gate run (gate_run %q): %v", td.GateRun, err)
	}
	rg := td.Regate
	if rg.Mode != "capsule" || !rg.Repos["app"].Result.Passed || rg.Repos["app"].Head != head || !strings.HasPrefix(rg.Tags["app"], "green/") {
		t.Fatalf("regate: %+v", rg)
	}
	if got := gitT(t, f.seed, "ls-remote", "--tags", "origin", "refs/tags/"+rg.Tags["app"]); !strings.Contains(got, rg.Tags["app"]) {
		t.Fatalf("the tag is not on origin: %q", got)
	}
	if !strings.Contains(res.Summary, "every failure re-ran green (1 flaky); the full gate ran once more: the gate passed in app; green tags app green/") {
		t.Fatalf("summary: %q", res.Summary)
	}
}

func TestGateCapsuleNothingGatedAndBarePins(t *testing.T) {
	f := newBatchFixture(t)
	head := f.originHead()
	writeFile(t, f.dir, "pins.json", `{"heads": {"app": "`+head+`"}, "gated": []}`)
	exit, _, errb := invoke(t, NameGate, []string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "pins.json"), "--out", f.art("gate.json")}, batchEnv(), newFakeRuntime(t))
	res := readResult(t, f.art("gate.json"))
	if exit != 0 || res.Verdict != VerdictPass || !strings.Contains(res.Summary, "nothing gated") || len(res.Heads) != 0 {
		t.Fatalf("exit %d %+v\n%s", exit, res, errb)
	}
	var gd gateDetails
	detailsOf(t, res, &gd)
	if gd.Mode != "capsule" || len(gd.Gated) != 0 || len(gd.Repos) != 0 {
		t.Fatalf("details: %+v", gd)
	}
	// bare pins gating app at its head: the gate runs and passes; its
	// container is the gate's own, labelled with the step's work item
	writeFile(t, f.dir, "pins.json", `{"heads": {"app": "`+head+`"}, "gated": ["app"]}`)
	rt := newFakeRuntime(t)
	exit, stdout, errb := invoke(t, NameGate, []string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "pins.json"), "--out", f.art("gate.json")},
		batchEnv(), rt)
	res = readResult(t, f.art("gate.json"))
	if exit != 0 || res.Verdict != VerdictPass || res.Heads["app"] != head || !strings.Contains(stdout, "gate: the full gate on app main") || !strings.Contains(stdout, NameGate+": the gate passed in app (pass)") {
		t.Fatalf("exit %d %+v\n%s\n%s", exit, res, stdout, errb)
	}
	if len(rt.starts) != 1 || rt.starts[0].Labels[LabelDaemon] != "pf-gate" || len(rt.starts[0].Labels[LabelBoot]) != 16 ||
		rt.starts[0].Labels[LabelWI] != "wi_batch" || rt.starts[0].Labels[LabelExpires] == "" {
		t.Fatalf("the container's labels: %+v", rt.starts)
	}
	if _, err := os.Stat(f.art("logs/.gitignore")); err != nil {
		t.Fatalf("the logs beside --out are not kept out of the chain: %v", err)
	}
	// outside a step (no work item) the container is labelled the gate's:
	// the release's and an operator's
	rt = newFakeRuntime(t)
	if exit, _, errb := invoke(t, NameGate, []string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "pins.json"), "--out", f.art("gate.json")}, stepEnv(), rt); exit != 0 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	if len(rt.starts) != 1 || rt.starts[0].Labels[LabelDaemon] != "pf-gate" || rt.starts[0].Labels[LabelWI] != "gate" {
		t.Fatalf("the container's labels outside a step: %+v", rt.starts)
	}
}

func TestGateRefusesUnusableInvocations(t *testing.T) {
	f := newBatchFixture(t)
	writeFile(t, f.dir, "pins.json", `{"heads": {"app": "x"}, "gated": ["lib"]}`)
	writeFile(t, f.dir, "nogated.json", `{"heads": {"app": "x"}}`)
	// a result file's pins are its own heads (what revert.py reads of
	// collect's result) and its details' gated: heads under details alone
	// name nothing
	writeFile(t, f.dir, "detailheads.json", `{"schema": "pf-step-result/1", "command": "revert.py", "heads": {}, "details": {"heads": {"app": "x"}, "gated": ["app"]}}`)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--pins", "p"}, "need --capsule"},
		{[]string{"--triage"}, "need --capsule"},
		{[]string{"--capsule", f.dir}, "--capsule needs --pins"},
		{[]string{"--capsule", f.dir, "--pins", "p", "--repo", "x"}, "--repo names a lone clone"},
		{[]string{"--capsule", f.dir, "--pins", "p", "--triage"}, "--triage needs --gate"},
		{[]string{"--capsule", f.dir, "--pins", "p", "--gate", "g"}, "need --triage"},
		{[]string{"--plan-only", "--json"}, "--plan-only prints the plan alone"},
		{[]string{"--plan-only", "--capsule", f.dir, "--pins", "p"}, "--plan-only prints the plan alone"},
		{[]string{"--mark-green"}, "--mark-green needs --summary"},
		{[]string{"--summary", "s"}, "needs --mark-green"},
		{[]string{"--remote", "r"}, "needs --mark-green"},
		{[]string{"--capsule", t.TempDir(), "--pins", "p"}, "no repos/ directory"},
		{[]string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "none.json")}, "--pins"},
		{[]string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "pins.json")}, "the gated repo lib has no head"},
		{[]string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "nogated.json")}, "no `gated` list"},
		{[]string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "detailheads.json")}, "the gated repo app has no head"},
		{[]string{"x"}, "unrecognized arguments"},
	} {
		exit, _, errb := invoke(t, NameGate, tc.args, batchEnv(), newFakeRuntime(t))
		if exit != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want %q)", tc.args, exit, errb, tc.want)
		}
	}
	// the isolation is the container, never the host
	if exit, _, errb := runScript(t, NameGate, []string{"--capsule", f.dir, "--pins", "p"}, batchEnv()); exit != 2 || !strings.Contains(errb, "--isolation container is required") {
		t.Errorf("no --isolation: exit %d %q", exit, errb)
	}
	if exit, _, errb := runScript(t, NameGate, []string{"--isolation", "host", "--capsule", f.dir, "--pins", "p"}, batchEnv()); exit != 2 || !strings.Contains(errb, "never on the host") {
		t.Errorf("--isolation host: exit %d %q", exit, errb)
	}
	// a triage of a gate result that judges another head than the pins
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	f.writeGate("0000000000000000000000000000000000000000", false, "")
	if exit, _, errb := f.triage(pool); exit != 2 || !strings.Contains(errb, "triage the gate of the pinned heads") {
		t.Fatalf("a stale gate result: exit %d %q", exit, errb)
	}
	// a triage of a gate that is not red has nothing to do
	f.writeGate(f.originHead(), true, "green/20261110-1")
	if exit, res, _ := f.triage(pool); exit != 2 || res == nil || !strings.Contains(res.Summary, "nothing to triage") {
		t.Fatalf("a green gate: exit %d %+v", exit, res)
	}
}

// TestGateCapsuleWithoutTheDockerfileIsNotJudged: a gated repo whose
// pinned head carries no .polyforge/Dockerfile.verify is not judged (exit
// 2, the reason in the summary): the gate runs only in the container,
// never on the host.
func TestGateCapsuleWithoutTheDockerfileIsNotJudged(t *testing.T) {
	f := newBatchFixture(t)
	gitT(t, f.seed, "rm", "-q", ".polyforge/Dockerfile.verify")
	gitT(t, f.seed, "commit", "-q", "-m", "no Dockerfile")
	gitT(t, f.seed, "push", "-q", "origin", "main")
	head := f.originHead()
	writeFile(t, f.dir, "pins.json", `{"heads": {"app": "`+head+`"}, "gated": ["app"]}`)
	rt := newFakeRuntime(t)
	exit, _, errb := invoke(t, NameGate, []string{"--capsule", f.dir, "--pins", filepath.Join(f.dir, "pins.json"), "--out", f.art("gate.json")}, batchEnv(), rt)
	res := readResult(t, f.art("gate.json"))
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "carries no .polyforge/Dockerfile.verify") || !strings.Contains(res.Summary, "never on the host") {
		t.Fatalf("exit %d %+v\n%s", exit, res, errb)
	}
	if len(rt.starts) != 0 {
		t.Fatalf("a container started: %+v", rt.starts)
	}
}

// TestIntegrateReadsTheGateScriptsResultsAndTags ('s
// acceptance: integrate.py reads gate.py's result files and green tags):
// on real gate.py output — a red gate whose one failure is flaky, its
// triage with --mark-green running the gate once more and tagging the
// head — integrate.py close takes the triage's regate as the gate's
// result (it names this gate run: gate_run, round and heads) and closes
// green with the tag gate.py wrote, the landed item moving to merged under
// it; the next collect reads that tag as the repo's newest green tag and
// finds nothing new to gate.
func TestIntegrateReadsTheGateScriptsResultsAndTags(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_f", "p#5", map[string]string{"flaky": "x\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	if exit, res, errb := f.gate(); exit != 1 || res == nil || res.Command != NameGate {
		t.Fatalf("the gate: exit %d %+v\n%s", exit, res, errb)
	}
	exit, tres, errb := f.triage(pool, "--mark-green", "--summary", "batch wi_batch")
	if exit != 0 || tres == nil || tres.Command != NameGate {
		t.Fatalf("the triage: exit %d %+v\n%s", exit, tres, errb)
	}
	var td triageDetails
	detailsOf(t, tres, &td)
	tag := ""
	if td.Regate != nil {
		tag = td.Regate.Tags["app"]
	}
	if !strings.HasPrefix(tag, "green/") {
		t.Fatalf("the regate tagged nothing: %+v", td.Regate)
	}
	// the record gate.py wrote names gate.py as its runner
	gitT(t, f.seed, "fetch", "-q", "origin", "refs/tags/"+tag+":refs/tags/"+tag)
	if msg := gitT(t, f.seed, "for-each-ref", "--format=%(contents)", "refs/tags/"+tag); !strings.Contains(msg, `"runner":"gate.py"`) || !strings.Contains(msg, `"commit":"`+head+`"`) {
		t.Fatalf("the green tag's record: %s", msg)
	}
	args := []string{"close", "--pool", pool, "--batch", f.art("batch.json"), "--gate", f.art("gate.json"), "--triage", f.art("triage.json"),
		"--capsule", f.dir, "--json", "--out", f.art("close.json")}
	exit, _, errb = invoke(t, NameIntegrate, args, batchEnv(), newFakeRuntime(t))
	res := readResult(t, f.art("close.json"))
	if exit != 0 || res.Verdict != VerdictPass {
		t.Fatalf("close: exit %d %+v\n%s", exit, res, errb)
	}
	var cd closeDetails
	detailsOf(t, res, &cd)
	ib := cd.IntegrationBatch
	if ib["result"] != "green" || fmt.Sprint(ib["tags"]) != "map[app:"+tag+"]" || fmt.Sprint(ib["main"]) != "map[app:"+head+"]" {
		t.Fatalf("the record: %v", ib)
	}
	if len(cd.Moves) != 1 || cd.Moves[0].WI != "wi_f" || cd.Moves[0].Set["state"] != "merged" || cd.Moves[0].Set["green_tag"] != tag {
		t.Fatalf("moves: %+v", cd.Moves)
	}
	// the next batch: the repo stands at the tag gate.py wrote
	exit, cres, errb := f.collect(pool)
	if exit != 0 || cres == nil {
		t.Fatalf("the next collect: exit %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, cres, &d)
	if len(d.Gated) != 0 || d.Repos["app"].Green != tag || !strings.Contains(d.Repos["app"].Skip, "at its newest green tag "+tag) {
		t.Fatalf("the next collect does not read gate.py's tag: %+v %q", d.Repos["app"], d.Empty)
	}
}
