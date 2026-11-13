package stepscripts

// integrate_test.go: `integrate.py collect` and `close` (the workflow
// script that took over pf-integrate, run as a step runs it) on a batch capsule
// fixture — one task repo cloned from a bare origin whose main carries the
// gate tree (the real scope engine, the conf, the Dockerfile, the fixture
// gate script) with its base tagged green, landed items merged --no-ff into
// main — and a pool snapshot written in the pool file's shape. No real
// remote, container or aihub is touched.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// batchCheck is the fixture's gate: TestBroken is red while the tree
// carries `broken`; TestFlaky is red on its first run while the tree
// carries `flaky` (a marker beside the clone remembers the run).
const batchCheck = `#!/bin/sh
unit=${1:-}
red=0
if [ -z "$unit" ] || [ "$unit" = TestBroken ]; then
  if [ -e broken ]; then
    echo "--- FAIL: TestBroken (0.00s)"; echo "    broken_test.go:3: the tree carries broken"; red=1
  else
    echo "--- PASS: TestBroken (0.00s)"
  fi
fi
if [ -z "$unit" ] || [ "$unit" = TestFlaky ]; then
  if [ -e flaky ] && [ ! -e ../.flaky-seen ]; then
    : > ../.flaky-seen
    echo "--- FAIL: TestFlaky (0.00s)"; echo "    flaky_test.go:3: red on the first run"; red=1
  else
    echo "--- PASS: TestFlaky (0.00s)"
  fi
fi
if [ "$red" = 1 ]; then
  echo "FAIL"; echo "FAIL	example.invalid/demo/pkg	0.01s"; exit 1
fi
echo "PASS"; echo "ok  	example.invalid/demo/pkg	0.01s"
`

// batchFixture is an integration batch's capsule with one task repo `app`.
type batchFixture struct {
	t      *testing.T
	dir    string // the capsule root
	origin string // the bare origin
	seed   string // the clone that lands items on origin's main
	items  []poolItem
}

// poolSnapshot is the pool file's shape (integrate.py's snapshot_json):
// the landed items, the batches not finished, per repo the newest red
// batch's head and the registered fix tickets' states.
type poolSnapshot struct {
	Items       []poolItem            `json:"items"`
	OpenBatches []string              `json:"open_batches,omitempty"`
	RedHeads    map[string]string     `json:"red_heads"`
	Tickets     map[string]poolTicket `json:"tickets,omitempty"`
}

// poolItem is one landed item of the pool file.
type poolItem struct {
	ID          string         `json:"id"`
	Slug        string         `json:"slug,omitempty"`
	Priority    string         `json:"priority,omitempty"`
	Landed      string         `json:"landed"`
	Integration map[string]any `json:"integration"`
	Waivers     []any          `json:"waivers,omitempty"`
}

// poolTicket is one registered fix ticket's state in the pool file.
type poolTicket struct {
	Status       string              `json:"status"`
	Integration  string              `json:"integration,omitempty"`
	MergeCommits map[string][]string `json:"merge_commits,omitempty"`
}

func (f *batchFixture) repo() string { return filepath.Join(f.dir, "repos", "app") }

