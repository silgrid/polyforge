package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctor_harness_prompts_test.go: the `harness diagram guidance` row —
// per-harness detection (pi, claude, step-agent): only the native-SVG
// block passes, legacy mermaid-only guidance (the old marked block or the
// owner's unmarked section) warns as legacy, and the --fix-harness-prompts
// contract: append-only, idempotent, mode-preserving, never creating a
// file, never rewriting a legacy section.

// ownerPiSection is the shape of an existing, unmarked legacy guidance
// section (a heading naming 图形优先 plus mermaid in the body).
const ownerPiSection = "# notes\n\nsome rule.\n\n## 输出规范：图形优先（owner 裁定）\n\n分析时**优先用 mermaid 图表达**；pi-web 同样支持 ```svg 代码块直接渲染。\n"

// legacyMarkedBlock is the mermaid-first block an earlier fix appended.
const legacyMarkedBlock = "<!-- polyforge:diagram-guidance:begin -->\n## 输出规范：图形优先\n\n分析系统/架构/流程/数据结构时，**优先用 mermaid 图表达**。\n<!-- polyforge:diagram-guidance:end -->\n"

// promptHarness lays out one harness root under a temp home: pi agent
// dir, ~/.claude and <harness-root>/step-agent, answering the paths.
type promptHarness struct{ home, piDir, root string }

func newPromptHarness(t *testing.T) promptHarness {
	t.Helper()
	home := t.TempDir()
	return promptHarness{home: home, piDir: filepath.Join(home, ".pi", "agent"), root: filepath.Join(home, "data")}
}

// contextFile answers the context file of the named harness, creating
// its dir (installing the harness) when install is true.
func (h promptHarness) contextFile(t *testing.T, name string, install bool) string {
	t.Helper()
	var dir, file string
	switch name {
	case "pi":
		dir, file = h.piDir, "AGENTS.md"
	case "claude":
		dir, file = filepath.Join(h.home, ".claude"), "CLAUDE.md"
	case "step-agent":
		dir, file = filepath.Join(h.root, "step-agent"), "AGENTS.md"
	}
	if install {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, file)
}

func (h promptHarness) check(fix bool) doctorCheck {
	return harnessPromptCheck(h.home, h.piDir, h.root, fix)
}

func TestHasDiagramGuidance(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		pass, legacy bool
	}{
		// the SVG block names mermaid as history, so the legacy test
		// matches it too; it is only consulted when the SVG block is absent
		{"SVG block", "x\n\n" + diagramGuidanceBlock, true, true},
		{"legacy marked block", legacyMarkedBlock, false, true},
		{"owner unmarked mermaid section", ownerPiSection, false, true},
		{"english heading + mermaid", "## Diagram output\nuse mermaid blocks\n", false, true},
		{"english heading + svg only", "## Diagram output\nuse ```svg blocks\n", false, false},
		{"heading without mermaid", "## 图形优先\n画图。\n", false, false},
		{"mermaid without a heading", "use mermaid when it helps\n", false, false},
		{"keyword not in a heading", "# rules\nprefer a diagram, mermaid\n", false, false},
		{"empty", "", false, false},
	} {
		if got := hasDiagramGuidance(tc.body); got != tc.pass {
			t.Errorf("%s: hasDiagramGuidance = %v, want %v", tc.name, got, tc.pass)
		}
		if got := hasLegacyDiagramGuidance(tc.body); got != tc.legacy {
			t.Errorf("%s: hasLegacyDiagramGuidance = %v, want %v", tc.name, got, tc.legacy)
		}
	}
}

