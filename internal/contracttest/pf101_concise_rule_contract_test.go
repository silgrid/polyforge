// carries: internal/common/harness/concise.go, internal/polyforge/driver/spawn.go, internal/polyforge/task/manager.go, internal/polyforge/daemon/analysis_queue.go, internal/polyforge/cli/doctor_concise_prompts.go

package test

// pf101_concise_rule_contract_test.go (polyforge#101, AC1): the concise
// output-discipline rule has exactly one source. The doctor block
// (harness.ConciseGuidanceBlock — what `pf doctor --fix-harness-prompts`
// appends to a harness context file) wraps the rule bytes exactly; the
// rule is one short English section (its heading, five to eight bullet
// lines), carries no project or component name (it ships with the
// binary and serves any project), and its heading line appears in
// exactly one Go source — a second copy anywhere under internal/ or
// cmd/ is the drift this test exists to catch. The step prompt and the
// three task prompts embed harness.ConciseRule's exact bytes; their
// per-surface occurrences are pinned by the driver, task and daemon
// package tests, and the source-level reference pins live here
// (TestPF101ConciseRuleReferencedByEveryPromptSite).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// pf101OneLineHead answers s's first line ("" when none).
func pf101FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestPF101ConciseRuleSourceContract pins the one-source rule itself.
func TestPF101ConciseRuleSourceContract(t *testing.T) {
	rule := harness.ConciseRule()
	if strings.TrimSpace(rule) == "" {
		t.Fatalf("ConciseRule is empty — the one source is not implemented")
	}
	block := harness.ConciseGuidanceBlock()

	// The doctor block is the markers wrapped exactly around the rule:
	// begin + "\n" + rule + end + "\n", byte for byte (the same shape as
	// the diagram guidance block).
	const begin = "<!-- polyforge:concise-guidance:begin -->"
	const end = "<!-- polyforge:concise-guidance:end -->"
	if !strings.HasPrefix(block, begin+"\n") || !strings.HasSuffix(block, end+"\n") {
		t.Fatalf("the guidance block is not the markers wrapped around the rule:\n%q", block)
	}
	if middle := strings.TrimSuffix(strings.TrimPrefix(block, begin+"\n"), end+"\n"); middle != rule {
		t.Fatalf("the guidance block's rule bytes differ from ConciseRule:\nblock middle: %q\nrule:        %q", middle, rule)
	}
	if !harness.HasConciseGuidance(block) || harness.HasConciseGuidance("") {
		t.Fatalf("HasConciseGuidance must recognize the block and an empty body's lack of it (block=%v empty=%v)",
			harness.HasConciseGuidance(block), harness.HasConciseGuidance(""))
	}

	// One short English section: its `## ` heading, five to eight
	// bullet lines, a blank line after the heading, no CJK ideograph.
	lines := strings.Split(strings.TrimSuffix(rule, "\n"), "\n")
	if len(lines) < 3 || lines[0] == "" || !strings.HasPrefix(lines[0], "## ") || lines[1] != "" {
		t.Fatalf("the rule must open with a `## ` heading and a blank line:\n%q", rule)
	}
	bullets := 0
	for _, l := range lines[2:] {
		switch {
		case strings.HasPrefix(l, "- "):
			bullets++
		case strings.TrimSpace(l) != "":
			t.Fatalf("a rule line is neither a bullet nor blank: %q", l)
		}
	}
	if bullets < 5 || bullets > 8 {
		t.Errorf("the rule carries %d bullet lines, want 5–8 (a short rule)\n%s", bullets, rule)
	}
	for _, r := range rule {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("the rule is the one English source; a CJK ideograph appears in it:\n%q", rule)
		}
	}

	// Project-neutral: no project or component name (the w72 list) in
	// the rule text. The block's markers carry the `polyforge:` prefix by
	// the diagram-block precedent (the rollback namespace), so the ban
	// reads the rule, not the block.
	for _, banned := range []string{"polyforge", "aihub", "pi-web", "webui"} {
		if strings.Contains(strings.ToLower(rule), banned) {
			t.Errorf("the rule names the project/component %q — it ships with the binary and serves any project", banned)
		}
	}

	// One source: the rule's heading line occurs in exactly one Go file
	// under internal/ and cmd/ — and that file is the source itself.
	heading := pf101FirstLine(rule)
	var where []string
	err := filepath.WalkDir(filepath.Join(v1Root(t), "internal"), func(p string, d fs.DirEntry, err error) error {
		return pf101WalkGo(t, p, d, err, heading, &where)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(filepath.Join(v1Root(t), "cmd"), func(p string, d fs.DirEntry, err error) error {
		return pf101WalkGo(t, p, d, err, heading, &where)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(where) != 1 || filepath.ToSlash(where[0]) != "internal/common/harness/concise.go" {
		t.Errorf("the rule's heading must occur in exactly one Go source, internal/common/harness/concise.go; found %v", where)
	}
}

// pf101WalkGo records rel when the .go file at p contains the heading.
func pf101WalkGo(t *testing.T, p string, d fs.DirEntry, err error, heading string, where *[]string) error {
	t.Helper()
	if err != nil || d.IsDir() || filepath.Ext(p) != ".go" {
		return err
	}
	raw, rerr := os.ReadFile(p)
	if rerr != nil {
		return rerr
	}
	if strings.Contains(string(raw), heading) {
		rel, _ := filepath.Rel(v1Root(t), p)
		*where = append(*where, filepath.ToSlash(rel))
	}
	return nil
}

// TestPF101ConciseRuleReferencedByEveryPromptSite: every rendering site
// composes the prompt from the one source — the driver's step-prompt
// assembly, the task manager's dispatched prompt and the daemon's
// ticket-analysis prompt reference harness.ConciseRule, and the
// doctor's concise row appends harness.ConciseGuidanceBlock — no site
// keeps a copy of its own.
func TestPF101ConciseRuleReferencedByEveryPromptSite(t *testing.T) {
	root := v1Root(t)
	for rel, want := range map[string]string{
		filepath.Join("internal", "polyforge", "driver", "spawn.go"):               "harness.ConciseRule()",
		filepath.Join("internal", "polyforge", "task", "manager.go"):               "harness.ConciseRule()",
		filepath.Join("internal", "polyforge", "daemon", "analysis_queue.go"):      "harness.ConciseRule()",
		filepath.Join("internal", "polyforge", "cli", "doctor_concise_prompts.go"): "harness.ConciseGuidanceBlock()",
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s must render from the one source (%s), not keep a copy of its own", rel, want)
		}
	}
}
