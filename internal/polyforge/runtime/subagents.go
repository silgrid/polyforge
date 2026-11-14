package runtime

// subagents.go: the claude half — a step's subagent
// conversations ride the main session's sync into the capsule. Claude
// Code writes each subagent's conversation beside the native session, in
// <claude-config>/projects/<cwd>/<uuid>/subagents/agent-<id>.jsonl (plus
// agent-<id>.meta.json: description, agent type, the parent's tool_use
// id); harness.ClaudeSubagentFiles lists them. Every sync point that
// copies the main session (the observe monitor's commit-boundary sync,
// syncLiveTranscript before a seal/pause/bounded-fail commit) also calls
// SyncSubagentTranscripts, which copies them, under their own names, to
// capsule.SubagentDir of the conversation's FIRST capsule transcript:
// a resumed claude dispatch continues the same native session, so its
// subagents (old and new) all land in one directory and none is copied
// twice; a copy is only ever added or refreshed, never removed, so a
// sidecar Claude Code has since dropped stays in the capsule.
//
// pi's subagent conversations are recorded by the driver from the step's
// own stdout (driver/subagentrec.go) straight into the same layout; there
// is nothing of pi's to copy here.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
)

// SyncSubagentTranscripts copies every claude conversation's subagent
// sidecars of the capsule into the capsule (best-effort, synchronous;
// failures are logged and skipped — the commit proceeds without them).
// The spawn journal names the conversations: one per distinct native
// session uuid, its directory beside the transcript of the session's
// first journal line. The journal is agent-writable, so that line's
// transcript must be the path this runtime derives from its own spawn
// token (sessionPathFor) and the destination passes
// capsule.EnsureSubagentDir; a line that fails either is skipped.
func SyncSubagentTranscripts(claudeConfigDir, capsuleDir string) {
	if claudeConfigDir == "" {
		return
	}
	entries, _ := capsule.ReadSpawnJournal(capsuleDir)
	seen := map[string]bool{}
	for _, e := range entries {
		if harness.Of(e.Harness) != harness.Claude || !harness.ValidSessionID(e.HarnessSession) || seen[e.HarnessSession] {
			continue
		}
		seen[e.HarnessSession] = true
		if e.SpawnToken == "" || strings.ContainsAny(e.SpawnToken, `/\`) || filepath.Clean(e.Transcript) != sessionPathFor(capsuleDir, e.SpawnToken) {
			continue
		}
		files := harness.ClaudeSubagentFiles(e.HarnessSession, claudeConfigDir, capsuleDir)
		if len(files) == 0 {
			continue
		}
		dir, ok := capsule.EnsureSubagentDir(capsuleDir, e.Transcript)
		if !ok {
			fmt.Fprintf(os.Stderr, "runtime: subagent sync skipped: no trusted directory beside %q\n", e.Transcript)
			continue
		}
		for _, src := range files {
			if err := copyIfChanged(src, filepath.Join(dir, filepath.Base(src))); err != nil {
				fmt.Fprintf(os.Stderr, "runtime: subagent sync: %v\n", err)
			}
		}
	}
}

// copyIfChanged copies src onto dst (sessionjsonl.CopyReadonly) unless
// dst already holds it: same size and written no earlier than src's last
// write (claude's sidecars only grow; its meta file changes size when it
// finishes). Neither a symlink at src is read through nor one at dst
// written through.
func copyIfChanged(src, dst string) error {
	si, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !si.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	if di, err := os.Lstat(dst); err == nil {
		if di.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", dst)
		}
		if di.Mode().IsRegular() && di.Size() == si.Size() && !di.ModTime().Before(si.ModTime()) {
			return nil
		}
	}
	return sessionjsonl.CopyReadonly(src, dst)
}