// TestDiagramGuidanceBlockClauses pins the SVG block: new markers, the
// SVG heading, and one keyword per clause (SVG mandate, mermaid as
// history only, proactive timing, compactness, quality baseline).
func TestDiagramGuidanceBlockClauses(t *testing.T) {
	if diagramGuidanceBegin == legacyDiagramGuidanceBegin {
		t.Fatal("the SVG block must carry a marker distinct from the legacy one")
	}
	if !strings.HasPrefix(diagramGuidanceBlock, "<!-- polyforge:diagram-guidance:svg:begin -->\n") ||
		!strings.HasSuffix(diagramGuidanceBlock, "<!-- polyforge:diagram-guidance:svg:end -->\n") {
		t.Fatalf("block markers: %q", diagramGuidanceBlock)
	}
	for _, want := range []string{
		"## 输出规范：图形优先（原生 SVG）",
		"```svg", "新图一律 SVG", // SVG mandate
		"仅作历史兼容",              // mermaid = history only
		"即使未被要求也配图", "≥3 个主体", // proactive timing
		"≤2 行", "≤8 个元素", // compactness
		"viewBox", "不溢出", "不硬编码亮色", "间距均匀", // quality baseline
	} {
		if !strings.Contains(diagramGuidanceBlock, want) {
			t.Errorf("the SVG block lacks %q", want)
		}
	}
	if strings.Contains(diagramGuidanceBlock, "优先用 mermaid") {
		t.Errorf("the SVG block still prefers mermaid: %q", diagramGuidanceBlock)
	}
	if !hasDiagramGuidance(diagramGuidanceBlock) {
		t.Errorf("the SVG block does not pass its own check")
	}
}

// TestHarnessPromptCheckPerHarness: each harness context, alone, passes
// only with the SVG block, warns `legacy` with the old marked block or the
// owner's unmarked mermaid section, warns `missing` when the guidance is
// absent (both with the block and the fix pointer), and warns without
// creating the file when the file is absent.
func TestHarnessPromptCheckPerHarness(t *testing.T) {
	for _, name := range []string{"pi", "claude", "step-agent"} {
		for _, tc := range []struct {
			body   string // "" = no file
			status string
			state  string
		}{
			{diagramGuidanceBlock, "pass", "pass"},
			{legacyMarkedBlock, "warn", "legacy"},
			{ownerPiSection, "warn", "legacy"},
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
			c := h.check(false)
			if c.Name != docChHarnessPrompts || c.Status != tc.status {
				t.Fatalf("%s %q: %s %s %q, want %s", name, tc.body, c.Name, c.Status, c.Detail, tc.status)
			}
			if !strings.Contains(c.Detail, name+" "+path+": "+tc.state) {
				t.Errorf("%s: detail does not name the file as %s: %q", name, tc.state, c.Detail)
			}
			if c.Status == "warn" && !strings.Contains(c.Detail, diagramGuidanceBlock) {
				t.Errorf("%s: warn detail lacks the guidance block: %q", name, c.Detail)
			}
			switch {
			case tc.body == "":
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s: a missing file was created (stat err %v)", name, err)
				}
				if strings.Contains(c.Detail, "--fix-harness-prompts") {
					t.Errorf("%s: a missing file is pointed at the fix that never creates it: %q", name, c.Detail)
				}
				if got := strings.Contains(c.Detail, "does not persist"); got != (name == "step-agent") {
					t.Errorf("%s: step-agent prune caveat present = %v: %q", name, got, c.Detail)
				}
			case tc.status == "warn":
				if !strings.Contains(c.Detail, "pf doctor --fix-harness-prompts") {
					t.Errorf("%s: warn lacks the fix pointer: %q", name, c.Detail)
				}
				if got := strings.Contains(c.Detail, "mermaid-only guidance"); got != (tc.state == "legacy") {
					t.Errorf("%s: legacy explanation present = %v: %q", name, got, c.Detail)
				}
			}
		}
	}
}

