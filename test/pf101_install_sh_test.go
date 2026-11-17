package test

// pf101_install_sh_test.go (polyforge#101, AC2): tools/install.sh runs
// `pf doctor --fix-harness-prompts` once per install or upgrade — the
// owner-ruled automatic append (owner 2026-11-17: install.sh may change
// the user's harness context files). The fix is idempotent and never
// creates a file, so a second install changes no byte; the claude
// target is the resolved one — a machine with only ~/.claude/AGENTS.md
// gets both guidance blocks appended there, and no CLAUDE.md is ever
// created. Hermetic like the w174 installer tests: scratch HOME, fake
// release, the real pf build behind a journaling wrapper.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// pf101JournalFixCount counts the journal's fix invocations.
func pf101JournalFixCount(t *testing.T, in *w174Install) int {
	t.Helper()
	n := 0
	for _, l := range in.journalLines() {
		if strings.Contains(l, "doctor --fix-harness-prompts") {
			n++
		}
	}
	return n
}

// TestPF101InstallRunsTheHarnessPromptFixOnce: a first install on a
// machine whose ~/.claude carries only an AGENTS.md appends both
// guidance blocks to it exactly once, creates no CLAUDE.md, and a second
// install changes no byte and runs the fix exactly once more.
func TestPF101InstallRunsTheHarnessPromptFixOnce(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-pf101")
	agents := filepath.Join(in.home, ".claude", "AGENTS.md")
	orig := "# owner rules\n\nkeep every byte of mine\n"
	if err := os.WriteFile(agents, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh: exit %d\n%s", code, out)
	}
	b, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	// the doctor's own diagram block (the harness diagram guidance row,
	// resolved to the AGENTS.md) after the original bytes, then the
	// concise block — each exactly once, the original bytes untouched
	if !strings.HasPrefix(string(b), orig+"\n<!-- polyforge:diagram-guidance:svg:begin -->") {
		t.Fatalf("the AGENTS.md after install =\n%q\nwant the diagram block appended after the original bytes", b)
	}
	if !strings.Contains(string(b), "## 输出规范：图形优先（原生 SVG）") {
		t.Fatalf("the AGENTS.md after install lacks the diagram block:\n%q", b)
	}
	if !strings.HasSuffix(string(b), harness.ConciseGuidanceBlock()) {
		t.Fatalf("the AGENTS.md after install lacks the concise block at its end:\n%q", b)
	}
	if strings.Count(string(b), "<!-- polyforge:concise-guidance:begin -->") != 1 ||
		strings.Count(string(b), "<!-- polyforge:diagram-guidance:svg:begin -->") != 1 {
		t.Fatalf("the guidance blocks are not exactly once each:\n%q", b)
	}
	if _, err := os.Stat(filepath.Join(in.home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("the install created a CLAUDE.md (stat err %v)", err)
	}
	if n := pf101JournalFixCount(t, in); n != 1 {
		t.Errorf("the journal records %d fix invocations, want 1:\n%s", n, strings.Join(in.journalLines(), "\n"))
	}

	// a second install changes no byte and runs the fix exactly once more
	again := string(b)
	out2, code := in.run(t)
	if code != 0 {
		t.Fatalf("second install.sh: exit %d\n%s", code, out2)
	}
	if b2, _ := os.ReadFile(agents); string(b2) != again {
		t.Fatalf("the second install changed the AGENTS.md:\n%q\nwant byte-identical\n%q", b2, again)
	}
	if n := pf101JournalFixCount(t, in); n != 2 {
		t.Errorf("the journal records %d fix invocations after two installs, want 2", n)
	}
}
