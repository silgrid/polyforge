package capsule

// overrides_test.go (u1): the step-overlay record's read/write shape —
// absent answers empty, torn is an error (the write path never
// overwrites state it could not read), a write round-trips every aspect
// with its operator and time, an aspect clear drops just that aspect,
// and OverlaySkips names exactly the skip-set steps.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func ovTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirRuntimeState), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStepOverridesReadAbsentAndTorn(t *testing.T) {
	dir := ovTestDir(t)
	// a capsule without the record: no overlay, no error
	ov, err := ReadStepOverrides(dir)
	if err != nil {
		t.Fatalf("absent record: %v", err)
	}
	if ov.Steps == nil || len(ov.Steps) != 0 {
		t.Fatalf("absent record answered %v, want an empty steps map", ov.Steps)
	}
	if got := OverlaySkips(ov); got != nil {
		t.Fatalf("absent record skips %v, want nil", got)
	}
	// a torn record is an error, never silently empty
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(StepOverridesJSON)), []byte(`{"steps":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStepOverrides(dir); err == nil {
		t.Fatal("a torn record must be an error: the write path must not silently overwrite state it could not read")
	}
}

func TestStepOverridesRoundTrip(t *testing.T) {
	dir := ovTestDir(t)
	no := false
	yes := true
	ov := StepOverrides{Steps: map[string]*StepOverride{
		"merge": {
			Models: []string{"claude:claude-opus-5-5:high", "pi:prov/m-a"},
			Skip:   &yes,
		},
		"review": {Models: []string{"pi:prov/m-r"}, ModelsBy: "usr_1", ModelsAt: "2026-11-15T01:02:03Z"},
		"sync":   {Skip: &no, SkipBy: "usr_2", SkipAt: "2026-11-15T02:03:04Z"},
	}}
	if err := WriteStepOverrides(dir, ov); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, err := ReadStepOverrides(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(back.Steps) != 3 {
		t.Fatalf("read back %d steps, want 3: %v", len(back.Steps), back.Steps)
	}
	m := back.Steps["merge"]
	if m == nil || !reflect.DeepEqual(m.Models, ov.Steps["merge"].Models) || m.Skip == nil || *m.Skip != true {
		t.Fatalf("merge aspect round-trip mismatch: %+v", m)
	}
	if r := back.Steps["review"]; r == nil || !reflect.DeepEqual(r.Models, ov.Steps["review"].Models) || r.ModelsBy != "usr_1" || r.ModelsAt != "2026-11-15T01:02:03Z" {
		t.Fatalf("review aspect round-trip mismatch: %+v", r)
	}
	if s := back.Steps["sync"]; s == nil || s.Skip == nil || *s.Skip != false || s.SkipBy != "usr_2" {
		t.Fatalf("sync aspect round-trip mismatch (skip false must stay distinct from absent): %+v", s)
	}
	if got := OverlaySkips(back); !reflect.DeepEqual(got, []string{"merge"}) {
		t.Fatalf("OverlaySkips = %v, want [merge]", got)
	}

	// clearing one aspect keeps the other and the entry's sibling steps:
	// a nil Models and a nil Skip drop the keys; an entry left empty is
	// removed; the file keeps the shape (a steps map, never null).
	ov.Steps["merge"] = &StepOverride{Models: nil, ModelsBy: "", ModelsAt: "", Skip: &yes, SkipBy: "usr_3", SkipAt: "t"}
	ov.Steps["sync"] = &StepOverride{}
	if err := WriteStepOverrides(dir, ov); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	back, err = ReadStepOverrides(dir)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if m := back.Steps["merge"]; m == nil || m.Models != nil || m.Skip == nil || *m.Skip != true {
		t.Fatalf("merge after clearing models: %+v (models cleared, skip kept)", m)
	}
	if _, ok := back.Steps["sync"]; ok {
		t.Fatalf("an entry with no aspect left must not stay: %+v", back.Steps["sync"])
	}
	if _, ok := back.Steps["review"]; !ok {
		t.Fatal("clearing one step's aspect must not drop other steps")
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(StepOverridesJSON)))
	if err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Steps map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil || shape.Steps == nil {
		t.Fatalf("the written file must keep the {\"steps\": {…}} shape with a non-null map: %s", raw)
	}
	// sorted ids from OverlaySkips for a stable answer over two skips
	ov2 := StepOverrides{Steps: map[string]*StepOverride{
		"b": {Skip: &yes}, "a": {Skip: &yes}, "c": {},
	}}
	if got := OverlaySkips(ov2); !reflect.DeepEqual(got, []string{"a", "b"}) {
		sort.Strings(got)
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("OverlaySkips = %v, want [a b]", got)
		}
	}
}
