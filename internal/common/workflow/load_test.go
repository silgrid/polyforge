package workflow_test

// load_test.go: the one lane loader against the contract
// (docs/reference/workflow-contract.md): every shipped lane loads (③), the
// registry dump is pinned to the contract version (①), every WFC code in
// force has a bad-lane fixture producing exactly it (④), another
// contract version is refused (⑤), and one load reports every defect at
// once with its file, line and YAML path.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// shippedLanes is the shipped lanes root.
const shippedLanes = "../../../workflows"

// TestShippedLanesLoad (③): every shipped lane loads without a defect at
// this contract, with its rewind budget.
func TestShippedLanesLoad(t *testing.T) {
	dirs, err := workflow.LaneDirs(shippedLanes)
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no shipped lanes: %v", err)
	}
	got := map[string]*workflow.Lane{}
	for _, d := range dirs {
		lane, errs := workflow.Load(d)
		if len(errs) > 0 {
			t.Errorf("shipped lane %s refused: %v", d, workflow.AsLoadError(d, errs))
			continue
		}
		got[lane.Name] = lane
	}
	for name, max := range map[string]int{"develop": 3, "micro": 3, "integrate": 3, "release": 0} {
		l := got[name]
		if l == nil {
			t.Fatalf("shipped lane %s missing (have %v)", name, keys(got))
		}
		if l.Contract != workflow.ContractVersion || l.MaxRewinds != max {
			t.Errorf("%s: contract %d max_rewinds %d, want %d %d", name, l.Contract, l.MaxRewinds, workflow.ContractVersion, max)
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// goldenName is the registry dump's golden file of contract version n.
func goldenName(n int) string { return fmt.Sprintf("contract.v%d.golden", n) }

// The pin's other two files: the registry's baseline (the shape the last
// version bump, or the last compatible change, left) and the list of
// incompatible changes registered since.
var (
	baselineGolden = filepath.Join("testdata", "contract.baseline.golden")
	pendingList    = "contract-pending.md"
)

// TestRegistryGoldenPinsTheVersion (①): ContractVersion changes by one
// rule: only an incompatible change bumps it, at most once per release. A
// compatible change (a new optional key whose default is the present
// behaviour, a new value of a key) never bumps it and needs no drain: the
// registry dump's golden file and its baseline are regenerated in place
// (PF_WRITE_CONTRACT_GOLDEN=1) and the lane schema with go generate. An
// incompatible change the dump shows against the baseline (a removed key,
// a new required key, a removed value) is red here until it is registered
// in contract-pending.md; the release that follows bumps the version once
// for every registered entry and empties the list (.polyforge/release.sh
// refuses a release whose list is not empty while the version stayed).
// No golden file of a later version exists.
func TestRegistryGoldenPinsTheVersion(t *testing.T) {
	path := filepath.Join("testdata", goldenName(workflow.ContractVersion))
	dump := workflow.RegistryDump()
	write := os.Getenv("PF_WRITE_CONTRACT_GOLDEN") == "1"
	list, err := os.ReadFile(pendingList)
	if err != nil {
		t.Fatalf("the incompatible-change list is missing: %v", err)
	}
	pending := len(workflow.PendingEntries(string(list)))
	base, err := os.ReadFile(baselineGolden)
	if err != nil && !(write && os.IsNotExist(err)) {
		t.Fatalf("the registry baseline is missing (%v): write it with PF_WRITE_CONTRACT_GOLDEN=1", err)
	}
	if write {
		if err := os.WriteFile(path, []byte(dump), 0o644); err != nil {
			t.Fatal(err)
		}
		// The baseline follows only a change that needs no registration:
		// an incompatible one left unregistered stays red.
		sameVersion := strings.HasPrefix(string(base), fmt.Sprintf("contract %d\n", workflow.ContractVersion))
		if pending == 0 && (!sameVersion || len(workflow.IncompatibleChanges(string(base), dump)) == 0) {
			if err := os.WriteFile(baselineGolden, []byte(dump), 0o644); err != nil {
				t.Fatal(err)
			}
			base = []byte(dump)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the registry golden file of contract %d is missing (%v): write it with PF_WRITE_CONTRACT_GOLDEN=1", workflow.ContractVersion, err)
	}
	if err := workflow.JudgeRegistry(dump, string(golden), string(base), pending); err != nil {
		t.Fatal(err)
	}
	goldens, _ := filepath.Glob(filepath.Join("testdata", "contract.v*.golden"))
	re := regexp.MustCompile(`contract\.v(\d+)\.golden$`)
	for _, g := range goldens {
		m := re.FindStringSubmatch(g)
		if n, _ := strconv.Atoi(m[1]); n > workflow.ContractVersion {
			t.Errorf("%s pins contract %d, later than ContractVersion %d", g, n, workflow.ContractVersion)
		}
	}
}

// fixtureCode answers the WFC code a bad-lane fixture's directory name
// (wfc-<nnn>[-<slug>]) names.
func fixtureCode(name string) string {
	return "WFC-" + strings.SplitN(strings.TrimPrefix(name, "wfc-"), "-", 2)[0]
}

// userFixtures is the lane set the user-lane fixtures are judged in: the
// built-in lanes under testdata/user/builtin (its shared tree, and the
// built-in lane the WFC-122 fixture's name shadows) and the user lanes
// under testdata/user/lanes.
var userFixtures = workflow.LaneSet{
	Builtin: filepath.Join("testdata", "user", "builtin"),
	User:    filepath.Join("testdata", "user", "lanes"),
}

// TestEveryCodeHasABadLane (④): every fixture under testdata/bad loads
// with exactly one defect, of the code its name carries, with a file, and
// a line whenever the defect sits in a YAML node — and so does every user
// lane fixture under testdata/user/lanes, judged as a user lane of its set
// (userFixtures: WFC-122 and the user lane's WFC-172 are the set's and the
// user lane's rules); every code in force has at least one fixture except
// WFC-123, which only a claim produces (its fixture is a lane that is not
// there: the runtime's claim tests).
func TestEveryCodeHasABadLane(t *testing.T) {
	dirs, err := workflow.LaneDirs(filepath.Join("testdata", "bad"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	judge := func(name, d string, errs []workflow.ContractError) {
		want := fixtureCode(name)
		if len(errs) != 1 || errs[0].Code != want {
			t.Errorf("fixture %s: defects %v, want exactly one %s", name, errs, want)
			return
		}
		if errs[0].File == "" || !strings.HasPrefix(errs[0].File, d) {
			t.Errorf("fixture %s: defect names no file of the lane: %+v", name, errs[0])
		}
		covered[want] = true
	}
	for _, d := range dirs {
		_, errs := workflow.Load(d)
		judge(filepath.Base(d), d, errs)
	}
	builtin, err := workflow.LaneDirs(userFixtures.Builtin)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range builtin {
		if _, errs := workflow.Load(d); len(errs) != 0 {
			t.Errorf("the user fixtures' built-in lane %s must load: %v", d, errs)
		}
	}
	user, err := userFixtures.UserLanes()
	if err != nil || len(user) == 0 {
		t.Fatalf("no user lane fixtures: %v", err)
	}
	for _, sl := range user {
		judge(sl.Name, sl.Dir, sl.Errors)
	}
	for code := range workflow.Codes {
		if code == workflow.CodeLaneMissing {
			continue // a claim-time code: the runtime's claim tests
		}
		if !covered[code] {
			t.Errorf("code %s (%s) has no bad-lane fixture under testdata/bad", code, workflow.Codes[code])
		}
	}
}

// internalStages are the contract numbers pre-release builds carried
// before the first public release started the numbering at 1 (docs/
// upgrade.md, workflow.UpgradeSection): a lane or a snapshot of such a
// build must meet WFC-101, never run.
var internalStages = []int{2, 3, 4, 5}

// TestOtherContractVersionsRefused (⑤): a lane rewritten to target
// another contract version — the previous one, and each number an
// internal build carried — is refused with exactly WFC-101, naming both
// versions and the upgrade section.
func TestOtherContractVersionsRefused(t *testing.T) {
	versions := append([]int{workflow.ContractVersion - 1}, internalStages...)
	for _, v := range versions {
		if v == workflow.ContractVersion {
			continue
		}
		root := t.TempDir()
		workflowtest.WriteLane(t, root, "micro", "steps:\n  - { id: fix }\n")
		p := filepath.Join(root, "micro", workflow.WorkflowFile)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		cur := fmt.Sprintf("contract: %d\n", workflow.ContractVersion)
		if !strings.Contains(string(raw), cur) {
			t.Fatalf("micro declares no %q", cur)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(raw), cur, fmt.Sprintf("contract: %d\n", v), 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, errs := workflow.Load(filepath.Join(root, "micro"))
		if len(errs) != 1 || errs[0].Code != workflow.CodeContractVersion || errs[0].Line != 1 || errs[0].Path != "contract" {
			t.Fatalf("contract %d: defects %v, want one %s at line 1, path contract", v, errs, workflow.CodeContractVersion)
		}
		for _, want := range []string{fmt.Sprintf("targets contract %d,", v), fmt.Sprintf("implements contract %d", workflow.ContractVersion), "docs/upgrade.md", workflow.UpgradeSection} {
			if !strings.Contains(errs[0].Msg, want) {
				t.Errorf("contract %d: WFC-101 message %q does not name %q", v, errs[0].Msg, want)
			}
		}
	}
}

// TestLoadReportsEveryDefectAtOnce: one load of a lane with defects in
// the workflow.yaml and in a definition answers all of them, in file and
// line order, each with its code, file, line and YAML path.
func TestLoadReportsEveryDefectAtOnce(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "many")
	write(t, filepath.Join(dir, workflow.WorkflowFile), `# a lane with several defects
contract: `+fmt.Sprint(workflow.ContractVersion)+`
name: many
description: a lane with several defects
steps:
  - id: build
    context_policy: fresh
  - id: review
    gate: true
    on_fail: { target: build, rewind: always }
    kind: judge
  - id: Bad_Id
`)
	write(t, filepath.Join(dir, "steps", "build.md"), workflowtest.Definition("build", "nowhere"))
	write(t, filepath.Join(dir, "steps", "review.md"), workflowtest.Definition("review"))
	_, errs := workflow.Load(dir)
	type key struct {
		code string
		line int
		path string
	}
	var got []key
	for _, e := range errs {
		got = append(got, key{e.Code, e.Line, e.Path})
	}
	wf := filepath.Join(dir, workflow.WorkflowFile)
	want := []key{
		{workflow.CodeUnknownKey, 7, "steps[0](build).context_policy"},
		{workflow.CodeUnknownKey, 10, "steps[1](review).on_fail.rewind"},
		{workflow.CodeUnknownKey, 11, "steps[1](review).kind"},
		{workflow.CodeStepID, 12, "steps[2].id"},
		{workflow.CodeMissingKey, 2, "max_rewinds"},
		{workflow.CodePlaceholder, 7, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("defects %v\nwant %v", errs, want)
	}
	seen := map[key]bool{}
	for _, k := range got {
		seen[k] = true
	}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("missing defect %+v in %v", k, errs)
		}
	}
	// the workflow.yaml defects come before the definition's, each file in
	// line order
	rank := func(e workflow.ContractError) int {
		if e.File == wf {
			return 0
		}
		return 1
	}
	for i := 1; i < len(errs); i++ {
		a, b := errs[i-1], errs[i]
		if rank(a) > rank(b) || (a.File == b.File && a.Line > b.Line) {
			t.Errorf("defects out of order: %v before %v", a, b)
		}
	}
	if errs[0].File != wf {
		t.Errorf("first defect names %s, want %s", errs[0].File, wf)
	}
	msg := workflow.AsLoadError(dir, errs).Error()
	for _, k := range want {
		if !strings.Contains(msg, k.code) {
			t.Errorf("the load error does not render %s: %s", k.code, msg)
		}
	}
}

// TestLoadReadsTheLane: a well-formed lane's keys arrive as declared —
// candidates canonical, the budget, the gate's on_fail, the hub verbs and
// attrs keys and the wrap attrs (HubAllowed adding attrs and wrap-attrs
// for the latter two).
func TestLoadReadsTheLane(t *testing.T) {
	root := t.TempDir()
	dir := workflowtest.WriteLane(t, root, "demo", `max_rewinds: 2
defaults:
  model_candidates: [pi:prov-a/model-a1, claude:prov-b/model-b1]
steps:
  - id: draft
    budget: { wall_minutes: 30 }
    hub: { verbs: [get, list], attrs: [integration] }
  - id: gate
    gate: true
    on_fail: { target: draft }
    model_candidates: [" claude:prov-b/model-b2 "]
  - id: land
    wrap_attrs: [integration]
`)
	lane, errs := workflow.Load(dir)
	if len(errs) > 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	if lane.Defaults == nil || strings.Join(lane.Defaults.ModelCandidates, ",") != "pi:prov-a/model-a1,claude:prov-b/model-b1" {
		t.Errorf("defaults = %+v", lane.Defaults)
	}
	draft, gate, land := lane.Step("draft"), lane.Step("gate"), lane.Step("land")
	if draft.WallMinutes != 30 || strings.Join(draft.Hub, ",") != "get,list" || strings.Join(draft.HubAttrs, ",") != "integration" || strings.Join(draft.HubAllowed(), ",") != "get,list,attrs" {
		t.Errorf("draft = %+v", draft)
	}
	if !gate.Gate || gate.OnFailTarget != "draft" || strings.Join(gate.ModelCandidates, ",") != "claude:prov-b/model-b2" || gate.HubAllowed() != nil {
		t.Errorf("gate = %+v", gate)
	}
	if strings.Join(land.WrapAttrs, ",") != "integration" || strings.Join(land.HubAllowed(), ",") != "wrap-attrs" {
		t.Errorf("land = %+v (hub allowed %v)", land, land.HubAllowed())
	}
}

// TestStepIDsReadThroughTheLoader: the step-id enumeration of a lane set
// reads every lane through Load; a lane with defects still contributes
// what Load read (its defect is the lane's, not the workspace yaml's), a
// lane whose workflow.yaml does not parse contributes nothing,
// `_`-prefixed directories are not lanes, and an unreadable root is an
// error.
func TestStepIDsReadThroughTheLoader(t *testing.T) {
	root := t.TempDir()
	workflowtest.WriteLane(t, root, "a", "steps:\n  - id: fix\n  - id: review\n")
	write(t, filepath.Join(root, "b", workflow.WorkflowFile), fmt.Sprintf("contract: %d", workflow.ContractVersion)+"\nname: b\ndescription: refused\nsteps:\n  - { id: deploy, kind: deploy }\n")
	write(t, filepath.Join(root, "torn", workflow.WorkflowFile), "steps: [unclosed\n")
	write(t, filepath.Join(root, "_archive", "old", workflow.WorkflowFile), "steps:\n  - id: archived\n")
	ids, err := workflow.LaneSet{Builtin: root}.StepIDs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys(ids), ",") != "deploy,fix,review" {
		t.Fatalf("step ids = %v, want deploy, fix, review", keys(ids))
	}
	if _, err := (workflow.LaneSet{Builtin: filepath.Join(root, "missing")}).StepIDs(); err == nil {
		t.Fatal("an unreadable lanes root must be an error")
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		out := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestContentHashCoversPathsAndBytes: the snapshot hash changes with any
// file's bytes or path, never with the order the paths are listed in.
func TestContentHashCoversPathsAndBytes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.md"), "one")
	write(t, filepath.Join(root, "b", "c.md"), "two")
	h1, err := workflow.ContentHash(root, []string{"a.md", "b/c.md"})
	if err != nil {
		t.Fatal(err)
	}
	if h2, _ := workflow.ContentHash(root, []string{"b/c.md", "a.md"}); h2 != h1 {
		t.Fatalf("order changed the hash: %s vs %s", h1, h2)
	}
	write(t, filepath.Join(root, "a.md"), "One")
	if h3, _ := workflow.ContentHash(root, []string{"a.md", "b/c.md"}); h3 == h1 {
		t.Fatal("a changed byte left the hash unchanged")
	}
	write(t, filepath.Join(root, "b", "d.md"), "two")
	if h4, _ := workflow.ContentHash(root, []string{"a.md", "b/d.md"}); h4 == h1 {
		t.Fatal("a renamed path left the hash unchanged")
	}
	if _, err := workflow.ContentHash(root, []string{"gone.md"}); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

// A definition with a placeholder defect still has its frontmatter judged:
// one load reports both.
func TestLoadReportsFrontmatterBesidePlaceholderDefects(t *testing.T) {
	root := t.TempDir()
	dir := workflowtest.WriteLaneUnchecked(t, root, "both", "steps:\n  - { id: draft }\n")
	write(t, filepath.Join(dir, "steps", "draft.md"), "---\nname: draft\ndescription: d\nkind: code\n---\n\nUse {{nowhere}}.\n")
	_, errs := workflow.Load(dir)
	codes := map[string]bool{}
	for _, e := range errs {
		codes[e.Code] = true
	}
	if len(errs) != 2 || !codes[workflow.CodePlaceholder] || !codes[workflow.CodeDefinitionFront] {
		t.Fatalf("defects %v, want the placeholder (WFC-195) and the unknown frontmatter key (WFC-191)", errs)
	}
}