// TestHarnessPromptCheckAbsentHarnessNotReported: a harness whose dir is
// absent is left out; with none installed the row is skip.
func TestHarnessPromptCheckAbsentHarnessNotReported(t *testing.T) {
	h := newPromptHarness(t)
	if c := h.check(true); c.Status != "skip" {
		t.Fatalf("no harness installed: %s %q, want skip", c.Status, c.Detail)
	}
	path := h.contextFile(t, "claude", true)
	if err := os.WriteFile(path, []byte(diagramGuidanceBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	c := h.check(false)
	if c.Status != "pass" || strings.Contains(c.Detail, "pi ") || strings.Contains(c.Detail, "step-agent") {
		t.Fatalf("only claude installed: %s %q", c.Status, c.Detail)
	}
	for _, d := range []string{h.piDir, filepath.Join(h.root, "step-agent")} {
		if _, err := os.Stat(d); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("an absent harness dir %s was created", d)
		}
	}
}

// TestHarnessPromptFixAppendsOnceAndIsIdempotent: --fix appends the SVG
// block exactly once after the untouched original bytes — a plain file
// and a file with the owner's unmarked legacy section alike — keeps the
// mode, turns the row to pass, and a second run changes no byte; a
// missing file is left absent.
func TestHarnessPromptFixAppendsOnceAndIsIdempotent(t *testing.T) {
	h := newPromptHarness(t)
	claude := h.contextFile(t, "claude", true)
	pi := h.contextFile(t, "pi", true)
	stepAgent := h.contextFile(t, "step-agent", true)
	orig := []byte("# my rules\n\nkeep this") // no trailing newline
	if err := os.WriteFile(claude, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi, []byte(ownerPiSection), 0o644); err != nil {
		t.Fatal(err)
	}

	c := h.check(true)
	if c.Status != "warn" || !strings.Contains(c.Detail, "claude "+claude+": missing; appended") || !strings.Contains(c.Detail, "pi "+pi+": legacy; appended") {
		t.Fatalf("fix run: %s %q (want warn only for the missing step-agent file)", c.Status, c.Detail)
	}
	got, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	want := string(orig) + "\n\n" + diagramGuidanceBlock
	if string(got) != want {
		t.Fatalf("appended file =\n%q\nwant\n%q", got, want)
	}
	if strings.Count(string(got), diagramGuidanceBegin) != 1 || strings.Count(string(got), diagramGuidanceEnd) != 1 {
		t.Fatalf("block markers not exactly once: %q", got)
	}
	if st, _ := os.Stat(claude); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(pi); string(b) != ownerPiSection+"\n"+diagramGuidanceBlock {
		t.Errorf("the owner's legacy section was rewritten, or the SVG block not appended after it: %q", b)
	}
	if _, err := os.Stat(stepAgent); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fix created the missing step-agent file (stat err %v)", err)
	}

	// second run: byte-for-byte no-op
	c = h.check(true)
	again, _ := os.ReadFile(claude)
	piAgain, _ := os.ReadFile(pi)
	if !bytes.Equal(again, got) || string(piAgain) != ownerPiSection+"\n"+diagramGuidanceBlock ||
		!strings.Contains(c.Detail, "claude "+claude+": pass") || !strings.Contains(c.Detail, "pi "+pi+": pass") {
		t.Fatalf("second fix run changed a file or did not pass: %q / %s %q", again, c.Status, c.Detail)
	}
	if wrote, err := appendDiagramGuidance(claude); wrote || err != nil {
		t.Fatalf("appendDiagramGuidance on a carrying file: wrote=%v err=%v", wrote, err)
	}
}

