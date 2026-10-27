package runtime

// pitranscript.go: the pi-harness live-transcript face (pilot F3).
// polyforge hands every pi step its capsule's own session directory,
//
//	<harness-root>/step-agent-sessions/<dashified-capsule-dir>/
//
// (piSessionDir), as PI_CODING_AGENT_SESSION_DIR. pi uses that value
// verbatim and writes its <ts>-<uuid>.jsonl sessions flat into it; pi's
// own per-cwd dashify rule applies only in its default store
// (~/.pi/agent/sessions/), which polyforge never uses. The observe
// monitor owns the sync (locate after spawn, copy into the capsule at
// every commit boundary of the dispatch, before the commit so the
// transcript rides git history — v1 ruling C1
// (archive/v1-architecture-spec.md)), per dispatch onto the path its
// step_started fact names. This file only holds the locator the monitor
// calls and the one definition of the directory both sides share.
//
// History: the v0 supervisor set PI_SESSION_DIR, a name the harness
// ignores (it honors <APP>_CODING_AGENT_SESSION_DIR), so transcripts
// landed in the default store; 9e8546a2 then passed the shared flat
// step-agent-sessions root while the locator scanned a per-capsule
// subdirectory, so nothing was found (polyforge#211).
//
// The per-capsule directory name is polyforge's own, reusing pi's
// dashify rule mirrored from its source: the resolved capsule dir is
// stripped of its one leading slash, every "/ \ :" becomes "-", and the
// result is wrapped in "--" ... "--". A capsule at
// /root/ws/capsules/polyforge-140 therefore gets
// "--root-ws-capsules-polyforge-140--". Path characters the rule does
// NOT fold (e.g. "#") survive into the directory name — one more reason
// the capsule directory never carries a raw slug: it is named by
// api.PathName ("<project>-<seq>", F2 / polyforge#144).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
)

// locateSlack is how far before the spawn moment a live transcript's
// mtime may sit: mtime is only file-resolution precise and the file is
// created moments after process birth. The slack stays smaller than any
// honest predecessor's last write (the old tree is fully collected
// before a re-derivation begins), so it can never re-admit a dead
// instance's transcript.
const locateSlack = 2 * time.Second

// Transcript FILE naming — deliberately NOT renamed with the v1 fossil
// cleanup: the capsule-side transcript copies are named
// session-<fingerprint>#N.jsonl, where the "#N" suffix is spawn_n's
// file-level expression — it carries the "which derivation wrote this"
// semantics (a re-derivation gets its own #-suffixed file and never
// clobbers its predecessor's; see sessionPathFor and the spawn token in
// recover.go). LocateLiveTranscript relies on that shape when it scans
// the harness session store for a live transcript. Renaming the pattern
// would orphan every existing sealed capsule's transcript files (the
// resume path re-reads the declared path from the capsule), so the
// naming stays frozen even though the spawn-journal line's display-key
// fossil (instance → spawn_token) is cleaned up.

// piSessionDirName derives polyforge's per-capsule pi session directory
// name (the dashify rule above). capsuleDir is resolved through symlinks
// first so the spawn side (SpawnParams.CapsuleDir) and the locator side
// (the daemon's capsuleDir) compute the same name even when one of them
// reaches the capsule through a symlink.
func piSessionDirName(capsuleDir string) string {
	real := capsuleDir
	if resolved, err := filepath.EvalSymlinks(capsuleDir); err == nil {
		real = resolved
	}
	trimmed := real
	// strip exactly one leading separator (the rule's ^[/\\] anchor)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if len(trimmed) > 0 && trimmed[0] == '\\' {
		trimmed = trimmed[1:]
	}
	dashed := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, trimmed)
	return "--" + dashed + "--"
}

// piSessionDir is the capsule's own pi session directory under the step
// session store — the ONE fact both sides read (polyforge#211): the
// spawn hands it to the step process as PI_CODING_AGENT_SESSION_DIR and
// LocateLiveTranscript scans it. pi uses that env value verbatim as the
// session directory and writes its sessions flat into it (the per-cwd
// dashified subdirectory exists only in its default store), so the
// per-capsule level must be part of the value the spawn passes; a
// shared flat root would mix every workspace's WIs and leave the
// locator nothing capsule-specific to scan. "" when no store is
// configured.
func piSessionDir(sessionsRoot, capsuleDir string) string {
	if sessionsRoot == "" {
		return ""
	}
	return filepath.Join(sessionsRoot, piSessionDirName(capsuleDir))
}

