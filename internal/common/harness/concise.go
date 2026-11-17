package harness

import "strings"

// concise.go (polyforge#101): the one short output-discipline rule —
// concise, plain speech — every agent surface carries: the harness
// context files (appended by `pf doctor --fix-harness-prompts` as a
// marked block, beside the diagram guidance block), every step dispatch
// (driver.assembleStepPrompt, under the prohibited actions) and every
// task-lane input (task.dispatchedPrompt, the daemon's ticket-analysis
// prompt). One source, English (the reply language is a separate
// instruction), project-neutral (no project or component name: it ships
// with the binary and serves any project): every surface renders
// ConciseRule's exact bytes, pinned byte-identical by the contract
// tests. The rule is the floor; the output-format skill (loaded by the
// interactive session) carries the details.
//
// The function bodies first shipped one ordinary commit ahead of the
// tests as zero-value stubs, so the contract test's first red was an
// assertion, never a compile error (the test-first record).

// The concise-guidance block's markers: the idempotency and rollback
// anchors (the doctor appends the block between them; deleting the two
// marker lines and what sits between rolls it back).
const (
	conciseGuidanceBegin = "<!-- polyforge:concise-guidance:begin -->"
	conciseGuidanceEnd   = "<!-- polyforge:concise-guidance:end -->"
)

// The rule's own text is the one source; every surface renders these
// exact bytes, and the contract test pins them against each rendering.
const conciseRuleText = "## Output discipline (concise, plain speech)\n\n" +
	"- Lead with the outcome: the first line of a reply states the result or the next action — no preamble, no process retelling, no sign-off.\n" +
	"- Match the reply's length and depth to the question; explain a term where it first appears; no filler, no decorative structure.\n" +
	"- A task's own required format (a JSON contract, a fixed schema) always wins over this rule's shape.\n" +
	"- Keep raw material out of the conversation: full command output, logs and dumps go into a dedicated subdirectory of the workspace's temporary directory; the reply carries counts, differences, short excerpts and the paths to read on.\n" +
	"- Once a report is filed where it belongs, later replies carry conclusions and references only.\n"

// ConciseRule answers the one short output-discipline rule, its `## `
// heading included, ending in a newline — the exact bytes every surface
// (the doctor block, the step prompt, the three task prompts) carries.
func ConciseRule() string {
	return conciseRuleText
}

// ConciseGuidanceBlock answers the marked block the doctor appends to a
// harness context file that lacks it: the begin marker, the rule, the
// end marker — the same shape as the diagram guidance block.
func ConciseGuidanceBlock() string {
	return conciseGuidanceBegin + "\n" + ConciseRule() + conciseGuidanceEnd + "\n"
}

// HasConciseGuidance reports whether a context file's body carries the
// concise-guidance block (its begin marker).
func HasConciseGuidance(body string) bool {
	return strings.Contains(body, conciseGuidanceBegin)
}
