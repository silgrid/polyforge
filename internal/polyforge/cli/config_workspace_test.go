package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// readWorkspaceDoc runs `pf --json config workspace --cwd <ws>` and
// decodes the document.
func readWorkspaceDoc(t *testing.T, f wfFixture) map[string]any {
	t.Helper()
	code, stdout, stderr := runPf(t, f.env, "--json", "config", "workspace", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("config workspace exited %d: %s", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("config workspace did not print JSON: %v\n%s", err, stdout)
	}
	return doc
}

func stepRead(t *testing.T, doc map[string]any, project, id string) map[string]any {
	t.Helper()
	p, _ := doc["steps"].(map[string]any)[project].(map[string]any)
	e, ok := p[id].(map[string]any)
	if !ok {
		t.Fatalf("steps.%s.%s missing: %v", project, id, p)
	}
	return e
}

const workspaceReadYAML = "steps:\n  alpha:\n    grill:\n      models: [claude:declared]\n      attrs: { question_batch_size: 2 }\n    code: { models: [] }\n    default: { models: [pi:ws-default] }\nmemory_policy:\n  default: { record: [base] }\n  alpha: { record: [alpha-only], skip: [alpha-skip] }\n"

// TestConfigWorkspaceReadsDeclaredChainsAttrsMemoryPolicy pins the step
// entry's additive fields: source/class of a declared chain and a
// declared attr override with its declaration's schema.
func TestConfigWorkspaceReadsDeclaredChainsAttrsMemoryPolicy(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML, []string{CapabilityStepsAttrs}))
	grill := stepRead(t, doc, "alpha", "grill")
	if grill["declared"] != true || !reflect.DeepEqual(grill["models"], []any{"claude:declared"}) || grill["source"] != "file" || grill["class"] != "live" {
		t.Fatalf("grill chain read wrong: %v", grill)
	}
	attr := grill["attrs"].(map[string]any)["question_batch_size"].(map[string]any)
	want := map[string]any{"value": 2.0, "declared": true, "source": "file", "class": "live", "default": 0.0, "schema": map[string]any{"kind": "int"}}
	if !reflect.DeepEqual(attr, want) {
		t.Fatalf("attr read %v, want %v", attr, want)
	}
	if _, ok := doc["memory_policy"].(map[string]any); !ok {
		t.Fatalf("memory_policy missing: %v", doc)
	}
}

// TestConfigWorkspaceReadsMemoryPolicy pins the memory_policy block: the
// default's effective lists (declared, else built-in), each project's own
// declared lists, never pre-merged, and the append fact.
func TestConfigWorkspaceReadsMemoryPolicy(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML, []string{CapabilityStepsAttrs}))
	mp := doc["memory_policy"].(map[string]any)
	if mp["source"] != "file" || mp["class"] != "next-claim" || mp["append_to_default"] != true {
		t.Fatalf("memory_policy facts wrong: %v", mp)
	}
	def := mp["default"].(map[string]any)
	if !reflect.DeepEqual(def["record"], []any{"base"}) || def["declared"] != true || len(def["skip"].([]any)) == 0 {
		t.Fatalf("default entry wrong (declared record, built-in skip): %v", def)
	}
	projects := mp["projects"].(map[string]any)
	alpha := projects["alpha"].(map[string]any)
	if !reflect.DeepEqual(alpha["record"], []any{"alpha-only"}) || !reflect.DeepEqual(alpha["skip"], []any{"alpha-skip"}) || alpha["declared"] != true {
		t.Fatalf("alpha entry must be its own lists: %v", alpha)
	}
	beta := projects["beta"].(map[string]any)
	if !reflect.DeepEqual(beta, map[string]any{"record": []any{}, "skip": []any{}, "declared": false}) {
		t.Fatalf("beta declares nothing: %v", beta)
	}
}

// TestConfigWorkspaceReportsOnlyDeclaredValues pins that nothing is
// invented: an undeclaring project gets no declared value, its attrs show
// the declaration's default as undeclared, and default/task rows exist.
func TestConfigWorkspaceReportsOnlyDeclaredValues(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML, []string{CapabilityStepsAttrs}))
	beta := doc["steps"].(map[string]any)["beta"].(map[string]any)
	for id, raw := range beta {
		if e := raw.(map[string]any); e["declared"] != false || e["models"] != nil || e["source"] != "default" {
			t.Fatalf("beta.%s invented a declared value: %v", id, e)
		}
	}
	for _, id := range []string{"default", "task", "grill", "spec", "plan", "code"} {
		if _, ok := beta[id]; !ok {
			t.Fatalf("beta lacks the %s row: %v", id, beta)
		}
	}
	attr := stepRead(t, doc, "beta", "grill")["attrs"].(map[string]any)["question_batch_size"].(map[string]any)
	if attr["declared"] != false || attr["value"] != 0.0 || attr["source"] != "default" {
		t.Fatalf("an undeclared attr must show its declaration's default: %v", attr)
	}
	if _, ok := stepRead(t, doc, "beta", "default")["attrs"]; ok {
		t.Fatal("default takes no attrs")
	}
}

// TestConfigWorkspaceReportsDeclaredEmptyList pins "declared-empty is
// declared, never absent".
func TestConfigWorkspaceReportsDeclaredEmptyList(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML, []string{CapabilityStepsAttrs}))
	code := stepRead(t, doc, "alpha", "code")
	if code["declared"] != true || !reflect.DeepEqual(code["models"], []any{}) || code["source"] != "file" {
		t.Fatalf("declared-empty read wrong: %v", code)
	}
}

// TestConfigWorkspaceUnreachableReportsIncomplete pins the read without a
// daemon: still mapped, the file's own declarations, steps_complete false
// with a reason.
func TestConfigWorkspaceUnreachableReportsIncomplete(t *testing.T) {
	f := newWfFixture(t, workspaceReadYAML, nil)
	f.env["PF_DAEMON_SOCKET"] = filepath.Join(t.TempDir(), "none.sock")
	doc := readWorkspaceDoc(t, f)
	if doc["mapped"] != true || doc["steps_complete"] != false || doc["steps_reason"] == "" {
		t.Fatalf("unreachable read wrong: %v", doc)
	}
	grill := stepRead(t, doc, "alpha", "grill")
	if grill["declared"] != true {
		t.Fatalf("the file's own declarations must still be listed: %v", grill)
	}
	if v := grill["attrs"].(map[string]any)["question_batch_size"].(map[string]any)["value"]; v != 2.0 {
		t.Fatalf("the override must be listed unchecked: %v", v)
	}
}