// newBatchFixture builds the fixture: the base commit (tagged
// green/20261101-1) on origin's main, cloned into the capsule.
func newBatchFixture(t *testing.T) *batchFixture {
	t.Helper()
	for _, bin := range []string{"git", "python3", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	f := &batchFixture{t: t, dir: filepath.Join(root, "capsule"), origin: filepath.Join(root, "app.git"), seed: filepath.Join(root, "seed")}
	gitT(t, root, "init", "-q", "--bare", "-b", "main", f.origin)
	gitT(t, root, "clone", "-q", f.origin, f.seed)
	gitT(t, f.seed, "checkout", "-q", "-B", "main")
	engine, err := os.ReadFile(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	gateID, err := os.ReadFile(filepath.Join("..", "..", "..", "tools", "gate-id"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"workflows/_shared/scripts/verify-scope": string(engine),
		"tools/gate-id":                          string(gateID),
		".polyforge/verify-scope.conf":           "stack=none\nfull_command=sh tests/check.sh\nrerun=full-1|sh tests/check.sh {unit}\n",
		".polyforge/Dockerfile.verify":           testDockerfile,
		"tests/check.sh":                         batchCheck,
		"pkg/broken_test.go":                     "package pkg\n\nfunc TestBroken(t *testing.T) {}\n",
		"pkg/flaky_test.go":                      "package pkg\n\nfunc TestFlaky(t *testing.T) {}\n",
		"README":                                 "app\n",
	} {
		writeFile(t, f.seed, rel, body)
	}
	if err := os.Chmod(filepath.Join(f.seed, "workflows", "_shared", "scripts", "verify-scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitAll(t, f.seed, "base")
	gitT(t, f.seed, "tag", "-a", "-m", "{}", "green/20261101-1")
	gitT(t, f.seed, "push", "-q", "origin", "main", "refs/tags/green/20261101-1")
	gitT(t, root, "clone", "-q", f.origin, f.repo())
	return f
}

// land merges a branch carrying files into origin's main (--no-ff) as the
// item's landing and records the item in the pool; it answers the merge.
func (f *batchFixture) land(id, slug string, files map[string]string) string {
	t := f.t
	t.Helper()
	gitT(t, f.seed, "checkout", "-q", "-b", "feat-"+id)
	for rel, body := range files {
		writeFile(t, f.seed, rel, body)
	}
	commitAll(t, f.seed, id)
	gitT(t, f.seed, "checkout", "-q", "main")
	gitT(t, f.seed, "merge", "-q", "--no-ff", "-m", "Merge "+id, "feat-"+id)
	merge := gitT(t, f.seed, "rev-parse", "HEAD")
	gitT(t, f.seed, "push", "-q", "origin", "main")
	f.items = append(f.items, poolItem{ID: id, Slug: slug, Landed: "2026-11-10T00:00:00Z", Integration: map[string]any{
		"state":         "landed",
		"repos":         []any{map[string]any{"repo": "app", "pr": "https://forge.invalid/pr/" + id, "merge_commit": merge}},
		"merge_commits": map[string]any{"app": []any{merge}},
	}})
	return merge
}

// tagGreen tags commit green on origin (an earlier batch's tag).
func (f *batchFixture) tagGreen(commit, name string) {
	gitT(f.t, f.seed, "tag", "-a", "-m", "{}", name, commit)
	gitT(f.t, f.seed, "push", "-q", "origin", "refs/tags/"+name)
}

// originHead answers origin's main head.
func (f *batchFixture) originHead() string { return gitT(f.t, f.seed, "rev-parse", "origin/main") }

// pool writes the pool snapshot (the landed items, modified by mod) and
// answers its path.
func (f *batchFixture) pool(mod func(*poolSnapshot)) string {
	t := f.t
	t.Helper()
	snap := &poolSnapshot{RedHeads: map[string]string{}}
	for _, it := range f.items {
		cp := it
		raw, _ := json.Marshal(it.Integration)
		_ = json.Unmarshal(raw, &cp.Integration)
		snap.Items = append(snap.Items, cp)
	}
	if mod != nil {
		mod(snap)
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.dir, "artifacts/integrate/pool.json", string(raw))
	return filepath.Join(f.dir, "artifacts", "integrate", "pool.json")
}

func (f *batchFixture) art(rel string) string {
	return filepath.Join(f.dir, "artifacts", "integrate", rel)
}

// batchEnv is the integrate lane's step environment.
func batchEnv() []string { return stepEnv("PF_WI_ID=wi_batch", "PF_ROUND=r1") }

// collect runs `integrate.py collect` and answers the exit code and the
// result.
func (f *batchFixture) collect(pool string) (int, *Result, string) {
	f.t.Helper()
	exit, _, errb := invoke(f.t, NameIntegrate, []string{"collect", "--pool", pool, "--capsule", f.dir, "--json", "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(f.t))
	if _, err := os.Stat(f.art("batch.json")); err != nil {
		return exit, nil, errb
	}
	return exit, readResult(f.t, f.art("batch.json")), errb
}

func TestIntegrateCollectTakesTheNewHeads(t *testing.T) {
	f := newBatchFixture(t)
	mA := f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	mB := f.land("wi_b", "p#2", map[string]string{"pkg/b.go": "package pkg\n"})
	exit, res, errb := f.collect(f.pool(nil))
	if exit != 0 || res == nil || res.Verdict != VerdictPass || res.Round != "r1" {
		t.Fatalf("exit %d result %+v\n%s", exit, res, errb)
	}
	var d collectDetails
	detailsOf(t, res, &d)
	head := f.originHead()
	if d.Batch != "wi_batch" || d.Heads["app"] != head || res.Heads["app"] != head || d.Refused != "" || d.Empty != "" {
		t.Fatalf("details: %+v", d)
	}
	if strings.Join(d.Gated, ",") != "app" || d.Repos["app"].Branch != "main" || d.Repos["app"].Green != "green/20261101-1" || d.Repos["app"].Skip != "" {
		t.Fatalf("gated: %v repos %+v", d.Gated, d.Repos["app"])
	}
	if len(d.Items) != 2 || d.Items[0].ID != "wi_a" || d.Items[1].ID != "wi_b" || d.Items[0].MergeCommits["app"][0] != mA || d.Items[1].MergeCommits["app"][0] != mB || d.Items[0].Slug != "p#1" {
		t.Fatalf("items: %+v", d.Items)
	}
	if len(d.Skipped) != 0 || len(d.Holds) != 0 || len(d.RedHeads) != 0 {
		t.Fatalf("skipped %+v holds %+v red %v", d.Skipped, d.Holds, d.RedHeads)
	}
	if !strings.Contains(res.Summary, "2 item(s) in the batch, 1 repo(s) to gate (app)") {
		t.Fatalf("summary: %q", res.Summary)
	}
}

func TestIntegrateCollectEmptyAtTheGreenTag(t *testing.T) {
	f := newBatchFixture(t)
	exit, res, errb := f.collect(f.pool(nil))
	if exit != 0 || res == nil {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, res, &d)
	if len(d.Gated) != 0 || d.Repos["app"].Skip != "at its newest green tag green/20261101-1" || !strings.HasPrefix(d.Empty, "nothing new to gate: app: at its newest green tag") {
		t.Fatalf("details: %+v (%q)", d.Repos["app"], d.Empty)
	}
	if d.Heads["app"] != f.originHead() || len(d.Items) != 0 {
		t.Fatalf("heads %v items %v", d.Heads, d.Items)
	}
}

// TestIntegrateCollectCarriesTheRedHead: the head the last red batch's gate
// failed on (the pool's red_heads, read from that batch's record) is not
// gated again while main has not moved, and the landed items awaiting it
// are skipped with the reason — a red batch does not reopen by itself.
func TestIntegrateCollectCarriesTheRedHead(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	head := f.originHead()
	exit, res, errb := f.collect(f.pool(func(s *poolSnapshot) { s.RedHeads["app"] = head }))
	if exit != 0 || res == nil {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, res, &d)
	if len(d.Gated) != 0 || !strings.Contains(d.Repos["app"].Skip, "the head the last red gate judged") || d.RedHeads["app"] != head {
		t.Fatalf("details: %+v red %v", d.Repos["app"], d.RedHeads)
	}
	if len(d.Items) != 0 || len(d.Skipped) != 1 || d.Skipped[0].WI != "wi_a" || !strings.Contains(d.Skipped[0].Why, "the head the last red gate judged") {
		t.Fatalf("items %v skipped %+v", d.Items, d.Skipped)
	}
	// main moved: the new head is gated and the item is in the batch
	f.land("wi_b", "p#2", map[string]string{"pkg/b.go": "package pkg\n"})
	exit, res, errb = f.collect(f.pool(func(s *poolSnapshot) { s.RedHeads["app"] = head }))
	if exit != 0 || res == nil {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &d)
	if strings.Join(d.Gated, ",") != "app" || len(d.Items) != 2 {
		t.Fatalf("after main moved: gated %v items %+v", d.Gated, d.Items)
	}
}

func TestIntegrateCollectRefusedAndHolds(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	// another batch of the project is running: nothing is pinned
	exit, res, errb := f.collect(f.pool(func(s *poolSnapshot) { s.OpenBatches = []string{"wi_other", "wi_batch"} }))
	if exit != 0 || res == nil {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, res, &d)
	if !strings.Contains(d.Refused, "wi_other") || len(d.Heads) != 0 || len(d.Items) != 0 || !strings.HasPrefix(res.Summary, "refused: ") {
		t.Fatalf("refused: %+v %q", d, res.Summary)
	}
	// a held source: blamed by a red batch, its fix tickets' states in the
	// snapshot — one failed (stuck), one still in develop (not stuck)
	head := f.originHead()
	pool := f.pool(func(s *poolSnapshot) {
		s.Items[0].Integration["red_rounds"] = 1.0
		s.Items[0].Integration["fix_tickets"] = []any{"p#9"}
		s.Items[0].Integration["red_heads"] = map[string]any{"app": head}
		s.Tickets = map[string]poolTicket{"p#9": {Status: "failed"}}
	})
	exit, res, errb = f.collect(pool)
	if exit != 0 || res == nil {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &d)
	if len(d.Holds) != 1 || d.Holds[0].WI != "wi_a" || !d.Holds[0].Stuck || !strings.Contains(d.Holds[0].Reason, "p#9 failed") || d.Holds[0].RedRounds != 1 || d.Holds[0].PRs[0] != "https://forge.invalid/pr/wi_a" {
		t.Fatalf("holds: %+v", d.Holds)
	}
	if !strings.HasSuffix(res.Summary, "1 stuck hold(s)") {
		t.Fatalf("summary: %q", res.Summary)
	}
	pool = f.pool(func(s *poolSnapshot) {
		s.Items[0].Integration["red_rounds"] = 1.0
		s.Items[0].Integration["fix_tickets"] = []any{"p#9"}
		s.Tickets = map[string]poolTicket{"p#9": {Status: "running"}}
	})
	if _, res, _ = f.collect(pool); res == nil {
		t.Fatal("no result")
	}
	detailsOf(t, res, &d)
	if len(d.Holds) != 1 || d.Holds[0].Stuck || !strings.Contains(d.Holds[0].Reason, "p#9 running") {
		t.Fatalf("an open fix ticket: %+v", d.Holds)
	}
	// the hold carries what a revert of it reverts: the item's merges and
	// its landed fix tickets' (a fix landed before the blaming gate is
	// stuck, and still part of the revert group)
	mA := f.items[0].Integration["merge_commits"].(map[string]any)["app"].([]any)[0].(string)
	pool = f.pool(func(s *poolSnapshot) {
		s.Items[0].Integration["red_rounds"] = 1.0
		s.Items[0].Integration["fix_tickets"] = []any{"p#9"}
		s.Items[0].Integration["red_heads"] = map[string]any{"app": head}
		s.Tickets = map[string]poolTicket{"p#9": {Status: "wrapped", Integration: "landed", MergeCommits: map[string][]string{"app": {mA}}}}
	})
	if _, res, _ = f.collect(pool); res == nil {
		t.Fatal("no result")
	}
	detailsOf(t, res, &d)
	h := d.Holds[0]
	if !h.Stuck || fmt.Sprint(h.MergeCommits) != "map[app:["+mA+"]]" || len(h.LandedFixes) != 1 || h.LandedFixes[0].Ref != "p#9" || fmt.Sprint(h.LandedFixes[0].MergeCommits) != "map[app:["+mA+"]]" {
		t.Fatalf("the hold's merges and landed fixes: %+v", h)
	}
}

func TestIntegrateCollectRefusesUnusableInvocations(t *testing.T) {
	f := newBatchFixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{}, "collect | close"},
		{[]string{"collect"}, "the task repos"}, // no --pool: the hub's, but no clones here
		{[]string{"collect", "--pool", "x", "extra"}, "unexpected arguments"},
		{[]string{"collect", "--pool", filepath.Join(f.dir, "none.json"), "--capsule", t.TempDir()}, "the task repos"},
		{[]string{"other"}, "no subcommand"},
	} {
		exit, _, errb := invoke(t, NameIntegrate, tc.args, batchEnv(), newFakeRuntime(t))
		if exit != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want %q)", tc.args, exit, errb, tc.want)
		}
	}
	// an unreadable pool is exit 2 with the result saying so
	exit, _, _ := invoke(t, NameIntegrate, []string{"collect", "--pool", filepath.Join(f.dir, "none.json"), "--capsule", f.dir, "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(t))
	if res := readResult(t, f.art("batch.json")); exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "none.json") {
		t.Fatalf("exit %d result %+v", exit, res)
	}
}

// closeArgs are `integrate.py close`'s arguments for the fixture.
func (f *batchFixture) close(pool string, gate bool) (int, *Result, string) {
	f.t.Helper()
	args := []string{"close", "--pool", pool, "--batch", f.art("batch.json"), "--capsule", f.dir, "--json", "--out", f.art("close.json")}
	if gate {
		args = append(args, "--gate", f.art("gate.json"))
	}
	exit, _, errb := invoke(f.t, NameIntegrate, args, batchEnv(), newFakeRuntime(f.t))
	if _, err := os.Stat(f.art("close.json")); err != nil {
		return exit, nil, errb
	}
	return exit, readResult(f.t, f.art("close.json")), errb
}

// writeGate writes a gate.py result for the fixture by hand: app judged at
// head, passed or not, with tag.
func (f *batchFixture) writeGate(head string, passed bool, tag string) {
	res := &Result{Schema: Schema, Command: NameGate, Details: map[string]any{
		"mode": "capsule", "gated": []string{"app"},
		"repos": map[string]any{"app": map[string]any{"head": head, "tag": tag, "result": map[string]any{"commit": head, "passed": passed, "runner": "gate.py", "runner_version": "2"}}},
	}}
	if err := WriteResult(f.art("gate.json"), res); err != nil {
		f.t.Fatal(err)
	}
}

// TestIntegrateCloseGreenCoversTheItems: collect, the real gate with
// --mark-green on the pinned head (the fake runtime runs the fixture's
// commands on the host), then close: the record is green with the tag,
// and both landed items move to merged under it.
func TestIntegrateCloseGreenCoversTheItems(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	f.land("wi_c", "p#3", map[string]string{"pkg/c.go": "package pkg\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	exit, stdout, errb := invoke(t, NameGate, []string{"--capsule", f.dir, "--pins", f.art("batch.json"), "--mark-green", "--summary", "batch wi_batch", "--json", "--out", f.art("gate.json")}, batchEnv(), newFakeRuntime(t))
	if exit != 0 {
		t.Fatalf("gate.py: exit %d\n%s\n%s", exit, stdout, errb)
	}
	gate := readResult(t, f.art("gate.json"))
	var gd gateDetails
	detailsOf(t, gate, &gd)
	if gate.Verdict != VerdictPass || gate.Heads["app"] != head || gd.Mode != "capsule" || gd.Repos["app"].Head != head || !gd.Repos["app"].Result.Passed || !strings.HasPrefix(gd.Tags["app"], "green/") || gd.Repos["app"].Tag != gd.Tags["app"] {
		t.Fatalf("gate result: %+v %+v", gate, gd)
	}
	if got := gitT(t, f.seed, "ls-remote", "--tags", "origin", "refs/tags/"+gd.Tags["app"]); !strings.Contains(got, "refs/tags/"+gd.Tags["app"]) {
		t.Fatalf("the tag is not on origin: %q", got)
	}
	if !strings.Contains(stdout, `"schema":"pf-step-result/1"`) || strings.Contains(stdout, "gate: the full gate on") {
		t.Fatalf("with --json stdout is the result alone: %q", stdout)
	}
	exit, res, errb := f.close(pool, true)
	if exit != 0 || res == nil || res.Verdict != VerdictPass {
		t.Fatalf("close: exit %d %+v\n%s", exit, res, errb)
	}
	var cd closeDetails
	detailsOf(t, res, &cd)
	ib := cd.IntegrationBatch
	if ib["result"] != "green" || fmt.Sprint(ib["main"]) != "map[app:"+head+"]" || fmt.Sprint(ib["tags"]) != "map[app:"+gd.Tags["app"]+"]" || fmt.Sprint(ib["items"]) != "[wi_a wi_c]" || fmt.Sprint(ib["green_items"]) != "[p#1 p#3]" || ib["closed_at"] == "" {
		t.Fatalf("record: %v", ib)
	}
	if len(cd.Moves) != 2 || cd.Moves[0].WI != "wi_a" || cd.Moves[1].WI != "wi_c" || cd.Moves[0].Set["state"] != "merged" || cd.Moves[0].Set["batch"] != "wi_batch" || cd.Moves[0].Set["green_tag"] != gd.Tags["app"] || cd.Moves[0].Set["green_commit"] != head || fmt.Sprint(cd.Moves[0].Set["green_tags"]) != "map[app:"+gd.Tags["app"]+"]" {
		t.Fatalf("moves: %+v", cd.Moves)
	}
	if res.Heads["app"] != head || !strings.Contains(res.Summary, "the batch is green: 2 landed item(s) move to merged") {
		t.Fatalf("heads %v summary %q", res.Heads, res.Summary)
	}
}

// TestIntegrateCloseRedRecordsTheRedHeads: a red gate tags nothing and the
// record names the head it failed on; an item an earlier green tag
// already covers still moves to merged; a passed repo without its tag
// cannot close; no gate at all closes empty.
func TestIntegrateCloseRedRecordsTheRedHeads(t *testing.T) {
	f := newBatchFixture(t)
	mA := f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	f.tagGreen(mA, "green/20261102-1")
	f.land("wi_b", "p#2", map[string]string{"broken": "x\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	f.writeGate(head, false, "")
	exit, res, errb := f.close(pool, true)
	if exit != 0 || res == nil {
		t.Fatalf("close: exit %d\n%s", exit, errb)
	}
	var cd closeDetails
	detailsOf(t, res, &cd)
	ib := cd.IntegrationBatch
	if ib["result"] != "red" || fmt.Sprint(ib["main"]) != "map[app:"+head+"]" || ib["tags"] != nil || fmt.Sprint(ib["items"]) != "[wi_b]" || fmt.Sprint(ib["green_items"]) != "[p#1]" {
		t.Fatalf("record: %v", ib)
	}
	if len(cd.Moves) != 1 || cd.Moves[0].WI != "wi_a" || fmt.Sprint(cd.Moves[0].Set["green_tags"]) != "map[app:green/20261102-1]" || cd.Moves[0].Set["green_tag"] != nil {
		t.Fatalf("moves: %+v", cd.Moves)
	}
	if !strings.Contains(res.Summary, "red heads app") {
		t.Fatalf("summary: %q", res.Summary)
	}
	// passed without a tag: not closable
	f.writeGate(head, true, "")
	if exit, res, _ := f.close(pool, true); exit != 2 || res == nil || res.Verdict != VerdictError || !strings.Contains(res.Summary, "carries no green tag") {
		t.Fatalf("passed without a tag: exit %d %+v", exit, res)
	}
	// no gate: empty, the covered item still moves
	exit, res, errb = f.close(pool, false)
	if exit != 0 || res == nil {
		t.Fatalf("close without a gate: exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &cd)
	if ib := cd.IntegrationBatch; ib["result"] != "empty" || ib["empty"] != "no gate ran" || len(cd.Moves) != 1 {
		t.Fatalf("record: %v moves %+v", ib, cd.Moves)
	}
	// refused: nothing moves
	if exit, _, errb := f.collect(f.pool(func(s *poolSnapshot) { s.OpenBatches = []string{"wi_other"} })); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	exit, res, errb = f.close(pool, false)
	if exit != 0 || res == nil {
		t.Fatalf("close refused: exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &cd)
	if ib := cd.IntegrationBatch; ib["result"] != "empty" || !strings.Contains(fmt.Sprint(ib["refused"]), "wi_other") || len(cd.Moves) != 0 {
		t.Fatalf("record: %v moves %+v", ib, cd.Moves)
	}
}

// TestIntegrateCloseRecordsTicketsRulingsAndReverts (workflow contract v2,
// change 13; contract 4, design #483 §5.3): the record's fix_tickets are
// the work items the batch's registered file writes filed (the step call
// ledger, `--calls`: every executed hub.file write's reference, once; a
// failed or pending one filed nothing, a move is no ticket), its
// hold_rulings are collect's rulings (`--rulings`: rulings.json's
// hold_rulings), and its reverts are revert.py's landed reverts
// (`--revert`); a named gate, triage or revert file that does not exist
// is absent (an empty batch ran none), and a triage whose regate passed
// closes the batch green on the regate's tags. The moves and the record
// are written to --ops-out and --record-out as the skill registers them.
func TestIntegrateCloseRecordsTicketsRulingsAndReverts(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	mB := f.land("wi_b", "p#2", map[string]string{"broken": "x\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	writeCalls(t, f)
	writeDecision(t, f, "green")
	revert := &Result{Schema: Schema, Command: scriptRevert, Heads: map[string]string{"app": head}, Details: map[string]any{
		"reverts": []any{map[string]any{"wi": "wi_b", "name": "p#2", "group": []any{"wi_b"}, "done": true,
			"repos": []any{map[string]any{"name": "app", "merge_commits": []any{mB}, "revert_pr": "https://forge.invalid/pr/77", "revert_merge": head}}}},
		"heads": map[string]any{"app": head}, "gated": []any{"app"},
	}}
	if err := WriteResult(f.art("revert.json"), revert); err != nil {
		t.Fatal(err)
	}
	// the gate was red, the triage's regate passed and tagged
	f.writeGate(head, false, "")
	regate := map[string]any{"mode": "capsule", "gated": []string{"app"}, "tags": map[string]any{"app": "green/20261110-1"},
		"repos": map[string]any{"app": map[string]any{"head": head, "tag": "green/20261110-1", "result": map[string]any{"commit": head, "passed": true, "runner": "gate.py", "runner_version": "2"}}}}
	gateRaw, err := os.ReadFile(f.art("gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	gateSum := sha256.Sum256(gateRaw)
	if err := WriteResult(f.art("triage.json"), &Result{Schema: Schema, Command: NameGate, Details: map[string]any{"all_flaky": true, "units": []any{}, "regate": regate, "gate_run": hex.EncodeToString(gateSum[:])}}); err != nil {
		t.Fatal(err)
	}
	f.tagGreen(head, "green/20261110-1")
	args := f.closeArgs(pool)
	exit, _, errb := invoke(t, NameIntegrate, args, batchEnv(), newFakeRuntime(t))
	if exit != 0 {
		t.Fatalf("close: exit %d\n%s", exit, errb)
	}
	var cd closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd)
	ib := cd.IntegrationBatch
	if ib["result"] != "green" || fmt.Sprint(ib["tags"]) != "map[app:green/20261110-1]" {
		t.Fatalf("the regate's pass closes the batch green on its tags: %v", ib)
	}
	if fmt.Sprint(ib["fix_tickets"]) != "[p#7 p#9]" {
		t.Fatalf("fix_tickets are the call ledger's filed work items: %v", ib["fix_tickets"])
	}
	// the files the skill registers: the moves as hub writes — each the
	// item's whole record as the pool read it, the move's set over it,
	// fenced on the whole record as read (workflow contract 1) — the record alone
	var ops []map[string]any
	if raw, err := os.ReadFile(f.art("close-ops.json")); err != nil || json.Unmarshal(raw, &ops) != nil {
		t.Fatalf("close-ops.json: %v", err)
	}
	if len(ops) != len(cd.Moves) || len(ops) == 0 {
		t.Fatalf("one move per covered item: %v (moves %+v)", ops, cd.Moves)
	}
	records := map[string]map[string]any{}
	for _, it := range f.items {
		records[it.ID] = it.Integration
	}
	for i, m := range cd.Moves {
		value := map[string]any{}
		for k, v := range records[m.WI] {
			value[k] = v
		}
		for k, v := range m.Set {
			value[k] = v
		}
		want, _ := json.Marshal(map[string]any{"verb": "attrs", "target": m.WI, "name": "integration", "value": value, "if": map[string]any{"integration": records[m.WI]}})
		if got, _ := json.Marshal(ops[i]); string(got) != string(want) {
			t.Fatalf("op %d = %s, want %s", i, got, want)
		}
	}
	var rec map[string]any
	if raw, err := os.ReadFile(f.art("integration-batch.json")); err != nil || json.Unmarshal(raw, &rec) != nil {
		t.Fatalf("integration-batch.json: %v", err)
	}
	if got, want := fmt.Sprint(rec), fmt.Sprint(ib); got != want {
		t.Fatalf("the record file is the details' record:\n got %s\nwant %s", got, want)
	}
	rulings, _ := ib["hold_rulings"].(map[string]any)
	if r, _ := rulings["wi_s"].(map[string]any); r["ruling"] != "clear" || r["words"] != "the red is the environment's" {
		t.Fatalf("hold_rulings are collect's rulings: %v", ib["hold_rulings"])
	}
	reverts, _ := ib["reverts"].([]any)
	if len(reverts) != 1 || fmt.Sprint(reverts[0]) != "map[name:p#2 prs:[https://forge.invalid/pr/77] wi:wi_b]" {
		t.Fatalf("reverts are revert.py's landed reverts: %v", ib["reverts"])
	}
	// the named files absent: the batch closes empty, nothing else named —
	// when the gate step judged it empty (a gated batch whose gate file is
	// missing is TestIntegrateCloseRefusesAGateResultTheFilesDoNotCarry's)
	for _, name := range []string{"gate.json", "triage.json", "revert.json"} {
		if err := os.Remove(f.art(name)); err != nil {
			t.Fatal(err)
		}
	}
	writeDecision(t, f, "empty")
	exit, _, errb = invoke(t, NameIntegrate, args, batchEnv(), newFakeRuntime(t))
	if exit != 0 {
		t.Fatalf("close with absent files: exit %d\n%s", exit, errb)
	}
	var cd2 closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd2)
	if ib := cd2.IntegrationBatch; ib["result"] != "empty" || ib["reverts"] != nil || ib["tags"] != nil || fmt.Sprint(ib["fix_tickets"]) != "[p#7 p#9]" {
		t.Fatalf("absent gate, triage and revert files: %v", ib)
	}
	// an absent call ledger filed nothing
	if err := os.Remove(filepath.Join(f.dir, "runtime-state", "step-calls.jsonl")); err != nil {
		t.Fatal(err)
	}
	if exit, _, errb = invoke(t, NameIntegrate, args, batchEnv(), newFakeRuntime(t)); exit != 0 {
		t.Fatalf("close without a call ledger: exit %d\n%s", exit, errb)
	}
	var cd3 closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd3)
	if ib := cd3.IntegrationBatch; ib["fix_tickets"] != nil {
		t.Fatalf("no call ledger, no fix tickets: %v", ib)
	}
}

// writeCalls writes the batch capsule's step call ledger as the daemon
// does: full-gate's batch (two files and a record write) and collect's
// (one file), recorded pending; the executions — p#9 and p#7 filed, the
// second file of full-gate failed, its record write made, p#9's file
// replayed.
func writeCalls(t *testing.T, f *batchFixture) {
	t.Helper()
	add := func(l stepcall.Line) stepcall.Line {
		t.Helper()
		l, err := stepcall.Append(f.dir, l)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	call := add(stepcall.Line{Kind: stepcall.KindCall, Step: "full-gate", Spawn: 1, Verb: "hub.batch"})
	id := func(n int) string { return fmt.Sprintf("%d.%d", call.Seq, n) }
	add(stepcall.Line{Kind: stepcall.KindResult, Step: "full-gate", Spawn: 1, Verb: "hub.batch", Call: call.Seq, Outcome: stepcall.OutcomeOK, Pending: []stepcall.Op{
		{ID: id(1), Verb: stepcall.HubFile, Key: "wi_batch/fix/wi_b/0", Lane: "develop", Goal: "g", Content: "c"},
		{ID: id(2), Verb: stepcall.HubFile, Key: "wi_batch/fix/wi_b/1", Lane: "develop", Goal: "g2", Content: "c"},
		{ID: id(3), Verb: stepcall.HubAttrs, Target: "wi_b", Name: "integration", Value: json.RawMessage(`{"state":"landed"}`)},
	}})
	add(stepcall.Line{Kind: stepcall.KindExec, Step: "full-gate", Spawn: 1, Verb: "hub.file", Op: id(1), Ref: "p#9", Outcome: stepcall.ExecDone})
	add(stepcall.Line{Kind: stepcall.KindExec, Step: "full-gate", Spawn: 1, Verb: "hub.file", Op: id(2), Outcome: stepcall.ExecFailed, Error: "aihub unreachable"})
	add(stepcall.Line{Kind: stepcall.KindExec, Step: "full-gate", Spawn: 1, Verb: "hub.attrs", Op: id(3), Ref: "p#8", Outcome: stepcall.ExecDone})
	call = add(stepcall.Line{Kind: stepcall.KindCall, Step: "collect", Spawn: 1, Verb: "hub.batch"})
	add(stepcall.Line{Kind: stepcall.KindResult, Step: "collect", Spawn: 1, Verb: "hub.batch", Call: call.Seq, Outcome: stepcall.OutcomeOK, Pending: []stepcall.Op{
		{ID: fmt.Sprintf("%d.1", call.Seq), Verb: stepcall.HubFile, Key: "hold-fix/wi_s/1", Lane: "develop", Goal: "g", Content: "c"},
	}})
	add(stepcall.Line{Kind: stepcall.KindExec, Step: "collect", Spawn: 1, Verb: "hub.file", Op: fmt.Sprintf("%d.1", call.Seq), Ref: "p#7", Outcome: stepcall.ExecDone})
	add(stepcall.Line{Kind: stepcall.KindExec, Step: "full-gate", Spawn: 1, Verb: "hub.file", Op: id(1), Ref: "p#9", Outcome: stepcall.ExecDone}) // a replay
}

// writeDecision writes the files close reads of the steps before it:
// collect's rulings (a revert and a clear ruling) and full-gate's decision
// with result.
func writeDecision(t *testing.T, f *batchFixture, result string) {
	t.Helper()
	writeFile(t, f.dir, "artifacts/integrate/rulings.json", `{"round":1,"reverts":["wi_b"],"hold_rulings":[{"item":"wi_s","ruling":"clear","words":"the red is the environment's","at":"2026-11-11T10:00:00Z"}]}`)
	writeFile(t, f.dir, "artifacts/integrate/gate-decision.json", `{"round":1,"result":"`+result+`","reverts":[],"reruns":0}`)
}

// closeArgs answers close's line as the close skill runs it (with the
// fixture's pool file).
func (f *batchFixture) closeArgs(pool string) []string {
	return []string{"close", "--pool", pool, "--batch", f.art("batch.json"), "--revert", f.art("revert.json"), "--gate", f.art("gate.json"), "--triage", f.art("triage.json"),
		"--rulings", f.art("rulings.json"), "--decision", f.art("gate-decision.json"), "--calls", filepath.Join(f.dir, "runtime-state", "step-calls.jsonl"),
		"--ops-out", f.art("close-ops.json"), "--record-out", f.art("integration-batch.json"),
		"--capsule", f.dir, "--json", "--out", f.art("close.json")}
}

// writeGateRound writes a gate.py result of the fixture's app judged at
// head in round, passed (with a tag) or failed.
func (f *batchFixture) writeGateRound(name, round, head string, passed bool) {
	tag := ""
	if passed {
		tag = "green/20261111-1"
	}
	repo := map[string]any{"head": head, "tag": tag, "result": map[string]any{"commit": head, "passed": passed, "runner": "gate.py", "runner_version": "2"}}
	res := &Result{Schema: Schema, Command: NameGate, Round: round, Heads: map[string]string{"app": head}, Details: map[string]any{
		"mode": "capsule", "gated": []string{"app"}, "repos": map[string]any{"app": repo}, "tags": map[string]any{"app": tag},
	}}
	if err := WriteResult(f.art(name), res); err != nil {
		f.t.Fatal(err)
	}
}

// writeGateRun is writeGateRound with the run's start and end stamps.
func (f *batchFixture) writeGateRun(name, round, head string, passed bool, started, ended string) {
	f.writeGateRound(name, round, head, passed)
	r := readResult(f.t, f.art(name))
	r.StartedAt, r.EndedAt = started, ended
	if err := WriteResult(f.art(name), r); err != nil {
		f.t.Fatal(err)
	}
}

// writeTriageOf writes a gate.py --triage result of the current gate.json
// (round, heads, started) whose regate passed or failed: what gate.py
// --triage records of the gate run it triaged.
func (f *batchFixture) writeTriageOf(round, head string, heads map[string]string, regatePassed bool, started string) {
	f.writeTriageRound(round, head, heads, regatePassed)
	r := readResult(f.t, f.art("triage.json"))
	r.StartedAt = started
	d, _ := r.Details.(map[string]any)
	raw, err := os.ReadFile(f.art("gate.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	d["gate_run"] = hex.EncodeToString(sum[:])
	if err := WriteResult(f.art("triage.json"), r); err != nil {
		f.t.Fatal(err)
	}
}

// writeTriageRound writes a gate.py --triage result of round over heads
// whose regate (every failure flaky, the gate once more) passed or failed.
func (f *batchFixture) writeTriageRound(round, head string, heads map[string]string, regatePassed bool) {
	repo := map[string]any{"head": head, "result": map[string]any{"commit": head, "passed": regatePassed, "runner": "gate.py", "runner_version": "2"}}
	if regatePassed {
		repo["tag"] = "green/20261111-1"
	}
	res := &Result{Schema: Schema, Command: NameGate, Round: round, Heads: heads, Details: map[string]any{
		"all_flaky": true, "units": []any{}, "regate": map[string]any{"mode": "capsule", "gated": []string{"app"}, "repos": map[string]any{"app": repo}},
	}}
	if err := WriteResult(f.art("triage.json"), res); err != nil {
		f.t.Fatal(err)
	}
}

// TestIntegrateCloseRefusesAGateResultTheFilesDoNotCarry (workflow
// contract v2 change 13, review item 4; contract 4: the decision file):
// close reads the gate step's result off its decision (gate-decision.json)
// and refuses (exit 2) a batch whose files give another — a gated batch
// the gate judged green whose gate.json is missing never closes silently
// empty; a red judgment over a passed gate file is refused the same way; a
// decision naming no result, or none at all, is refused; the agreeing
// judgment closes. A refusal leaves no ops or record file behind, an
// earlier run's included.
func TestIntegrateCloseRefusesAGateResultTheFilesDoNotCarry(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	// the gate step judged green, the gate file is missing (an earlier
	// run's ops and record files lie about)
	writeDecision(t, f, "green")
	writeFile(t, f.dir, "artifacts/integrate/close-ops.json", `[{"verb":"attrs","target":"wi_stale","name":"integration","value":{"state":"merged"}}]`)
	writeFile(t, f.dir, "artifacts/integrate/integration-batch.json", `{"result":"green"}`)
	exit, _, _ := invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	res := readResult(t, f.art("close.json"))
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "the gate step's result is green, but the gate files give empty") {
		t.Fatalf("a judged batch without its gate file must not close: exit %d %q", exit, res.Summary)
	}
	for _, name := range []string{"close-ops.json", "integration-batch.json"} {
		if _, err := os.Stat(f.art(name)); !os.IsNotExist(err) {
			t.Fatalf("a refused close left %s behind: %v", name, err)
		}
	}
	// the gate step judged red, the gate file passed
	f.writeGateRound("gate.json", "r1", head, true)
	writeDecision(t, f, "red")
	exit, _, _ = invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	if res = readResult(t, f.art("close.json")); exit != 2 || !strings.Contains(res.Summary, "the gate step's result is red, but the gate files give green") {
		t.Fatalf("a red judgment over a passed gate: exit %d %q", exit, res.Summary)
	}
	// the decision names no result, or is missing
	writeDecision(t, f, "")
	exit, _, _ = invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	if res = readResult(t, f.art("close.json")); exit != 2 || !strings.Contains(res.Summary, "the gate step has not decided") {
		t.Fatalf("a decision without a result: exit %d %q", exit, res.Summary)
	}
	if err := os.Remove(f.art("gate-decision.json")); err != nil {
		t.Fatal(err)
	}
	exit, _, _ = invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	if res = readResult(t, f.art("close.json")); exit != 2 || !strings.Contains(res.Summary, "the gate decision") {
		t.Fatalf("a missing decision: exit %d %q", exit, res.Summary)
	}
	// agreeing: green over the passed gate closes
	writeDecision(t, f, "green")
	exit, _, errb := invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	var cd closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd)
	if exit != 0 || cd.IntegrationBatch["result"] != "green" {
		t.Fatalf("the agreeing judgment closes: exit %d %v\n%s", exit, cd.IntegrationBatch, errb)
	}
}

// TestIntegrateCloseIgnoresAStaleRegate (workflow contract v2 change 13,
// review item 5): a triage's regate is the gate's result only when the
// triage is this gate's — the same round and heads. A failed regate of an
// earlier round, or of other heads, never turns a green gate red; the
// triage of this very gate does count (the control).
func TestIntegrateCloseIgnoresAStaleRegate(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	closeResult := func(want int) string {
		t.Helper()
		exit, _, errb := invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
		if exit != want {
			t.Fatalf("close: exit %d, want %d (%s)\n%s", exit, want, readResult(t, f.art("close.json")).Summary, errb)
		}
		var cd closeDetails
		detailsOf(t, readResult(t, f.art("close.json")), &cd)
		r, _ := cd.IntegrationBatch["result"].(string)
		return r
	}
	f.writeGateRound("gate.json", "r2", head, true)
	writeDecision(t, f, "green")
	// an earlier round's triage, its regate failed
	f.writeTriageRound("r1", head, map[string]string{"app": head}, false)
	if got := closeResult(0); got != "green" {
		t.Fatalf("an earlier round's failed regate turned the green gate %s", got)
	}
	// this round's triage over other heads
	f.writeTriageRound("r2", head, map[string]string{"app": "0000000000000000000000000000000000000000"}, false)
	if got := closeResult(0); got != "green" {
		t.Fatalf("a triage of other heads turned the green gate %s", got)
	}
	// the control: this gate's own triage (round and heads) counts — its
	// failed regate is the batch's result (the gate step judged red)
	f.writeGateRound("gate.json", "r2", head, false)
	f.writeTriageOf("r2", head, map[string]string{"app": head}, false, "")
	writeDecision(t, f, "red")
	if got := closeResult(0); got != "red" {
		t.Fatalf("this gate's failed regate is the batch's result, got %s", got)
	}
}

// TestIntegrateCloseRerunAfterAFailedRegateClosesGreen (review round 2 of
// change 13): every failure re-ran green but the regate failed, the owner
// had the same pins gated again (rerun: the same round, the same heads)
// and that gate passed — the earlier triage judged the gate run the rerun
// replaced, so its failed regate is no longer the batch's result: close
// records green.
func TestIntegrateCloseRerunAfterAFailedRegateClosesGreen(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	head := f.originHead()
	// the gate run, red; its triage (this run's): all flaky, the regate red
	f.writeGateRun("gate.json", "r2", head, false, "2026-11-11T10:00:00Z", "2026-11-11T10:05:00Z")
	f.writeTriageOf("r2", head, map[string]string{"app": head}, false, "2026-11-11T10:05:00Z")
	writeDecision(t, f, "red")
	exit, _, errb := invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	var cd closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd)
	if exit != 0 || cd.IntegrationBatch["result"] != "red" {
		t.Fatalf("the gate run's own failed regate is the batch's result: exit %d %v\n%s", exit, cd.IntegrationBatch, errb)
	}
	// the owner's rerun: the same round and heads gated again, green
	f.writeGateRun("gate.json", "r2", head, true, "2026-11-11T10:20:00Z", "2026-11-11T10:25:00Z")
	writeDecision(t, f, "green")
	exit, _, errb = invoke(t, NameIntegrate, f.closeArgs(pool), batchEnv(), newFakeRuntime(t))
	res := readResult(t, f.art("close.json"))
	var cd2 closeDetails
	detailsOf(t, res, &cd2)
	if exit != 0 || cd2.IntegrationBatch["result"] != "green" {
		t.Fatalf("the rerun's green gate closes the batch green: exit %d %q %v\n%s", exit, res.Summary, cd2.IntegrationBatch, errb)
	}
}
