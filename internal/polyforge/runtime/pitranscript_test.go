package runtime

// pitranscript_test.go: the per-dispatch live-transcript resolution rule
// (LiveTranscript, polyforge#224's review B1): a resumed pi dispatch's
// live transcript is the capsule file it appends to (the predecessor's
// declared destination, validated — never verbatim from the
// agent-writable journal); every other dispatch resolves through
// LocateTranscript. The commit-boundary sync consumes the same function,
// so its resumed-pi shape (nothing to copy: the live transcript IS the
// commit destination) is pinned here too.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
)

// w224Journal writes one spawn journal (one line per entry) under the
// capsule's runtime-state dir.
func w224Journal(t *testing.T, capDir string, entries []capsule.SpawnJournalEntry) {
	t.Helper()
	journal := filepath.Join(capDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		body += string(raw) + "\n"
	}
	if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w224FreshLine answers one fresh pi dispatch's journal line whose own
// token names its capsule transcript (the spawner's honest shape).
func w224FreshLine(capDir, token string, startedAt time.Time) capsule.SpawnJournalEntry {
	return capsule.SpawnJournalEntry{
		StepID:     "fix",
		SpawnToken: token,
		PID:        os.Getpid(),
		StartedAt:  startedAt.UTC().Format(time.RFC3339Nano),
		SpawnN:     2,
		Transcript: sessionPathFor(capDir, token),
		Harness:    "pi",
	}
}

// A resumed pi dispatch's live transcript is the capsule file it appends
// to — the predecessor's declared destination, validated: the journal is
// agent-writable, so the path is derived (an earlier line's own
// token-named session file inside <capsuleDir>/session/), never taken
// verbatim. A fresh pi dispatch's live transcript stays the session
// store's file (LocateTranscript).
func TestLiveTranscriptResumedPiIsTheCapsuleFile(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	sessionsRoot := filepath.Join(root, "harness", StepAgentSessionsDirName)
	store := piSessionDir(sessionsRoot, capDir)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(store, "2026-10-20T12-00-00-000000Z-resumed-no.jsonl")
	if err := os.WriteFile(live, []byte("{\"t\":\"turn\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	fresh := w224FreshLine(capDir, "tok#1", started)
	// the resumed dispatch: its own token is new, its transcript is the
	// predecessor's capsule file (the --session path the driver passes)
	resumed := capsule.SpawnJournalEntry{
		StepID: "fix", SpawnToken: "tok#2", PID: os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SpawnN: 3,
		Resume: true, Transcript: fresh.Transcript, Harness: "pi",
	}
	// the capsule session file exists (the predecessor wrote it)
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh.Transcript, []byte("{\"t\":\"turn\",\"text\":\"step work\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LiveTranscript(sessionsRoot, "", capDir, []capsule.SpawnJournalEntry{fresh, resumed}); got != fresh.Transcript {
		t.Fatalf("LiveTranscript(resumed pi) = %q, want the predecessor's capsule transcript %q", got, fresh.Transcript)
	}
	// a fresh pi dispatch (the same journal minus the resumed line) still
	// resolves the store's file — the resumed rule never leaks into it
	if got := LiveTranscript(sessionsRoot, "", capDir, []capsule.SpawnJournalEntry{fresh}); got != live {
		t.Fatalf("LiveTranscript(fresh pi) = %q, want the store file %q", got, live)
	}
}

// A forged resumed line is no source: the transcript is never taken
// verbatim from the agent-writable journal — a path outside the capsule
// (an arbitrary file), a session-shaped path no earlier line derives,
// and a relative path all answer "".
func TestLiveTranscriptForgedResumedLineIsNoSource(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "victim.jsonl")
	if err := os.WriteFile(outside, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	earlier := w224FreshLine(capDir, "tok#1", time.Now().Add(-time.Minute))
	if err := os.WriteFile(earlier.Transcript, []byte("{\"t\":\"turn\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shape := func(transcript string) capsule.SpawnJournalEntry {
		return capsule.SpawnJournalEntry{
			StepID: "fix", SpawnToken: "tok#2", PID: os.Getpid(),
			StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SpawnN: 3,
			Resume: true, Transcript: transcript, Harness: "pi",
		}
	}
	for name, tc := range map[string]capsule.SpawnJournalEntry{
		"outside the capsule":        shape(outside),
		"no earlier line derives it": shape(sessionPathFor(capDir, "never-declared")),
		"relative path":              shape("session/session-tok.jsonl"),
	} {
		t.Run(name, func(t *testing.T) {
			if got := LiveTranscript("", "", capDir, []capsule.SpawnJournalEntry{earlier, tc}); got != "" {
				t.Fatalf("LiveTranscript(forged resumed line) = %q, want \"\" (never verbatim)", got)
			}
		})
	}
	// the earlier line itself is not resumed: honest fresh lines still
	// resolve through the store (empty store here → no source)
	if got := LiveTranscript("", "", capDir, []capsule.SpawnJournalEntry{earlier}); got != "" {
		t.Fatalf("LiveTranscript(fresh pi, empty store) = %q, want \"\"", got)
	}
}

// A claude dispatch's live transcript is its native session (outside the
// capsule, resumed or not) — the resumed rule never applies to it.
func TestLiveTranscriptClaudeResumedOrNotIsTheNativeSession(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(root, "claude-config", "projects", harness.ClaudeProjectDirName(capDir), testSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("{\"type\":\"assistant\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{false, true} {
		e := capsule.SpawnJournalEntry{
			StepID: "fix", SpawnToken: "tok#2", PID: os.Getpid(),
			StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SpawnN: 3,
			Resume: resume, Transcript: sessionPathFor(capDir, "tok#2"),
			Harness: "claude", HarnessSession: testSession,
		}
		if got := LiveTranscript("", filepath.Join(root, "claude-config"), capDir, []capsule.SpawnJournalEntry{e}); got != native {
			t.Fatalf("LiveTranscript(claude resumed=%v) = %q, want the native session %q", resume, got, native)
		}
	}
}

// The commit-boundary sync on a resumed pi dispatch copies nothing: the
// dispatch's live transcript IS the capsule file (LiveTranscript answers
// it), so nothing lands in the capsule from anywhere — the capsule's
// own content stays untouched.
func TestSyncLiveTranscriptResumedPiCopiesNothing(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	fresh := w224FreshLine(capDir, "tok#1", time.Now().Add(-time.Minute))
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh.Transcript, []byte("CAPSULE TAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resumed := capsule.SpawnJournalEntry{
		StepID: "fix", SpawnToken: "tok#2", PID: os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SpawnN: 3,
		Resume: true, Transcript: fresh.Transcript, Harness: "pi",
	}
	w224Journal(t, capDir, []capsule.SpawnJournalEntry{fresh, resumed})
	r := &Runtime{cfg: Config{}}
	r.syncLiveTranscript(capDir)
	if got, _ := os.ReadFile(fresh.Transcript); string(got) != "CAPSULE TAIL\n" {
		t.Fatalf("the resumed dispatch's sync overwrote the capsule transcript: %q", got)
	}
}
