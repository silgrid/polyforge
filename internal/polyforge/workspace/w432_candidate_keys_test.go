package workspace

// w432_candidate_keys_test.go: every list is composed
// under a step's id, through the same chain (pf submit --models > the
// yaml's step-id key > the step's model_candidates > the yaml's default >
// the lane's defaults). The sub-definitions the daemon actions dispatched
// (the verify judge, the conflict resolver, the integrate ruling) had no
// key of their own; they left with the actions (workflow contract v2
// changes 17 and 16). A yaml key that names no installed step — the
// retired `verify-judge` among them — is stale: the yaml loads, the key
// governs nothing, and StaleKeys names it for the doctor's warning.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

const shippedWorkflows = "../../../workflows"

func shippedLane(t *testing.T, name string) *workflow.Lane {
	t.Helper()
	lane, err := workflow.LoadStrict(filepath.Join(shippedWorkflows, name))
	if err != nil {
		t.Fatal(err)
	}
	return lane
}

// TestW432EveryListIsKeyedByAStep: every list the shipped lanes compose
// is keyed by a step of the lane — none by any other name (the
// sub-definitions the daemon actions dispatched, workflow contract v2
// change 16, had none of their own).
func TestW432EveryListIsKeyedByAStep(t *testing.T) {
	for _, lane := range []string{"develop", "micro", "integrate", "release"} {
		for key := range ComposeCandidates(shippedLane(t, lane), nil, "p", []string{"pi:m"}) {
			if shippedLane(t, lane).Step(key) == nil {
				t.Errorf("%s composes a list under %q, which is no step of the lane", lane, key)
			}
		}
	}
}

// TestW432TheDispatchingStepKeyGoverns: the yaml key of a step governs
// it, above the step's own list; on the integrate lane full-gate's key
// governs full-gate alone, never collect.
func TestW432TheDispatchingStepKeyGoverns(t *testing.T) {
	cfg := &Config{Models: map[string]map[string][]string{"p": {"verification": {"pi:judge"}, "full-gate": {"pi:gate"}}}}
	if got := ComposeCandidates(shippedLane(t, "develop"), cfg, "p", nil)["verification"]; !reflect.DeepEqual(got, []string{"pi:judge"}) {
		t.Fatalf("the verify-judge's list: %v", got)
	}
	// the integrate lane's steps compose under their own keys alone: the
	// full-gate key governs full-gate, never collect (which has its own)
	in := ComposeCandidates(shippedLane(t, "integrate"), cfg, "p", nil)
	if !reflect.DeepEqual(in["full-gate"], []string{"pi:gate"}) || reflect.DeepEqual(in["collect"], []string{"pi:gate"}) {
		t.Fatalf("full-gate's key governs full-gate alone: %v", in)
	}
}

// TestW432StaleKeysLoadAndAreNamed: the retired verify-judge key, a
// removed lane's step and typos (`defaults`, `grilll`) load (no refusal)
// and are named by StaleKeys, sorted; default and task
// never are.
func TestW432StaleKeysLoadAndAreNamed(t *testing.T) {
	ws := writeYaml(t, "steps:\n  e2e:\n    default: { models: [pi:d] }\n    defaults: { models: [pi:x] }\n    task: { models: [pi:t] }\n    verify-judge: { models: [pi:judge] }\n    root-cause: { models: [pi:a] }\n    fix: { models: [pi:f] }\n  b:\n    grilll: { models: [pi:g] }\n")
	cfg, err := Load(ws, nil)
	if err != nil {
		t.Fatalf("a yaml with stale keys loads: %v", err)
	}
	stale, err := StaleKeys(cfg, workflow.LaneSet{Builtin: stepsDir(t)})
	if err != nil || strings.Join(stale, ",") != "steps.b.grilll,steps.e2e.defaults,steps.e2e.root-cause,steps.e2e.verify-judge" {
		t.Fatalf("stale keys: %v, %v", stale, err)
	}
	if BundlelessKey("verify-judge") {
		t.Fatal("verify-judge is no key of its own any more")
	}
	if !strings.Contains(StaleKeyHint, `section "`+KeyRenameSection+`"`) || !strings.Contains(StaleKeyHint, "verification") {
		t.Fatalf("the hint names the rewrite and the upgrade section: %s", StaleKeyHint)
	}
	// a stale key governs nothing: no step list carries it
	fm := shippedLane(t, "develop")
	if got := ComposeCandidates(fm, cfg, "e2e", nil)["verification"]; !reflect.DeepEqual(got, []string{"pi:d"}) {
		t.Fatalf("the verify-judge under a stale verify-judge key runs on the default: %v", got)
	}
}

// TestStaleKeyHintNamesTheKeyRenameSection: the section the
// stale-key hint names is a docs/upgrade.md heading whose body explains
// the rename the hint asks for (verify-judge → verification) — the
// key-rename section, not the current contract's UpgradeSection, which
// moves with every contract and explains no key rename.
func TestStaleKeyHintNamesTheKeyRenameSection(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/upgrade.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	at := strings.Index(doc, "\n## "+KeyRenameSection+"\n")
	if at < 0 {
		t.Fatalf("docs/upgrade.md has no section %q", KeyRenameSection)
	}
	body := doc[at+1:]
	body = body[len("## "+KeyRenameSection):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	if !strings.Contains(strings.Join(strings.Fields(body), " "), "rename `steps.<project>.verify-judge` to `steps.<project>.verification`") {
		t.Fatalf("the section %q does not explain the verify-judge rename", KeyRenameSection)
	}
	if KeyRenameSection == workflow.UpgradeSection {
		t.Fatal("the key-rename section is pinned on its own, not on the contract's upgrade section")
	}
}
