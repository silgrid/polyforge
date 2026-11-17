package cli

// doctor_concise_prompts_test.go (polyforge#101, AC2): the `harness
// concise guidance` row — per-harness detection (pi, claude,
// step-agent) of the concise-output guidance block, the
// --fix-harness-prompts contract (append-only, idempotent,
// mode-preserving, never creating a file), and Claude Code's resolved
// context-file target (CLAUDE.md when it exists, else AGENTS.md — the
// file the harness actually loads; owner ruling 2026-11-17).

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// conciseBlock is the canonical block the row judges and the fix
// appends.
var conciseBlock = harness.ConciseGuidanceBlock()

// claudeContext answers the claude context file path under h.home for
// the named shape, creating ~/.claude (installing the harness).
func (h promptHarness) claudeContext(t *testing.T, file string) string {
	t.Helper()
	dir := filepath.Join(h.home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, file)
}

// TestPF101ConciseRowPerHarness: each harness context, alone, passes
// only with the concise block, warns `missing` when it is absent (the
// detail carrying the block and the fix pointer), and warns without
// creating the file when the file is absent.
func TestPF101ConciseRowPerHarness(t *testing.T) {
	for _, name := range []string{"pi", "claude", "step-agent"} {
		for _, tc := range []struct {
			body   string // "" = no file
			status string
			state  string
		}{
			{conciseBlock, "pass", "pass"},
			{diagramGuidanceBlock, "warn", "missing"},
			{"# context\n\nno guidance here\n", "warn", "missing"},
			{"", "warn", "no file (not created)"},
		} {
			h := newPromptHarness(t)
			path := h.contextFile(t, name, true)
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c := concisePromptCheck(h.home, h.piDir, h.root, false)
			if c.Name != docChConcisePrompts || c.Status != tc.status {
				t.Fatalf("%s %q: %s %s %q, want %s", name, tc.body, c.Name, c.Status, c.Detail, tc.status)
			}
			if !strings.Contains(c.Detail, name+" "+path+": "+tc.state) {
				t.Errorf("%s: detail does not name the file as %s: %q", name, tc.state, c.Detail)
			}
			switch {
			case tc.body == "":
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s: a missing file was created (stat err %v)", name, err)
				}
				if strings.Contains(c.Detail, "--fix-harness-prompts") {
					t.Errorf("%s: a missing file is pointed at the fix that never creates it: %q", name, c.Detail)
				}
			case tc.status == "warn":
				if !strings.Contains(c.Detail, "pf doctor --fix-harness-prompts") {
					t.Errorf("%s: warn lacks the fix pointer: %q", name, c.Detail)
				}
				if !strings.Contains(c.Detail, conciseBlock) {
					t.Errorf("%s: warn detail lacks the guidance block: %q", name, c.Detail)
				}
			}
		}
	}
	// no harness installed: skip, with the same shape as the diagram row
	h := newPromptHarness(t)
	c := concisePromptCheck(h.home, h.piDir, h.root, false)
	if c.Status != "skip" {
		t.Fatalf("no harness installed: %s %q, want skip", c.Status, c.Detail)
	}
}

// TestPF101ConciseRowFixAppendsOnceAndIsIdempotent: --fix appends the
// concise block exactly once after the untouched original bytes, keeps
// the mode, turns the row to pass, and a second run changes no byte; a
// missing file is left absent.
func TestPF101ConciseRowFixAppendsOnceAndIsIdempotent(t *testing.T) {
	h := newPromptHarness(t)
	claude := h.contextFile(t, "claude", true)
	stepAgent := h.contextFile(t, "step-agent", true)
	orig := []byte("# my rules\n\nkeep this") // no trailing newline
	if err := os.WriteFile(claude, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	c := concisePromptCheck(h.home, h.piDir, h.root, true)
	if c.Status != "warn" || !strings.Contains(c.Detail, "claude "+claude+": missing; appended") {
		t.Fatalf("fix run: %s %q", c.Status, c.Detail)
	}
	got, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	want := string(orig) + "\n\n" + conciseBlock
	if string(got) != want {
		t.Fatalf("appended file =\n%q\nwant\n%q", got, want)
	}
	if strings.Count(string(got), "<!-- polyforge:concise-guidance:begin -->") != 1 {
		t.Fatalf("block markers not exactly once: %q", got)
	}
	if st, _ := os.Stat(claude); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", st.Mode().Perm())
	}
	if _, err := os.Stat(stepAgent); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fix created the missing step-agent file (stat err %v)", err)
	}
	// second run: byte-for-byte no-op, the row passes
	c = concisePromptCheck(h.home, h.piDir, h.root, true)
	again, _ := os.ReadFile(claude)
	if !bytes.Equal(again, got) || !strings.Contains(c.Detail, "claude "+claude+": pass") {
		t.Fatalf("second fix run changed a file or did not pass: %q / %s %q", again, c.Status, c.Detail)
	}
}

