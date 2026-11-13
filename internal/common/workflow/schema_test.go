package workflow

// schema_test.go: the lane schema workflows/_schema/workflow-<N>.schema.json
// is the registry's (LaneSchema regenerates it byte for byte, every key of
// the lane file carries a schema), it is the only schema shipped, every
// shipped lane points its editor at it and validates against it, and it
// refuses what the registry refuses key by key.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// shippedRoot is the shipped lanes root, from this package's directory.
const shippedRoot = "../../../workflows"

// schemaPath is the committed lane schema of ContractVersion.
var schemaPath = filepath.Join(shippedRoot, SchemaDir, SchemaFile(ContractVersion))

// TestLaneSchemaIsTheRegistry: the committed schema is what LaneSchema
// renders now (regenerate: `go generate ./internal/common/workflow`), and
// the schema directory holds it alone.
func TestLaneSchemaIsTheRegistry(t *testing.T) {
	want, err := LaneSchema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("the lane schema %s is missing (%v): run `go generate ./internal/common/workflow`", schemaPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not the registry's schema: run `go generate ./internal/common/workflow` and commit it\n--- committed\n%s\n--- registry\n%s", schemaPath, got, want)
	}
	ents, err := os.ReadDir(filepath.Join(shippedRoot, SchemaDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != SchemaFile(ContractVersion) {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("%s holds %v, want %s alone (a contract bump replaces the schema)", SchemaDir, names, SchemaFile(ContractVersion))
	}
}

// TestEveryLaneFileKeyHasASchema: no key of the lane file renders as a
// bare description (which would accept any value).
func TestEveryLaneFileKeyHasASchema(t *testing.T) {
	for set, keys := range laneFileKeySets() {
		for k, spec := range keys {
			if len(spec.Schema) == 0 {
				t.Errorf("%s key %s carries no schema", set, k)
			}
		}
	}
}

// laneSchema compiles the committed schema.
func laneSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("lane.json", bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("lane.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// yamlAsJSON decodes YAML text into the JSON value the validator reads.
func yamlAsJSON(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestShippedLanesValidateAgainstTheSchema: each of the four shipped lanes
// opens with the editor's pointer at the schema and validates against it.
func TestShippedLanesValidateAgainstTheSchema(t *testing.T) {
	s := laneSchema(t)
	pointer := "# yaml-language-server: $schema=../" + SchemaDir + "/" + SchemaFile(ContractVersion) + "\n"
	for _, lane := range []string{"develop", "micro", "integrate", "release"} {
		raw, err := os.ReadFile(filepath.Join(shippedRoot, lane, WorkflowFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(raw), pointer) {
			t.Errorf("%s: %s does not open with %q", lane, WorkflowFile, strings.TrimSpace(pointer))
		}
		if err := s.Validate(yamlAsJSON(t, string(raw))); err != nil {
			t.Errorf("%s: %s does not validate against %s: %v", lane, WorkflowFile, schemaPath, err)
		}
	}
}

// TestLaneSchemaRefusesWhatTheRegistryRefuses: one valid lane, then one
// defect of each kind the schema states — an unknown key at the lane and at
// the step, another contract, a malformed id, a hub verb outside the set, a
// lock other than merge, a tool window past the bound, a non-string var
// file, a model candidate without a harness, a missing required key.
func TestLaneSchemaRefusesWhatTheRegistryRefuses(t *testing.T) {
	s := laneSchema(t)
	cur := fmt.Sprintf("contract: %d", ContractVersion)
	ok := cur + "\nname: demo\ndescription: d\nsteps:\n  - id: work\n"
	if err := s.Validate(yamlAsJSON(t, ok)); err != nil {
		t.Fatalf("a minimal lane must validate: %v", err)
	}
	for _, bad := range []string{
		ok + "kind: x\n",
		strings.Replace(ok, cur, fmt.Sprintf("contract: %d", ContractVersion-1), 1),
		strings.Replace(ok, "id: work", "id: Work", 1),
		ok + "    hub: { verbs: [push] }\n",
		ok + "    hub: { attrs: [Integration] }\n",
		ok + "    hub: [get]\n",
		ok + "    resources: { locks: [bar] }\n",
		ok + "    budget: { tool_minutes: 181 }\n",
		ok + "    vars_files: { note: [a] }\n",
		ok + "    model_candidates: [model-a]\n",
		ok + "    kind: judge\n",
		strings.Replace(ok, "name: demo\n", "", 1),
	} {
		if err := s.Validate(yamlAsJSON(t, bad)); err == nil {
			t.Errorf("the schema accepts a lane the registry refuses:\n%s", bad)
		}
	}
}
