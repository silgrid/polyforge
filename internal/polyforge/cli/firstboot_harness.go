package cli

// firstboot_harness.go — polyforge#176 (84-components-C): the
// workstation first boot's harness-reference merge, the ticket's
// settled Q5 shape: a MINIMAL merge written in Go, independent of the
// host installer (tools/install.sh) — the host installer keeps its own
// copy for host installs, and the boundary between the two is
// documented in docs/workstation.md: both merge the same two facts
// (the pi packages entry and the claude skill symlinks) toward their
// own release root, the installer toward <data-dir>/release/current,
// this one toward /components/current — and neither is called by the
// other.
//
// The merge is minimal and preserving: a user's login (the pi agent
// dir's auth), their other packages and their own skill entries are
// never touched; only this repository's own entry is replaced.

import (
	"errors"
	"os"
)

// harnessMergeOpts is the merge's input.
type harnessMergeOpts struct {
	// Home is $HOME (the claude side's root).
	Home string
	// AgentDir is the pi agent dir (PI_CODING_AGENT_DIR; ~/.pi/agent is
	// the default the whole stack agrees on) — settings.json lives in
	// it and a user's login (auth.json) stays untouched beside it.
	AgentDir string
	// ComponentsRoot is the components volume's root (/components):
	// the release checkout the references point at is <root>/current.
	ComponentsRoot string
	// RepoSlug is the repository's owner/name a pre-#174 git package
	// entry of this repository is recognized by (the same identity
	// tools/install.sh's $REPO carries).
	RepoSlug string
}

// harnessMergeReport is what one merge did (printed by the caller; no
// credentials ever appear here).
type harnessMergeReport struct {
	// PiPackages is the pi settings' packages outcome: added, replaced
	// or already.
	PiPackages string
	// SkillsLinked lists the claude skill names newly linked.
	SkillsLinked []string
	// SkillsKept lists the claude skills already pointing at current.
	SkillsKept []string
	// SkillsUntouched lists the entries that are the user's own and
	// stay as they were.
	SkillsUntouched []string
	// ClaudeHook says the claude SessionStart hook was registered.
	ClaudeHook bool
}

// mergeHarness merges the harness references: the pi packages entry and
// the claude skill symlinks point at <ComponentsRoot>/current, a
// pre-existing entry of this repository is replaced in place, and
// everything else in the settings (a login, other packages, the user's
// own skill entries) is preserved.
func mergeHarness(o harnessMergeOpts) (*harnessMergeReport, error) {
	return nil, errors.New("harness merge: not implemented yet")
}

// ensure the unused import stays until the implementation lands
var _ = os.Getenv