// LocateLiveTranscript scans the harness session store for the live
// transcript of a derivation whose process was born at notBefore: the
// newest .jsonl under the capsule's own session directory (piSessionDir) whose
// mtime is at/after the spawn moment (minus the locate slack). "" when
// the directory, or any qualifying file, does not exist — the callers
// treat that as the typed transcript_missing condition, never an error.
func LocateLiveTranscript(sessionsRoot, capsuleDir string, notBefore time.Time) string {
	dir := piSessionDir(sessionsRoot, capsuleDir)
	if dir == "" {
		return "" // no step session store configured: never the operator's ~/.pi
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	floor := notBefore.Add(-locateSlack)
	var (
		best    string
		bestMod time.Time
	)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().Before(floor) {
			continue // an earlier derivation's file (or unrelated residue)
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = filepath.Join(dir, e.Name())
			bestMod = info.ModTime()
		}
	}
	return best
}

// LocateTranscript is the per-harness live-transcript locator both sync
// points share (polyforge#159, dispatched by harness per #171 ruling 7):
// pi → the harness session store's newest session file of the capsule
// since the spawn moment; claude → its native session file under the
// claude-config dir the caller names, by the uuid the daemon minted for
// the dispatch (only a canonical uuid is looked up — the id may come
// from the agent-writable spawn journal).
func LocateTranscript(piSessionsRoot, claudeConfigDir, capsuleDir string, spawnAt time.Time, h, session string) string {
	if harness.Of(h) == harness.Claude {
		return harness.LocateClaudeSession(session, claudeConfigDir, capsuleDir)
	}
	return LocateLiveTranscript(piSessionsRoot, capsuleDir, spawnAt)
}

// LiveTranscript answers the current dispatch's (the latest
// spawn-journal entry's) live-transcript path — the ONE per-dispatch
// resolution rule every consumer of the current dispatch's live
// transcript shares: the commit-boundary sync's source, and the
// summarize lane's daemon-side resolution (polyforge#224). A resumed pi
// dispatch appends to the capsule transcript itself — the destination
// an earlier journal line declared and trustedTranscriptDest derives,
// validated by trustedResumeTranscript (the journal is agent-writable,
// so the path is never taken verbatim); every other dispatch — a fresh
// pi dispatch's session-store file, a claude dispatch's native session
// (outside the capsule, resumed or not) — resolves through
// LocateTranscript. "" when nothing resolves: the callers treat that
// as no source (the sync skips, the summarize lane's growth gate
// blocks).
func LiveTranscript(piSessionsRoot, claudeConfigDir, capsuleDir string, entries []capsule.SpawnJournalEntry) string {
	if len(entries) == 0 {
		return ""
	}
	last := entries[len(entries)-1]
	claude := harness.Of(last.Harness) == harness.Claude
	if last.Resume && !claude {
		// a resumed pi dispatch appends to the capsule file itself
		return trustedResumeTranscript(capsuleDir, entries)
	}
	spawnAt, err := time.Parse(time.RFC3339Nano, last.StartedAt)
	if err != nil && !claude {
		return ""
	}
	return LocateTranscript(piSessionsRoot, claudeConfigDir, capsuleDir, spawnAt, last.Harness, last.HarnessSession)
}

