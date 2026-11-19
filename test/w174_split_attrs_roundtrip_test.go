// carries: internal/polyforge/cli/verbs_aihub.go, internal/aihub/core/workitem.go, internal/aihub/core/attrs.go

package test

// w174_split_attrs_roundtrip_test.go (polyforge#174, AC1+AC3): the
// round trip the split flow depends on — a ticket filed by the real
// `pf file create --attr split.origin=… --attr split.unit=…` is listed
// by the real `pf queue` filtered on the SAME attr pair (both must
// hold, only the matching tickets), and the create stores the dotted
// keys as the nested dot paths the filter walks — one shape, write and
// read. Authored RED on the flat-key CLI: the queue filter answers 0
// items (the reported defect — the duplicate check of the epic split
// never worked) and the stored shape carries the unreachable flat key.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

const (
	w174Project = "w174-split-attrs"
	w174Other   = "w174-split-elsewhere"
)

// w174CreateWIThMarkers files one ticket through the real CLI with the
// split markers the skill's split section writes and answers its id.
func w174CreateWIThMarkers(t *testing.T, inst *v1Install, jsonEnv w113Env, project, origin, unit string) string {
	t.Helper()
	out := w113PfWant(t, inst, -1, jsonEnv, "file", "create", "--project", project,
		"--goal", "w174 split child "+origin+"/"+unit,
		"--attr", "split.origin="+origin, "--attr", "split.unit="+unit)
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &wi); err != nil || wi.ID == "" {
		t.Fatalf("create with split markers answered no id: %v (%s)", err, out)
	}
	return wi.ID
}

// w174QueueIDs answers the ids of one `pf --json queue --project P
// --attr <path>=<value>…` run (each flag split at its first '=').
func w174QueueIDs(t *testing.T, inst *v1Install, jsonEnv w113Env, project string, attrs ...string) []string {
	t.Helper()
	args := []string{"queue", "--project", project}
	for _, a := range attrs {
		args = append(args, "--attr", a)
	}
	out := w113PfWant(t, inst, -1, jsonEnv, args...)
	var doc struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &doc); err != nil {
		t.Fatalf("queue --attr answer is not the server's list document: %v (%s)", err, out)
	}
	ids := make([]string, 0, len(doc.Items))
	for _, it := range doc.Items {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	return ids
}

// w174AttrsOf answers one ticket's attrs object through the real CLI.
func w174AttrsOf(t *testing.T, inst *v1Install, jsonEnv w113Env, id string) map[string]any {
	t.Helper()
	out := w113PfWant(t, inst, -1, jsonEnv, "file", "show", id)
	var doc struct {
		Attrs map[string]any `json:"attrs"`
	}
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &doc); err != nil {
		t.Fatalf("file show %s is not the server's record: %v (%s)", id, err, out)
	}
	return doc.Attrs
}

// TestW174SplitAttrsRoundTrip: file create's --attr pair and queue's
// --attr filter share one shape — the ticket the pair filed is the one
// (and only the ones) the same pair filters back, and the stored attrs
// carry no flat dotted key.
func TestW174SplitAttrsRoundTrip(t *testing.T) {
	inst := w140Setup(t, w174Project)
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: w174Other, Description: "w174 elsewhere",
	}); err != nil {
		t.Fatalf("create project %s: %v", w174Other, err)
	}
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	cfg := w113ConfigFile(t, filepath.Join(home, "cli.toml"), inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{w174Project, w174Other}}})
	jsonEnv := w113Env{config: cfg, json: true}

	// the ticket under test and the distractors: same origin, other
	// unit; same unit, other origin; no markers; another project with
	// the same pair.
	target := w174CreateWIThMarkers(t, inst, jsonEnv, w174Project, "wi_O", "bg-ui")
	otherUnit := w174CreateWIThMarkers(t, inst, jsonEnv, w174Project, "wi_O", "bg-rt")
	otherOrigin := w174CreateWIThMarkers(t, inst, jsonEnv, w174Project, "wi_P", "bg-ui")
	bare := w140Create(t, inst, w174Project, "w174 no markers", nil, nil)
	otherProject := w174CreateWIThMarkers(t, inst, jsonEnv, w174Other, "wi_O", "bg-ui")

	// THE round trip: the same pair that filed the ticket lists it, and
	// both conditions must hold — only the matching ticket.
	if got, want := w174QueueIDs(t, inst, jsonEnv, w174Project, "split.origin=wi_O", "split.unit=bg-ui"),
		[]string{target}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queue --attr split.origin=wi_O --attr split.unit=bg-ui listed %v, want exactly %v — the create's markers must be filterable by the same pair (polyforge#174 AC1)", got, want)
	}
	// each condition alone narrows to its own matches, never the bare
	// ticket and never another project's.
	if got, want := w174QueueIDs(t, inst, jsonEnv, w174Project, "split.origin=wi_O"),
		[]string{otherUnit, target}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queue --attr split.origin=wi_O listed %v, want %v (both wi_O children, nothing else)", got, want)
	}
	if got, want := w174QueueIDs(t, inst, jsonEnv, w174Project, "split.unit=bg-ui"),
		[]string{otherOrigin, target}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queue --attr split.unit=bg-ui listed %v, want %v (both bg-ui children, nothing else)", got, want)
	}
	if got, want := w174QueueIDs(t, inst, jsonEnv, w174Other, "split.origin=wi_O", "split.unit=bg-ui"),
		[]string{otherProject}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the other project's query listed %v, want %v — a filter never crosses projects", got, want)
	}
	// the bare ticket stays invisible to the split filters.
	for _, q := range [][]string{
		{"split.origin=wi_O"}, {"split.unit=bg-ui"}, {"split.origin=wi_P"},
	} {
		for _, id := range w174QueueIDs(t, inst, jsonEnv, w174Project, q...) {
			if id == bare {
				t.Fatalf("the ticket without markers (%s) matched %v — a split filter must not list it", bare, q)
			}
		}
	}

	// the stored shape IS the filtered shape: the dotted keys nest as
	// the dot paths the filter walks — the flat key the old CLI wrote
	// is the value no query reaches (AC3: 写入形状 = 过滤读取的形状).
	wantAttrs := map[string]any{"split": map[string]any{"origin": "wi_O", "unit": "bg-ui"}}
	if got := w174AttrsOf(t, inst, jsonEnv, target); !reflect.DeepEqual(got, wantAttrs) {
		t.Fatalf("target attrs = %v, want exactly the nested shape %v (polyforge#174 AC3: the create's shape must equal the filter's shape)", got, wantAttrs)
	}
	if got := w174AttrsOf(t, inst, jsonEnv, otherUnit); !reflect.DeepEqual(got,
		map[string]any{"split": map[string]any{"origin": "wi_O", "unit": "bg-rt"}}) {
		t.Fatalf("sibling attrs = %v, want the nested shape", got)
	}
}
