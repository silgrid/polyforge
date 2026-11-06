package stepscripts

// w473_result_schema_test.go: the published JSON Schema of
// the result file, workflows/_shared/scripts/schema/pf-step-result-1.json,
// is the Go result's shape. Every result file a step command writes in a
// test of this package (its --out, after each invocation) is validated
// against it; this file pins that each of the step commands has a
// real result file validated, that the schema names exactly Result's
// fields in their order, and that a broken result fails it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// resultSchemaPath is the published schema, from this package's directory.
var resultSchemaPath = filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "schema", "pf-step-result-1.json")

var (
	resultSchemaOnce sync.Once
	resultSchema     *jsonschema.Schema
	resultSchemaErr  error
)

// validateResult validates one result file's bytes against the schema.
func validateResult(raw []byte) error {
	resultSchemaOnce.Do(func() {
		src, err := os.ReadFile(resultSchemaPath)
		if err != nil {
			resultSchemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("pf-step-result-1.json", bytes.NewReader(src)); err != nil {
			resultSchemaErr = err
			return
		}
		resultSchema, resultSchemaErr = c.Compile("pf-step-result-1.json")
	})
	if resultSchemaErr != nil {
		return resultSchemaErr
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	return resultSchema.Validate(v)
}

// validated records, per test name, the commands whose written result
// files checkWrittenResult validated.
var (
	validatedMu sync.Mutex
	validated   = map[string]map[string]bool{}
)

// checkWrittenResult validates the result file an invocation's --out names
// (none written: nothing to check) and records its command under the
// test's name.
func checkWrittenResult(t *testing.T, args []string) {
	t.Helper()
	out := ""
	for i, a := range args {
		if a == "--out" && i+1 < len(args) {
			out = args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--out="); ok {
			out = v
		}
	}
	if out == "" {
		return
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return
	}
	if err := validateResult(raw); err != nil {
		t.Fatalf("the result file %s does not satisfy the pf-step-result/1 schema: %v\n%s", out, err, raw)
	}
	var r struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return
	}
	validatedMu.Lock()
	defer validatedMu.Unlock()
	if validated[t.Name()] == nil {
		validated[t.Name()] = map[string]bool{}
	}
	validated[t.Name()][r.Command] = true
}

// TestResultSchemaValidatesEveryCommand: one real run of each step command
// — an existing test of its own, run here as a subtest — writes a result
// file that its invocation validated against the schema.
func TestResultSchemaValidatesEveryCommand(t *testing.T) {
	runs := []struct {
		command string
		test    func(*testing.T)
	}{
		// the workflow scripts' result files, checked the same way
		{NameGate, TestGateCapsuleNothingGatedAndBarePins},
		{NameFacts, TestFactsPlanConformanceBothWays},
		{NameIntegrate, TestIntegrateCollectTakesTheNewHeads},
		{scriptSync, TestSyncMergesCleanlyAndFastForwards},
		{scriptMerge, TestMergeMergesAtTheVerifiedHead},
		{scriptRevert, TestRevertOneItemTwoReposResumable},
		{scriptRun, TestRunOneLineInTheContainerWritesTheResult},
		{scriptPublished, publishedRunsAtTheVerifiedHeads},
	}
	var covered []string
	for _, r := range runs {
		name := ""
		t.Run(r.command, func(t *testing.T) {
			name = t.Name()
			r.test(t)
		})
		validatedMu.Lock()
		ok := validated[name][r.command]
		validatedMu.Unlock()
		if !ok {
			t.Errorf("%s: no %s result file was written and validated against the schema", name, r.command)
			continue
		}
		covered = append(covered, r.command)
	}
	if want := []string{NameGate, NameFacts, NameIntegrate, scriptSync, scriptMerge, scriptRevert, scriptRun, scriptPublished}; !slices.Equal(covered, want) {
		t.Errorf("validated %v, want every step script %v", covered, want)
	}
}

// scriptPublished is published.py as its result file names itself.
const scriptPublished = "published.py"

// publishedRunsAtTheVerifiedHeads: one real run of published.py, as
// merge.py's own run is set up (every repo published at the verified
// head): exit 0, its result file validated on the way.
func publishedRunsAtTheVerifiedHeads(t *testing.T) {
	w := newGitWorld(t, "app")
	w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, _ := mergeInputs(w)
	out := filepath.Join(w.capsule, "artifacts", "publish", "record.json")
	record := filepath.Join(w.capsule, "artifacts", "publish", "integration.json")
	exit, _, errb := invokePy(t, scriptPublished, []string{"--capsule", w.capsule, "--publish", publish, "--verified", verified, "--record-out", record, "--json", "--out", out}, w.env("PF_ROUND=round-p"))
	if exit != 0 {
		t.Fatalf("published.py: exit %d\n%s", exit, errb)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("published.py wrote no record: %v", err)
	}
}

// TestResultSchemaIsTheGoShape: the schema's required list and properties
// are Result's JSON fields, in order, and admit nothing else; the verdict
// enum is the four verdicts.
func TestResultSchemaIsTheGoShape(t *testing.T) {
	raw, err := os.ReadFile(resultSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Required   []string                   `json:"required"`
		Additional *bool                      `json:"additionalProperties"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	var fields []string
	rt := reflect.TypeOf(Result{})
	for i := range rt.NumField() {
		tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		fields = append(fields, tag)
	}
	if !slices.Equal(s.Required, fields) {
		t.Errorf("schema required %v, want Result's fields %v", s.Required, fields)
	}
	if len(s.Properties) != len(fields) {
		t.Errorf("schema has %d properties, Result %d fields", len(s.Properties), len(fields))
	}
	if s.Additional == nil || *s.Additional {
		t.Errorf("the schema must refuse a key Result does not have (additionalProperties: false)")
	}
	var verdict struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(s.Properties["verdict"], &verdict); err != nil {
		t.Fatal(err)
	}
	if want := []string{VerdictPass, VerdictFail, VerdictError, VerdictPartial}; !slices.Equal(verdict.Enum, want) {
		t.Errorf("verdict enum %v, want %v", verdict.Enum, want)
	}
	var schemaTag struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(s.Properties["schema"], &schemaTag); err != nil || schemaTag.Const != Schema {
		t.Errorf("schema tag const %q, want %q", schemaTag.Const, Schema)
	}
}

// TestResultSchemaRefusesABrokenResult: a real result file passes; with any
// required field dropped, the verdict not its exit code's, an unknown key
// or a wrong type, it fails.
func TestResultSchemaRefusesABrokenResult(t *testing.T) {
	dir := t.TempDir()
	pins := gatePins(t, dir)
	out := filepath.Join(dir, "result.json")
	if exit, _, errb := invoke(t, NameGate, []string{"--capsule", dir, "--pins", pins, "--out", out}, stepEnv("PF_ROUND=r1"), newFakeRuntime(t)); exit != 0 {
		t.Fatalf("exit %d: %s", exit, errb)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateResult(raw); err != nil {
		t.Fatalf("the real result fails the schema: %v", err)
	}
	mutate := func(f func(map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	var fields []string
	rt := reflect.TypeOf(Result{})
	for i := range rt.NumField() {
		tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		fields = append(fields, tag)
	}
	for _, f := range fields {
		if validateResult(mutate(func(m map[string]any) { delete(m, f) })) == nil {
			t.Errorf("a result without %q passed the schema", f)
		}
	}
	for why, f := range map[string]func(map[string]any){
		"verdict fail at exit 0":    func(m map[string]any) { m["verdict"] = VerdictFail },
		"verdict pass at exit 2":    func(m map[string]any) { m["exit_code"] = 2 },
		"an unknown key":            func(m map[string]any) { m["extra"] = true },
		"exit code a string":        func(m map[string]any) { m["exit_code"] = "1" },
		"a sub-second timestamp":    func(m map[string]any) { m["ended_at"] = "2026-11-12T01:02:03.123Z" },
		"another schema tag":        func(m map[string]any) { m["schema"] = "pf-step-result/2" },
		"heads with a non-string":   func(m map[string]any) { m["heads"] = map[string]any{"app": 1} },
		"details an array":          func(m map[string]any) { m["details"] = []any{} },
		"an empty command":          func(m map[string]any) { m["command"] = "" },
		"logs holding a non-string": func(m map[string]any) { m["logs"] = []any{1} },
	} {
		if validateResult(mutate(f)) == nil {
			t.Errorf("%s passed the schema", why)
		}
	}
}