// trustedResumeTranscript validates the capsule transcript a resumed pi
// dispatch appends to (its --session path, the journal's own declaration
// — agent-writable, so never verbatim): the path must be the
// token-derived session file an EARLIER journal line declared as its
// own destination (the predecessor whose conversation the dispatch
// continues — a resumed dispatch appends to a predecessor's file, never
// its own token-named one), resolving inside <capsuleDir>/session/ with
// the parent symlink-resolved and the file itself not a symlink — the
// same jail trustedTranscriptDest keeps. It answers the validated path,
// or "" (the line names no trusted source).
func trustedResumeTranscript(capsuleDir string, entries []capsule.SpawnJournalEntry) string {
	last := entries[len(entries)-1]
	if last.Transcript == "" {
		return ""
	}
	want := filepath.Clean(last.Transcript)
	ok := false
	for _, e := range entries[:len(entries)-1] {
		if e.SpawnToken == "" || strings.ContainsAny(e.SpawnToken, `/\`) {
			continue
		}
		if e.Transcript != "" && filepath.Clean(e.Transcript) == want &&
			sessionPathFor(capsuleDir, e.SpawnToken) == want {
			ok = true
			break
		}
	}
	if !ok {
		return ""
	}
	sessDir, err := filepath.EvalSymlinks(filepath.Join(capsuleDir, capsule.DirSession))
	if err != nil {
		return ""
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(want))
	if err != nil {
		return ""
	}
	if rel, err := filepath.Rel(sessDir, parent); err != nil || rel != "." {
		return ""
	}
	if fi, err := os.Lstat(want); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return want
}

// syncLiveTranscript copies the CURRENT dispatch's live harness
// transcript onto that dispatch's own capsule session path (the latest
// spawn-journal entry's transcript) so a seal / pause / bounded-fail
// commit carries the tail written since the last commit boundary. It is
// synchronous and best-effort (a missing source or journal is a no-op:
// the commit proceeds without the tail) and touches only the latest
// dispatch's file — an earlier step's transcript is never overwritten.
// A resumed dispatch already appends to the capsule file itself. Callers
// run it after the agent tree is killed and BEFORE the commit and the
// read-only seal.
func (r *Runtime) syncLiveTranscript(capsuleDir string) {
	entries, _ := capsule.ReadSpawnJournal(capsuleDir)
	if len(entries) == 0 {
		return
	}
	last := entries[len(entries)-1]
	claude := harness.Of(last.Harness) == harness.Claude
	// a resumed pi dispatch appends to the capsule file itself: its live
	// transcript (LiveTranscript) IS the commit destination — nothing to
	// copy; a claude dispatch's native session is always outside the
	// capsule (resumed or not), so it is always copied
	if last.Transcript == "" || (last.Resume && !claude) {
		return
	}
	// The journal is agent-writable: the destination is never taken from
	// it verbatim (see trustedTranscriptDest).
	dst, ok := trustedTranscriptDest(capsuleDir, entries)
	if !ok {
		fmt.Fprintf(os.Stderr, "runtime: transcript sync skipped: the latest spawn-journal line names an untrusted destination %q\n", last.Transcript)
		return
	}
	// the current dispatch's live transcript through the shared
	// per-dispatch rule (LiveTranscript — the same resolution the
	// summarize lane's daemon side uses, not a duplicate)
	src := LiveTranscript(r.cfg.StepAgentSessionsDir(), r.cfg.ClaudeConfigDir(), capsuleDir, entries)
	if src == "" || src == dst {
		return
	}
	if err := sessionjsonl.CopyReadonly(src, dst); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: transcript sync before commit failed: %v\n", err)
	}
}

// trustedTranscriptDest validates the latest spawn-journal line's
// transcript destination before the runtime writes to it. The agent can
// forge journal lines, so the path must (1) equal the one this runtime
// derives itself for that spawn token (sessionPathFor), (2) resolve inside
// <capsuleDir>/session/ with symlinks on the parent resolved and the file
// itself not a symlink, and (3) not be the destination of any EARLIER
// journal line (a forged line aimed at step 1's file). It answers the
// validated path, or false to skip the sync.
func trustedTranscriptDest(capsuleDir string, entries []capsule.SpawnJournalEntry) (string, bool) {
	last := entries[len(entries)-1]
	if last.SpawnToken == "" || strings.ContainsAny(last.SpawnToken, `/\`) {
		return "", false
	}
	want := sessionPathFor(capsuleDir, last.SpawnToken)
	if filepath.Clean(last.Transcript) != want {
		return "", false
	}
	for _, e := range entries[:len(entries)-1] {
		if e.SpawnToken == last.SpawnToken || (e.Transcript != "" && filepath.Clean(e.Transcript) == want) {
			return "", false
		}
	}
	sessDir, err := filepath.EvalSymlinks(filepath.Join(capsuleDir, capsule.DirSession))
	if err != nil {
		return "", false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(want))
	if err != nil {
		return "", false
	}
	if rel, err := filepath.Rel(sessDir, parent); err != nil || rel != "." {
		return "", false
	}
	if fi, err := os.Lstat(want); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return want, true
}