// TestPF101ClaudeTargetResolution: the claude context file the rows
// judge — and the fix appends to — is ~/.claude/CLAUDE.md when it
// exists, else ~/.claude/AGENTS.md; with neither, the canonical
// CLAUDE.md path names the absence and nothing is created. Both rows
// (diagram and concise) resolve the same way.
func TestPF101ClaudeTargetResolution(t *testing.T) {
	// only AGENTS.md: the append lands there, both blocks, once each
	h := newPromptHarness(t)
	agents := h.claudeContext(t, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# owner rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name  string
		run   func(home, piDir, root string, fix bool) doctorCheck
		block string
	}{
		{"diagram", harnessPromptCheck, diagramGuidanceBlock},
		{"concise", concisePromptCheck, conciseBlock},
	} {
		c := check.run(h.home, h.piDir, h.root, false)
		if !strings.Contains(c.Detail, "claude "+agents+": missing") {
			t.Fatalf("%s row does not name the AGENTS.md target: %q", check.name, c.Detail)
		}
		if strings.Contains(c.Detail, filepath.Join(h.home, ".claude", "CLAUDE.md")) {
			t.Fatalf("%s row names a CLAUDE.md that does not exist: %q", check.name, c.Detail)
		}
		c = check.run(h.home, h.piDir, h.root, true)
		if !strings.Contains(c.Detail, "claude "+agents+": missing; appended") {
			t.Fatalf("%s fix run: %q", check.name, c.Detail)
		}
	}
	b, _ := os.ReadFile(agents)
	want := "# owner rules\n\n" + diagramGuidanceBlock + "\n" + conciseBlock
	if string(b) != want {
		t.Fatalf("AGENTS.md after both fixes =\n%q\nwant\n%q", b, want)
	}

	// both files: CLAUDE.md is the target; AGENTS.md is untouched
	h2 := newPromptHarness(t)
	claudeMd := h2.claudeContext(t, "CLAUDE.md")
	agentsMd := h2.claudeContext(t, "AGENTS.md")
	if err := os.WriteFile(claudeMd, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentsMd, []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := concisePromptCheck(h2.home, h2.piDir, h2.root, true)
	if !strings.Contains(c.Detail, "claude "+claudeMd+": missing; appended") {
		t.Fatalf("with both files the target is CLAUDE.md: %q", c.Detail)
	}
	if g, _ := os.ReadFile(agentsMd); string(g) != "# agents\n" {
		t.Fatalf("AGENTS.md was touched: %q", g)
	}

	// neither file: the canonical path names the absence, nothing created
	h3 := newPromptHarness(t)
	if err := os.MkdirAll(filepath.Join(h3.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	c = concisePromptCheck(h3.home, h3.piDir, h3.root, true)
	if c.Status != "warn" || !strings.Contains(c.Detail, "claude "+filepath.Join(h3.home, ".claude", "CLAUDE.md")+": no file (not created)") {
		t.Fatalf("with no claude context file the canonical path names the absence: %s %q", c.Status, c.Detail)
	}
	if _, err := os.Stat(filepath.Join(h3.home, ".claude", "CLAUDE.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the fix created the claude file (stat err %v)", err)
	}
}
