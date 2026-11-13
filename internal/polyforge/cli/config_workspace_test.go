package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

const workspaceReadYAML = "steps:\n  alpha:\n    grill:\n      models: [claude:declared]\n    code: { models: [] }\n    default: { models: [pi:ws-default] }\n"

// TestConfigWorkspaceReadsDeclaredChainsMemoryPolicy pins the step
// entry's additive fields: source/class of a declared chain; the moved
// cli.toml facts and the removed step attrs are not carried.
func TestConfigWorkspaceReadsDeclaredChainsMemoryPolicy(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML))
	grill := stepRead(t, doc, "alpha", "grill")
	if grill["declared"] != true || !reflect.DeepEqual(grill["models"], []any{"claude:declared"}) || grill["source"] != "file" || grill["class"] != "live" {
		t.Fatalf("grill chain read wrong: %v", grill)
	}
	if _, ok := grill["attrs"]; ok {
		t.Fatalf("a step entry carries no attrs any more: %v", grill)
	}
	for _, moved := range []string{"memory_policy", "summarize"} {
		if _, ok := doc[moved]; ok {
			t.Fatalf("config workspace still carries %s (a cli.toml fact read via config list): %v", moved, doc)
		}
	}
}

// TestConfigListReadsMemoryPolicy pins the config list memory_policy
// block per workspace path: the default's effective lists (declared, else
// built-in), each project's own declared lists, never pre-merged.
func TestConfigListReadsMemoryPolicy(t *testing.T) {
	f := newWfFixture(t, workspaceReadYAML)
	cfgPath := filepath.Join(f.env["XDG_CONFIG_HOME"], "polyforge", "cli.toml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n[memory_policy.\"" + f.ws + "\".default]\nrecord = [\"base\"]\n\n[memory_policy.\"" + f.ws + "\".alpha]\nrecord = [\"alpha-only\"]\nskip = [\"alpha-skip\"]\n"
	if err := os.WriteFile(cfgPath, append(raw, extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPf(t, f.env, "--json", "config", "list")
	if code != 0 {
		t.Fatalf("config list exited %d: %s", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("config list did not print JSON: %v\n%s", err, stdout)
	}
	block := doc["memory_policy"].(map[string]any)[f.ws].(map[string]any)
	def := block["default"].(map[string]any)
	if !reflect.DeepEqual(def["record"], []any{"base"}) || def["declared"] != true || len(def["skip"].([]any)) == 0 {
		t.Fatalf("default entry wrong (declared record, built-in skip): %v", def)
	}
	alpha := block["alpha"].(map[string]any)
	if !reflect.DeepEqual(alpha["record"], []any{"alpha-only"}) || !reflect.DeepEqual(alpha["skip"], []any{"alpha-skip"}) || alpha["declared"] != true {
		t.Fatalf("alpha entry must be its own lists: %v", alpha)
	}
	beta := block["beta"].(map[string]any)
	if !reflect.DeepEqual(beta, map[string]any{"record": []any{}, "skip": []any{}, "declared": false}) {
		t.Fatalf("beta declares nothing: %v", beta)
	}
	items := doc["items"].([]any)
	found := 0
	for _, raw := range items {
		it := raw.(map[string]any)
		if k, _ := it["key"].(string); strings.HasPrefix(k, "summarize.") {
			found++
			if it["class"] != "live" || it["source"] != "default" || it["schema"] == nil {
				t.Fatalf("summarize item wrong: %v", it)
			}
		}
	}
	if found != 3 {
		t.Fatalf("config list carries %d summarize items, want 3: %v", found, items)
	}
	if code, _, stderr := runPf(t, f.env, "config", "get", "summarize.language"); code == 0 || !strings.Contains(stderr, "pf config list") {
		t.Fatalf("config get summarize.language must refuse pointing at config list (exit %d): %s", code, stderr)
	}
}

// TestConfigWorkspaceReportsOnlyDeclaredValues pins that nothing is
// invented: an undeclaring project gets no declared value, and
// default/task rows exist.
func TestConfigWorkspaceReportsOnlyDeclaredValues(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML))
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
}

// TestConfigWorkspaceReportsDeclaredEmptyList pins "declared-empty is
// declared, never absent".
func TestConfigWorkspaceReportsDeclaredEmptyList(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, workspaceReadYAML))
	code := stepRead(t, doc, "alpha", "code")
	if code["declared"] != true || !reflect.DeepEqual(code["models"], []any{}) || code["source"] != "file" {
		t.Fatalf("declared-empty read wrong: %v", code)
	}
}

// TestConfigWorkspaceUnreachableReportsIncomplete pins the read without a
// daemon: still mapped, the file's own declarations, steps_complete false
// with a reason.
func TestConfigWorkspaceUnreachableReportsIncomplete(t *testing.T) {
	f := newWfFixture(t, workspaceReadYAML)
	f.env["PF_DAEMON_SOCKET"] = filepath.Join(t.TempDir(), "none.sock")
	doc := readWorkspaceDoc(t, f)
	if doc["mapped"] != true || doc["steps_complete"] != false || doc["steps_reason"] == "" {
		t.Fatalf("unreachable read wrong: %v", doc)
	}
	grill := stepRead(t, doc, "alpha", "grill")
	if grill["declared"] != true {
		t.Fatalf("the file's own declarations must still be listed: %v", grill)
	}
}

// TestConfigWorkspaceRefusesLegacySections pins the whole-file refusal
// a yaml still carrying `summarize:` or `memory_policy:`
// makes `config workspace` error naming the file, the line and
// `pf config migrate`; nothing is read from either section.
func TestConfigWorkspaceRefusesLegacySections(t *testing.T) {
	for _, yaml := range []string{
		"steps:\n  alpha:\n    default: { models: [pi:a] }\nsummarize:\n  models: [pi:s]\n",
		"memory_policy:\n  default: { record: [a] }\n",
	} {
		f := newWfFixture(t, yaml)
		code, _, stderr := runPf(t, f.env, "--json", "config", "workspace", "--cwd", f.ws)
		if code == 0 {
			t.Fatalf("config workspace accepted a legacy yaml:\n%s", yaml)
		}
		for _, needle := range []string{filepath.Join(f.ws, ".polyforge.yaml"), "line ", "pf config migrate"} {
			if !strings.Contains(stderr, needle) {
				t.Fatalf("the refusal lacks %q: %s", needle, stderr)
			}
		}
	}
}
