package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// doctor_harness_prompts.go: the `harness diagram guidance` row of
// `pf doctor`. It reports, per installed harness, whether the user
// context file the harness loads carries the native-SVG diagram
// guidance block (pi-web renders ```svg blocks as SVG DOM). It never
// fails and never moves the exit code: pass = every installed harness
// carries the SVG block, warn = some does not (legacy mermaid-only
// guidance or none at all; the detail carries the block to add), skip =
// no harness is installed here. The report itself writes nothing; only
// the explicit `--fix-harness-prompts` appends the block — to an
// EXISTING, readable context file, never creating one, never rewriting a
// byte already there (a legacy section stays as it is).

const docChHarnessPrompts = "harness diagram guidance"

// The SVG guidance block's markers: the idempotency and rollback anchors.
const (
	diagramGuidanceBegin = "<!-- polyforge:diagram-guidance:svg:begin -->"
	diagramGuidanceEnd   = "<!-- polyforge:diagram-guidance:svg:end -->"
)

// legacyDiagramGuidanceBegin marks the mermaid-first block an earlier
// fix appended. It is only reported (legacy warn), never honored as the
// guidance; the warning is migration guidance, deletable once no
// installed context file carries a legacy section.
const legacyDiagramGuidanceBegin = "<!-- polyforge:diagram-guidance:begin -->"

// diagramGuidanceBlock is the one canonical block appended to every
// context file that lacks it.
const diagramGuidanceBlock = diagramGuidanceBegin + "\n" +
	"## 输出规范：图形优先（原生 SVG）\n\n" +
	"- 图形语言：分析系统/架构/流程/数据结构时用 ```svg 代码块画原生 SVG（pi-web 直接渲染 SVG DOM，样式随面板主题），配简短文字说明；新图一律 SVG。\n" +
	"- 旧 mermaid 图仍可渲染，仅作历史兼容，不再用于新图。\n" +
	"- 时机（主动画）：讨论涉及 ≥3 个主体/组件、多阶段流程、职责或数据交接、状态转换时，即使未被要求也配图；被追问「为什么不画图」视为失误。\n" +
	"- 紧凑：元素文本 ≤2 行，单图 ≤8 个元素、承载一个结构，细节写文字。\n" +
	"- 品质基线：viewBox 定尺寸，文本不溢出元素框，配色用中性色/面板主题色（不硬编码亮色），元素间距均匀。\n" +
	diagramGuidanceEnd + "\n"

// diagramHeadingWords name an unmarked (legacy) guidance section.
var diagramHeadingWords = []string{"图形优先", "图形输出", "diagram", "graphic"}

// hasDiagramGuidance reports whether a context file carries the SVG
// guidance block (its begin marker) — the only pass.
func hasDiagramGuidance(body string) bool {
	return strings.Contains(body, diagramGuidanceBegin)
}

// hasLegacyDiagramGuidance reports whether a context file lacking the
// SVG block carries legacy mermaid-only guidance: the legacy begin
// marker, or a markdown heading naming one of diagramHeadingWords plus
// `mermaid` somewhere in the file.
func hasLegacyDiagramGuidance(body string) bool {
	if strings.Contains(body, legacyDiagramGuidanceBegin) {
		return true
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "mermaid") {
		return false
	}
	for _, line := range strings.Split(lower, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, w := range diagramHeadingWords {
			if strings.Contains(line, w) {
				return true
			}
		}
	}
	return false
}

