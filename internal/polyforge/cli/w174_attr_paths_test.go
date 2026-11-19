package cli

// w174_attr_paths_test.go (polyforge#174 u1): `pf file create --attr`
// builds a dotted key as the nested path it names — the same dot path
// `pf queue --attr` filters on — and refuses, order-independently, the
// creates whose paths collide (an exact repeat, even with the same
// value, and a parent assigned beside its sub-path) or carry an empty
// segment. Authored red on the flat-key CLI (the reported defect).

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// w174Answer writes the canned work-item answer.
func w174Answer(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, cannedWI)
}

// w174CreateAttrs answers the attrs object of the one recorded request.
func w174CreateAttrs(t *testing.T, rec *reqRecorder) map[string]any {
	t.Helper()
	got := rec.last()
	var body struct {
		Attrs map[string]any `json:"attrs"`
	}
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("create body is not JSON: %v (%s)", err, got.Body)
	}
	return body.Attrs
}

// TestW174FileCreateAttrPathsNest: a dotted --attr key nests, distinct
// paths sharing a prefix merge, a key without dots stays one top-level
// key, and a JSON value keeps its type (3 stays a number).
func TestW174FileCreateAttrPathsNest(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, w174Answer)
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, _, stderr := runPf(t, env,
		"file", "create", "--project", "polyforge", "--goal", "split markers",
		"--attr", "split.origin=wi_O", "--attr", "split.unit=bg-ui",
		"--attr", "team=x", "--attr", "num=3")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := map[string]any{
		"split": map[string]any{"origin": "wi_O", "unit": "bg-ui"},
		"team":  "x",
		"num":   float64(3),
	}
	if got := w174CreateAttrs(t, &rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("create attrs = %v, want the nested path shape %v (a dotted key must store as the path it names, polyforge#174 AC1)", got, want)
	}
	// the flat dotted key the old CLI wrote is the shape no query reaches
	if _, flat := w174CreateAttrs(t, &rec)["split.origin"]; flat {
		t.Fatal("create attrs carries the flat key \"split.origin\" — the list filter walks the dot path and would never match it (polyforge#174)")
	}
}

// TestW174FileCreateAttrPathConflicts: one create whose paths collide
// or carry an empty segment is refused whole, nothing is sent, and the
// refusal does not depend on flag order — every case runs as given AND
// reversed (an object VALUE beside its sub-path must refuse in both
// directions: the value object is never a container to descend into,
// polyforge#174 review r1#1).
func TestW174FileCreateAttrPathConflicts(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
	}{
		{"exact repeat, same value", []string{"a.b=1", "a.b=1"}},
		{"exact repeat, other value", []string{"a.b=1", "a.b=2"}},
		{"parent beside its sub-path", []string{"a=1", "a.b=2"}},
		{"sub-path beside its parent", []string{"a.b.c=1", "a.b=2"}},
		{"object value beside its sub-path", []string{`a={"b":"x"}`, "a.b=y"}},
		// the two below are the ones the walk used to let through in one
		// order: the JSON value object was descended into as a container
		{"object value beside another sub-path", []string{`a={"b":"x"}`, "a.c=y"}},
		{"empty object value beside its sub-path", []string{`a={}`, "a.b=1"}},
		{"split object beside its marker sibling", []string{`split={"origin":"X"}`, "split.unit=Y"}},
		{"empty inner segment", []string{"a..b=1"}},
		{"leading dot", []string{".a=1"}},
		{"trailing dot", []string{"a.=1"}},
	}
	for _, tc := range cases {
		for _, reverse := range []bool{false, true} {
			name := tc.name
			flags := append([]string{}, tc.flags...)
			if reverse {
				name += " (flags reversed)"
				for i, j := 0, len(flags)-1; i < j; i, j = i+1, j-1 {
					flags[i], flags[j] = flags[j], flags[i]
				}
			}
			t.Run(name, func(t *testing.T) {
				var rec reqRecorder
				srv := newFakeAihub(t, &rec, w174Answer)
				env := withMappingConfig(t)
				env["PF_SERVER"] = srv.URL
				env["PF_API_KEY"] = "k_cli"
				args := []string{"file", "create", "--project", "polyforge", "--goal", "conflict"}
				for _, f := range flags {
					args = append(args, "--attr", f)
				}
				code, _, stderr := runPf(t, env, args...)
				if code != 1 {
					t.Fatalf("code=%d, want 1 (stderr %s)", code, stderr)
				}
				if n := len(rec.all()); n != 0 {
					t.Fatalf("%d request(s) reached aihub — a refused create sends nothing", n)
				}
				if !strings.Contains(stderr, "--attr") {
					t.Fatalf("the refusal must name the --attr flag: %s", stderr)
				}
			})
		}
	}
	// non-conflicting paths sharing a root keep merging (the split pair).
	t.Run("sibling paths merge", func(t *testing.T) {
		var rec reqRecorder
		srv := newFakeAihub(t, &rec, w174Answer)
		env := withMappingConfig(t)
		env["PF_SERVER"] = srv.URL
		env["PF_API_KEY"] = "k_cli"
		code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "merge",
			"--attr", "split.unit=bg-ui", "--attr", "split.origin=wi_O")
		if code != 0 {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		if got := w174CreateAttrs(t, &rec); !reflect.DeepEqual(got, map[string]any{
			"split": map[string]any{"origin": "wi_O", "unit": "bg-ui"},
		}) {
			t.Fatalf("create attrs = %v, want the merged pair", got)
		}
	})
}
