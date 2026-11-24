package driver

// w82_step_prompt_toolnames_test.go (polyforge#82, AC1): every assembled
// step prompt of the new-conversation full path carries the tool-name
// correction rule — this environment runs no MCP server, the available
// tools are the built-in list, and a `Tool … not found` reply means a
// wrong name, never an environment fault. The rule lives once, in the
// `## Tool names` section of _shared/step-header.md (part 3 of every
// prompt, rendered verbatim for pi and claude dispatches alike — so one
// section true for both harnesses is true in every prompt), and its
// claude list is pinned against runtime.ClaudeStepTools, its pi list
// against the step agent dir's isolation set (stepagentdir.go: the
// built-ins plus the one subagent extension, mcp.json deliberately
// absent) — the two code-level tool surfaces the sentence must not drift
// from.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// w82ToolNamesHeading is the section's heading in _shared/step-header.md.
const w82ToolNamesHeading = "## Tool names"

// w82ToolNamesSection answers the shipped step header's tool-names
// section body ("" and false when the section is absent).
func w82ToolNamesSection(t *testing.T) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "step-header.md"))
	if err != nil {
		t.Fatalf("read the shipped step header: %v", err)
	}
	var body []string
	in := false
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(ln) == w82ToolNamesHeading
			continue
		}
		if in {
			body = append(body, ln)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n")), in
}

// TestW82StepPromptCarriesToolNameCorrection (AC1): the shipped
// step-header.md carries the tool-name correction rule — no MCP server,
// the available tool list, the not-found correction — accurate for both
// harnesses (the claude enumeration is exactly runtime.ClaudeStepTools,
// the pi enumeration names the built-ins and the subagent extension),
// and every shipped step's assembled new-conversation prompt renders the
// section verbatim. Red on the pre-change tree: no section exists.
func TestW82StepPromptCarriesToolNameCorrection(t *testing.T) {
	section, ok := w82ToolNamesSection(t)
	if !ok {
		t.Fatalf("the shipped step header carries no %q section", w82ToolNamesHeading)
	}
	flat := strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		"no MCP server",
		"`mcp__…`",
		"`Tool … not found`",
		"retry with the exact name",
		"not an environment fault",
		"keep working until `pf step handoff`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the tool-names section lacks %q:\n%s", want, section)
		}
	}
	// the harness accuracy AC1 pins: the claude list is exactly the
	// daemon's ClaudeStepTools (claudespawn.go: the step's whole tool
	// surface under --strict-mcp-config, so no MCP server exists), the
	// pi list names the built-ins plus the subagent extension
	// (stepagentdir.go's minimal set: mcp.json deliberately absent).
	var claude []string
	for _, tool := range strings.Split(runtime.ClaudeStepTools, ",") {
		claude = append(claude, "`"+tool+"`")
	}
	if want := "on a claude dispatch " + strings.Join(claude, ", ") + "."; !strings.Contains(flat, want) {
		t.Errorf("the tool-names section must enumerate ClaudeStepTools exactly on the claude dispatch: %s", want)
	}
	for _, tool := range []string{"`bash`", "`read`", "`edit`", "`write`", "`subagent`"} {
		if !strings.Contains(flat, tool) {
			t.Errorf("the tool-names section's pi list lacks %s", tool)
		}
	}
	// every shipped step's assembled prompt (the new-conversation full
	// path — part 3 renders the section verbatim, for a pi dispatch as
	// for a claude one: the same stepHeader text reaches both)
	for _, bs := range shippedDefinitions(t) {
		prompt := spawnStepPrompt(t, filepath.Base(bs[0]), bs[1])
		if !strings.Contains(strings.Join(strings.Fields(prompt), " "), flat) {
			t.Errorf("%s/%s: the assembled prompt does not carry the tool-names section verbatim", filepath.Base(bs[0]), bs[1])
		}
	}
}
