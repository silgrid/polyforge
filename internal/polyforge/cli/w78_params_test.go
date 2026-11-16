package cli

// w78_params_test.go (polyforge#78 AC2): the project layer of a step's
// params through the CLI. `pf config set steps.<p>.<s>.params.<name>
// <value>` writes the one leaf (every other byte kept) only when some lane
// of the workspace declares that param on that step (owner ruling 6) and
// the value is one line of at most 256 bytes; `unset` drops it and every
// entry it left empty; a five-segment key other than params stays an
// unknown workspace key. `pf config workspace --cwd` reports a step
// entry's params (owner ruling 12), and `pf workflows --cwd` each step
// param's final value per mapped project with its level — the lane
// default or the project (owner ruling 13, the model chain's labels).

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// w78Lane is a lane whose probe step declares two params; its code step
// declares none (the feature-full lane of the fixture has a code step too).
const w78Lane = "steps:\n  - id: probe\n    params:\n      image: base-img\n      flag: \"\"\n  - id: code\n"

func TestW78ConfigSetStepParam(t *testing.T) {
	const before = "# keep me\nsteps:\n  alpha:\n    code: { models: [\"pi:c\"] }\n"
	f := newWfFixture(t, before)
	writeLane(t, f.workflowsDir, "parlane", w78Lane)
	path := filepath.Join(f.ws, ".polyforge.yaml")

	// a declared param: the step entry, its params block and the one leaf
	// are created in block style; nothing else moves
	sha, _ := fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.probe.params.image", "reg/img:2", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("set steps.alpha.probe.params.image exited %d: %s", code, stderr)
	}
	want := before + "    probe:\n      params:\n        image: \"reg/img:2\"\n"
	if _, now := fileHash(t, path); string(now) != want {
		t.Fatalf("the param write is not the one leaf:\n--- got\n%s--- want\n%s", now, want)
	}

	// refusals: each leaves the file byte-identical
	sha, raw := fileHash(t, path)
	for name, c := range map[string]struct{ key, value, why string }{
		"a param no lane declares":          {"steps.alpha.probe.params.ghost", "x", "ghost"},
		"a param the step does not declare": {"steps.alpha.code.params.image", "x", "params"},
		"a value of two lines":              {"steps.alpha.probe.params.image", "a\nb", "one line"},
		"a value over the bound":            {"steps.alpha.probe.params.image", strings.Repeat("v", 257), "at most 256 bytes"},
		"a removed five-segment key":        {"steps.alpha.code.attrs.question_batch_size", "3", "unknown workspace key"},
	} {
		code, _, stderr := runPf(t, f.env, "config", "set", c.key, c.value, "--cwd", f.ws, "--expect-hash", sha)
		if code == 0 || !strings.Contains(stderr, c.why) {
			t.Errorf("%s: set %s must be refused naming %q: %d %s", name, c.key, c.why, code, stderr)
		}
		if _, now := fileHash(t, path); string(now) != string(raw) {
			t.Fatalf("%s: a refused write changed the file:\n%s", name, now)
		}
	}

	// the key is live: the next dispatch composes it
	if got := WorkspaceConfigClass("steps.alpha.probe.params.image"); got != ConfigLive {
		t.Errorf("the class of steps.<p>.<s>.params.<n> is %q, want %q", got, ConfigLive)
	}

	// unset: the leaf goes, and the params block and entry it left empty
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.probe.params.image", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unset steps.alpha.probe.params.image exited %d: %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != before {
		t.Fatalf("unset did not restore the file:\n--- got\n%s--- want\n%s", now, before)
	}
}

func TestW78ConfigWorkspaceParams(t *testing.T) {
	f := newWfFixture(t, "steps:\n  alpha:\n    probe:\n      params: { image: proj-img, flag: \"\" }\n    code: { models: [pi:c] }\n")
	writeLane(t, f.workflowsDir, "parlane", w78Lane)
	doc := readWorkspaceDoc(t, f)
	probe := stepRead(t, doc, "alpha", "probe")
	if want := map[string]any{"image": "proj-img", "flag": ""}; !reflect.DeepEqual(probe["params"], want) {
		t.Fatalf("steps.alpha.probe reports params %v, want %v (entry %v)", probe["params"], want, probe)
	}
	if v, ok := stepRead(t, doc, "alpha", "code")["params"]; ok && v != nil {
		t.Fatalf("an entry declaring no params reports none: %v", v)
	}
}

func TestW78ConfigWorkflowsParams(t *testing.T) {
	f := newWfFixture(t, "steps:\n  alpha:\n    probe: { params: { image: proj-img } }\n    code: { params: { image: leak } }\n")
	writeLane(t, f.workflowsDir, "parlane", w78Lane)
	code, stdout, stderr := runPf(t, f.env, "--json", "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("pf workflows exited %d: %s", code, stderr)
	}
	var doc struct {
		Reason  string `json:"reason"`
		Bundles []struct {
			Name  string `json:"name"`
			Steps []struct {
				ID     string                                  `json:"id"`
				Params map[string]map[string]map[string]string `json:"params"`
			} `json:"steps"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("pf workflows did not print JSON: %v\n%s", err, stdout)
	}
	if doc.Reason != "" {
		t.Fatalf("pf workflows degraded: %s", doc.Reason)
	}
	params := map[string]map[string]map[string]map[string]string{}
	for _, b := range doc.Bundles {
		for _, s := range b.Steps {
			if s.Params != nil {
				params[b.Name+"/"+s.ID] = s.Params
			}
		}
	}
	// probe's params per mapped project; the code steps (parlane's declares
	// no params, feature-full's neither) show none — the project's image on
	// code governs nothing
	want := map[string]map[string]map[string]map[string]string{"parlane/probe": {
		"alpha": {
			"image": {"value": "proj-img", "from": "steps.alpha.probe.params.image"},
			"flag":  {"value": "", "from": "bundle parlane step probe params.flag"},
		},
		"beta": {
			"image": {"value": "base-img", "from": "bundle parlane step probe params.image"},
			"flag":  {"value": "", "from": "bundle parlane step probe params.flag"},
		},
	}}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("pf workflows step params = %v, want %v", params, want)
	}
}