// TestHarnessPromptFixLegacyMarkedBlock: the fix appends the SVG block
// after a legacy marked block, never touching it; both marker pairs then
// occur exactly once, and a second run changes no byte.
func TestHarnessPromptFixLegacyMarkedBlock(t *testing.T) {
	h := newPromptHarness(t)
	claude := h.contextFile(t, "claude", true)
	orig := "# mine\n\n" + legacyMarkedBlock
	if err := os.WriteFile(claude, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	c := h.check(true)
	if c.Status != "pass" || !strings.Contains(c.Detail, "claude "+claude+": legacy; appended") {
		t.Fatalf("fix run: %s %q", c.Status, c.Detail)
	}
	got, _ := os.ReadFile(claude)
	if string(got) != orig+"\n"+diagramGuidanceBlock {
		t.Fatalf("fixed file = %q", got)
	}
	for _, m := range []string{legacyDiagramGuidanceBegin, "<!-- polyforge:diagram-guidance:end -->", diagramGuidanceBegin, diagramGuidanceEnd} {
		if n := strings.Count(string(got), m); n != 1 {
			t.Errorf("marker %s occurs %d times, want 1", m, n)
		}
	}
	h.check(true)
	if again, _ := os.ReadFile(claude); !bytes.Equal(again, got) {
		t.Fatalf("second fix run changed the file: %q", again)
	}
}

// TestAppendDiagramGuidanceSeparators: the block is separated by one
// blank line whatever the file's tail, and the result is recognized.
func TestAppendDiagramGuidanceSeparators(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"", diagramGuidanceBlock},
		{"a", "a\n\n" + diagramGuidanceBlock},
		{"a\n", "a\n\n" + diagramGuidanceBlock},
		{"a\n\n", "a\n\n" + diagramGuidanceBlock},
	} {
		p := filepath.Join(t.TempDir(), "AGENTS.md")
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if wrote, err := appendDiagramGuidance(p); !wrote || err != nil {
			t.Fatalf("%q: wrote=%v err=%v", tc.body, wrote, err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != tc.want || !hasDiagramGuidance(string(got)) {
			t.Errorf("%q: got %q, want %q", tc.body, got, tc.want)
		}
	}
	if wrote, err := appendDiagramGuidance(filepath.Join(t.TempDir(), "missing.md")); wrote || err == nil {
		t.Errorf("a missing file: wrote=%v err=%v, want an error and no write", wrote, err)
	}
}

// TestAppendDiagramGuidanceKeepsSymlink: a symlinked context file is
// appended at its target; the link stays a link.
func TestAppendDiagramGuidanceKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared.md")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if wrote, err := appendDiagramGuidance(link); !wrote || err != nil {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a regular file (err %v)", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "x\n\n"+diagramGuidanceBlock {
		t.Fatalf("target = %q", b)
	}
}

// TestHarnessPromptFixFailureIsWarn: a failed append is a warn naming
// the error, never a fail (needs a non-root user: root ignores modes).
func TestHarnessPromptFixFailureIsWarn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	h := newPromptHarness(t)
	claude := h.contextFile(t, "claude", true)
	if err := os.WriteFile(claude, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(claude)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	c := h.check(true)
	if c.Status != "warn" || !strings.Contains(c.Detail, "append failed") {
		t.Fatalf("%s %q, want warn naming the failed append", c.Status, c.Detail)
	}
	if b, _ := os.ReadFile(claude); string(b) != "x\n" {
		t.Fatalf("a failed append changed the file: %q", b)
	}
}

// TestDoctorFixHarnessPromptsFlag: the row runs in the full report and
// --fix-harness-prompts appends through it; combined with --harness the
// flag is refused before anything runs.
func TestDoctorFixHarnessPromptsFlag(t *testing.T) {
	env := withMappingConfig(t)
	home := t.TempDir()
	env["HOME"] = home
	env["PI_CODING_AGENT_DIR"] = ""
	env["POLYFORGE_DATA_DIR"] = filepath.Join(home, "data")
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runPf(t, env, "doctor", "--harness", "--fix-harness-prompts")
	if code == 0 || !strings.Contains(stderr, "--fix-harness-prompts cannot be combined with --harness") {
		t.Fatalf("--harness --fix-harness-prompts: exit %d stderr %q, want a refusal", code, stderr)
	}
	if b, _ := os.ReadFile(claude); string(b) != "# mine\n" {
		t.Fatalf("the refused run wrote the file: %q", b)
	}

	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChHarnessPrompts); st != "warn" || !strings.Contains(detail, "claude "+claude+": missing") {
		t.Fatalf("report run: %s %q", st, detail)
	}
	if b, _ := os.ReadFile(claude); string(b) != "# mine\n" {
		t.Fatalf("the report-only run wrote the file: %q", b)
	}

	_, _, doc = doctorStatus(t, env, "--json", "doctor", "--fix-harness-prompts")
	if st, detail := doctorFind(t, doc, docChHarnessPrompts); st != "pass" || !strings.Contains(detail, "claude "+claude+": missing; appended") {
		t.Fatalf("fix run: %s %q", st, detail)
	}
	if b, _ := os.ReadFile(claude); string(b) != "# mine\n\n"+diagramGuidanceBlock {
		t.Fatalf("fixed file = %q", b)
	}
}
