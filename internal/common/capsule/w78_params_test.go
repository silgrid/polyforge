package capsule

// w78_params_test.go (polyforge#78 AC3): the overlay record carries a
// step's per-ticket param overrides — per param its value, operator and
// time, a clear kept as a tombstone with its own operator and time (an
// empty string is a legal value, so a clear is not a set to "") — and
// they survive the write and read back exactly; a step entry that holds
// only param records is an entry like any other, and one holding no
// aspect at all is still dropped.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestW78OverrideParamsRecord(t *testing.T) {
	dir := ovTestDir(t)
	const t1, t2 = "2026-11-16T10:00:00Z", "2026-11-16T11:00:00Z"
	probe := map[string]*ParamOverride{
		"image": {Value: "reg/img:2", By: "usr_1", At: t1},
		"flag":  {Value: "", By: "usr_1", At: t1},
		"old":   {Cleared: true, By: "usr_2", At: t2},
	}
	tomb := map[string]*ParamOverride{"image": {Cleared: true, By: "usr_3", At: t2}}
	in := StepOverrides{Steps: map[string]*StepOverride{
		"probe": {Params: probe},
		"tomb":  {Params: tomb},
		"bare":  {},
	}}
	if err := WriteStepOverrides(dir, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadStepOverrides(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if e := got.Steps["probe"]; e == nil || !reflect.DeepEqual(e.Params, probe) {
		t.Fatalf("the probe entry's params did not round-trip: %+v", e)
	}
	if e := got.Steps["probe"]; e.Models != nil || e.Skip != nil {
		t.Fatalf("a params-only entry grew another aspect: %+v", e)
	}
	if e := got.Steps["tomb"]; e == nil || !reflect.DeepEqual(e.Params, tomb) {
		t.Fatalf("an entry holding only a clear's tombstone must stay (its time guards the merge): %+v", e)
	}
	if _, ok := got.Steps["bare"]; ok {
		t.Fatal("an entry with no aspect must still be dropped")
	}

	// the on-disk shape: params keyed by name, each with its own by/at, a
	// clear marked cleared
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(StepOverridesJSON)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Steps map[string]map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the record does not parse: %v", err)
	}
	var params map[string]map[string]any
	if err := json.Unmarshal(doc.Steps["probe"]["params"], &params); err != nil {
		t.Fatalf("the probe entry carries no params object: %v\n%s", err, raw)
	}
	if p := params["image"]; p["value"] != "reg/img:2" || p["by"] != "usr_1" || p["at"] != t1 {
		t.Fatalf("the image record's shape: %v", p)
	}
	if p := params["old"]; p["cleared"] != true || p["by"] != "usr_2" || p["at"] != t2 {
		t.Fatalf("the clear's tombstone shape: %v", p)
	}
}
