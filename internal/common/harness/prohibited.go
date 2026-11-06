package harness

// The prohibited-actions prompt section (owner ruling
// 2026-11-10): agent-side safety is a soft mitigation for now — no
// separate user, no sandbox — so every step dispatch
// (driver.assembleStepPrompt) and every task-lane run (task.Manager)
// is told, in one short explicit list, what it must never do. The list
// lives here once; two facts differ between the two callers and are
// parameters: the one source that may require a listed action — a
// step's own definition, never other text its prompt carries (a rewind
// reason, a prior round's summary); a task's own prompt (the analyzer's
// prompt, daemon.analyzerPrompt, marks the message text it embeds as
// untrusted data itself) — and workDir, the directory the session works
// in (a step's capsule root, a task's project workspace).
//
// Summarize (internal/polyforge/summarize) carries no list: its claude
// dispatch runs with no tools (`--tools ""`,), but its pi
// dispatch, `pi -p --no-session --model <m>`, keeps pi's default tools
// (read, bash, edit, write) — a gap noted here, not closed (summarize is
// outside).

// StepProhibitedActions answers the section a step dispatch carries:
// only the step's own definition may require a listed action.
func StepProhibitedActions(capsuleRoot string) string {
	return prohibitedActions("your step definition", capsuleRoot)
}

// TaskProhibitedActions answers the section a task-lane run carries:
// only the task's own prompt may require a listed action.
func TaskProhibitedActions(workDir string) string {
	return prohibitedActions("your task", workDir)
}

func prohibitedActions(requiredBy, workDir string) string {
	return "# Prohibited actions\n\n" +
		"Unless " + requiredBy + " explicitly requires it, never:\n\n" +
		"- read or write the daemon's data or config: `$POLYFORGE_DATA_DIR` (else `~/.local/share/polyforge`), `~/.config/polyforge`, " +
		"and the directories `$CLAUDE_CONFIG_DIR` and `$PI_CODING_AGENT_DIR` name (running `pf` is fine);\n" +
		"- create or change a `.claude/settings*.json`, a Claude Code or git hook, or a command-running key in any git config, " +
		"`--global` and `--system` included (`core.fsmonitor`, `core.hooksPath`, `core.sshCommand`, an alias, a filter);\n" +
		"- read or write another work item's capsule or worktree, or a workspace file outside `" + workDir + "`;\n" +
		"- kill or signal a process you did not start.\n"
}