// appendGuidanceBlock appends block to the existing file at path unless
// has already reports the guidance carried, keeping every existing byte
// and the file's mode; it answers whether it wrote. A symlinked file is
// written at its target. The write is atomic, so an interrupted run
// leaves the old file intact; a second run is a no-op.
func appendGuidanceBlock(path string, block string, has func(string) bool) (bool, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	st, err := os.Stat(target)
	if err != nil {
		return false, err
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return false, err
	}
	if has(string(body)) {
		return false, nil
	}
	sep := ""
	switch {
	case len(body) == 0:
	case !strings.HasSuffix(string(body), "\n"):
		sep = "\n\n"
	case !strings.HasSuffix(string(body), "\n\n"):
		sep = "\n"
	}
	data := append(append(body, sep...), block...)
	if err := atomicfile.Write(target, data, st.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// appendDiagramGuidance appends diagramGuidanceBlock to the existing
// file at path unless it already carries the SVG block.
func appendDiagramGuidance(path string) (bool, error) {
	return appendGuidanceBlock(path, diagramGuidanceBlock, hasDiagramGuidance)
}

// claudeContextFiles are Claude Code's user context files, in the order
// the doctor resolves the claude target (polyforge#101, owner ruling
// 2026-11-17): the CLAUDE.md Claude Code reads natively, else the
// AGENTS.md its built-in cc-plugin-agents-md plugin loads where no
// CLAUDE.md exists — the file the harness actually loads, and the one
// the fix appends to.
var claudeContextFiles = []string{"CLAUDE.md", "AGENTS.md"}

// harnessPromptTarget is one installed harness's context file the
// doctor judges.
type harnessPromptTarget struct{ name, dir, file string }

// harnessPromptTargets answers the context file of every installed
// harness: pi (piDir/AGENTS.md), Claude Code (~/.claude, resolved
// through claudeContextFiles) and the daemon's step agent
// (<harnessRoot>/step-agent/AGENTS.md). A harness whose directory is
// absent is not installed and is left out. With neither claude file,
// the canonical CLAUDE.md names the absence.
func harnessPromptTargets(home, piDir, harnessRoot string) []harnessPromptTarget {
	candidates := []struct{ name, dir, file string }{
		{"pi", piDir, "AGENTS.md"},
		{"claude", filepath.Join(home, ".claude"), claudeContextFiles[0]},
		{"step-agent", filepath.Join(harnessRoot, "step-agent"), "AGENTS.md"},
	}
	var out []harnessPromptTarget
	for _, t := range candidates {
		if st, err := os.Stat(t.dir); err != nil || !st.IsDir() {
			continue
		}
		file := t.file
		if t.name == "claude" {
			for _, f := range claudeContextFiles {
				if _, err := os.Stat(filepath.Join(t.dir, f)); err == nil {
					file = f
					break
				}
			}
		}
		out = append(out, harnessPromptTarget{t.name, t.dir, file})
	}
	return out
}

// noHarnessInstalledDetail is the two harness-guidance rows' shared
// skip detail.
const noHarnessInstalledDetail = "no harness installed here (no pi agent dir, ~/.claude or step-agent dir)"

// stepAgentNoFileCaveat is the sentence both rows append when the
// step-agent dir carries no context file.
const stepAgentNoFileCaveat = "the daemon's step-agent dir deliberately carries no AGENTS.md and prunes entries outside its minimal set at daemon start, so a hand-made file there does not persist"

// harnessPromptContext answers the home, pi agent dir and harness root
// the harness-guidance rows judge (the daemon's harness_root, else the
// data dir).
func harnessPromptContext(r *doctorRun) (home, piDir, root string) {
	home, _ = os.UserHomeDir()
	piDir = piAgentDir(home)
	root = r.cfg.Daemon.HarnessRoot
	if root == "" {
		root = DataDir(home)
	}
	return
}

// harnessPromptCheck judges the context file of every installed harness:
// pi (piDir/AGENTS.md), Claude Code (resolved through
// claudeContextFiles) and the daemon's step agent
// (<harnessRoot>/step-agent/AGENTS.md). A harness whose directory is
// absent is not installed and is left out. With fix, an existing file
// lacking the SVG block (legacy or missing) gets it appended after its
// existing bytes first.
func harnessPromptCheck(home, piDir, harnessRoot string, fix bool) doctorCheck {
	c := doctorCheck{Name: docChHarnessPrompts}
	var rows []string
	warn, fixable, legacy, stepAgentMissing := false, false, false, false
	for _, t := range harnessPromptTargets(home, piDir, harnessRoot) {
		path := filepath.Join(t.dir, t.file)
		body, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			warn = true
			if t.name == "step-agent" {
				stepAgentMissing = true
			}
			rows = append(rows, fmt.Sprintf("%s %s: no file (not created)", t.name, path))
		case err != nil:
			warn = true
			rows = append(rows, fmt.Sprintf("%s %s: unreadable (%v)", t.name, path, err))
		case hasDiagramGuidance(string(body)):
			rows = append(rows, fmt.Sprintf("%s %s: pass", t.name, path))
		default:
			state := "missing"
			if hasLegacyDiagramGuidance(string(body)) {
				state, legacy = "legacy", true
			}
			if !fix {
				warn, fixable = true, true
				rows = append(rows, fmt.Sprintf("%s %s: %s", t.name, path, state))
			} else if _, aerr := appendDiagramGuidance(path); aerr != nil {
				warn = true
				rows = append(rows, fmt.Sprintf("%s %s: %s; append failed (%v)", t.name, path, state, aerr))
			} else {
				rows = append(rows, fmt.Sprintf("%s %s: %s; appended", t.name, path, state))
			}
		}
	}
	if len(rows) == 0 {
		c.Status, c.Detail = "skip", noHarnessInstalledDetail
		return c
	}
	c.Status, c.Detail = "pass", strings.Join(rows, "; ")
	if !warn {
		return c
	}
	c.Status = "warn"
	if legacy && fixable {
		c.Detail += " — legacy = mermaid-only guidance from before the native-SVG rule; it no longer counts"
	}
	if fixable {
		c.Detail += " — run `pf doctor --fix-harness-prompts` to append the SVG block below after the existing bytes of the file(s) (append-only, idempotent; a legacy section is never rewritten — delete it by hand after reading it; a missing file is never created)"
	}
	if stepAgentMissing {
		c.Detail += " — " + stepAgentNoFileCaveat
	}
	c.Detail += " — the SVG guidance block:\n" + diagramGuidanceBlock
	return c
}
