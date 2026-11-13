package runtime

// subagents_test.go: — a claude step's subagent sidecars
// (<claude-config>/projects/<cwd>/<uuid>/subagents/agent-*.jsonl and
// .meta.json) ride the main session's sync into the capsule, beside the
// capsule transcript that first holds the conversation
// (session/session-<token>/subagents/), so a resumed dispatch never
// copies them a second time and a later sync never drops one.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
)

const testSession2 = "1c0a7b63-4d2e-4f9a-8b8c-2d3e4f5a6b7c"

// claudeLine is one claude dispatch's honest journal line: its own
// token-named capsule transcript and its native session uuid.
func claudeLine(capDir, token, session string, resume bool) capsule.SpawnJournalEntry {
	return capsule.SpawnJournalEntry{
		StepID: "build", SpawnToken: token, PID: os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SpawnN: 1,
		Resume: resume, Transcript: sessionPathFor(capDir, token),
		Harness: "claude", HarnessSession: session,
	}
}

// nativeSidecars writes the native subagents dir of one claude session.
func nativeSidecars(t *testing.T, configDir, capDir, session string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", harness.ClaudeProjectDirName(capDir), session, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readOrEmpty(p string) string {
	raw, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(raw)
}

// AC1: every subagent sidecar of the session is copied into the capsule's
// session/ beside the conversation's first capsule transcript — named by
// that transcript and the subagent's own agent id; a resumed dispatch of
// the same native session adds no second copy; a later sync carries a
// grown or new sidecar and never removes one whose native file is gone;
// another native session of the job keeps its own directory.
func TestSyncSubagentTranscriptsClaude(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "claude-config")
	native := nativeSidecars(t, configDir, capDir, testSession, map[string]string{
		"agent-a01.jsonl":     "{\"type\":\"user\",\"isSidechain\":true}\n",
		"agent-a01.meta.json": `{"description":"read note","toolUseId":"toolu_1","finished":false}`,
	})
	w224Journal(t, capDir, []capsule.SpawnJournalEntry{
		claudeLine(capDir, "fp", testSession, false),
		{StepID: "spec", SpawnToken: "fp#1", SpawnN: 2, Transcript: sessionPathFor(capDir, "fp#1"), Harness: "pi"},
		claudeLine(capDir, "fp#2", testSession, true),
	})
	SyncSubagentTranscripts(configDir, capDir)
	first := filepath.Join(capDir, capsule.DirSession, "session-fp", capsule.DirSubagents)
	for _, name := range []string{"agent-a01.jsonl", "agent-a01.meta.json"} {
		if got, want := readOrEmpty(filepath.Join(first, name)), readOrEmpty(filepath.Join(native, name)); got != want || got == "" {
			t.Fatalf("%s: capsule copy %q, native %q", name, got, want)
		}
	}
	for _, other := range []string{"session-fp#2", "session-fp#1"} {
		if _, err := os.Stat(filepath.Join(capDir, capsule.DirSession, other)); err == nil {
			t.Fatalf("a second copy landed beside %s", other)
		}
	}

	// a later sync: one sidecar grew, one is new, the meta finished
	nativeSidecars(t, configDir, capDir, testSession, map[string]string{
		"agent-a01.jsonl":     "{\"type\":\"user\",\"isSidechain\":true}\n{\"type\":\"assistant\",\"isSidechain\":true}\n",
		"agent-a01.meta.json": `{"description":"read note","toolUseId":"toolu_1","finished":true}`,
		"agent-b02.jsonl":     "{\"type\":\"user\",\"isSidechain\":true}\n",
	})
	SyncSubagentTranscripts(configDir, capDir)
	for _, name := range []string{"agent-a01.jsonl", "agent-a01.meta.json", "agent-b02.jsonl"} {
		if got, want := readOrEmpty(filepath.Join(first, name)), readOrEmpty(filepath.Join(native, name)); got != want {
			t.Fatalf("%s after the second sync: %q, want %q", name, got, want)
		}
	}

	// the native sidecars are gone (claude-config pruned): the capsule
	// copies stay; a second native session of the job gets its own dir
	if err := os.RemoveAll(native); err != nil {
		t.Fatal(err)
	}
	nativeSidecars(t, configDir, capDir, testSession2, map[string]string{"agent-c03.jsonl": "{}\n"})
	w224Journal(t, capDir, []capsule.SpawnJournalEntry{
		claudeLine(capDir, "fp", testSession, false),
		claudeLine(capDir, "fp#2", testSession, true),
		claudeLine(capDir, "fp#3", testSession2, false),
	})
	SyncSubagentTranscripts(configDir, capDir)
	if readOrEmpty(filepath.Join(first, "agent-b02.jsonl")) == "" || readOrEmpty(filepath.Join(first, "agent-a01.jsonl")) == "" {
		t.Fatal("a capsule copy was dropped when its native file went away")
	}
	if readOrEmpty(filepath.Join(capDir, capsule.DirSession, "session-fp#3", capsule.DirSubagents, "agent-c03.jsonl")) != "{}\n" {
		t.Fatal("the second session's sidecar is not beside its own transcript")
	}
}

// The journal is agent-writable: a claude line whose transcript is not the
// token-derived capsule path, or whose token names a path, gets no copy;
// a pi line and a line without a canonical uuid are not claude sessions.
func TestSyncSubagentTranscriptsRefusesForgedLines(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "claude-config")
	nativeSidecars(t, configDir, capDir, testSession, map[string]string{"agent-a01.jsonl": "{}\n"})
	outside := filepath.Join(root, "outside", "session-x.jsonl")
	forged := claudeLine(capDir, "fp", testSession, false)
	forged.Transcript = outside
	slashed := claudeLine(capDir, "../fp", testSession, false)
	w224Journal(t, capDir, []capsule.SpawnJournalEntry{forged, slashed})
	SyncSubagentTranscripts(configDir, capDir)
	if _, err := os.Stat(filepath.Join(root, "outside")); err == nil {
		t.Fatal("a forged transcript path received the sidecars")
	}
	ents, _ := os.ReadDir(filepath.Join(capDir, capsule.DirSession))
	if len(ents) != 0 {
		t.Fatalf("a forged line produced capsule files: %v", ents)
	}
}

// The seal/pause/bounded-fail sync carries the subagent sidecars with the
// main session, so a step that ends without another commit boundary still
// commits them.
func TestSyncLiveTranscriptCarriesClaudeSubagents(t *testing.T) {
	root := t.TempDir()
	capDir := filepath.Join(root, "cap")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "claude-config")
	mainNative := filepath.Join(configDir, "projects", harness.ClaudeProjectDirName(capDir), testSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(mainNative), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainNative, []byte("{\"type\":\"assistant\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nativeSidecars(t, configDir, capDir, testSession, map[string]string{"agent-a01.jsonl": "{\"type\":\"assistant\"}\n"})
	w224Journal(t, capDir, []capsule.SpawnJournalEntry{claudeLine(capDir, "fp", testSession, false)})
	r := &Runtime{cfg: Config{HarnessRoot: root}}
	r.syncLiveTranscript(capDir)
	if readOrEmpty(sessionPathFor(capDir, "fp")) == "" {
		t.Fatal("the main session was not synced")
	}
	if readOrEmpty(filepath.Join(capDir, capsule.DirSession, "session-fp", capsule.DirSubagents, "agent-a01.jsonl")) == "" {
		t.Fatal("the subagent sidecar did not ride the seal sync")
	}
}

// The copy never reads through a symlink at the source (the listing
// already skips one; this is the copy's own guard for a file swapped
// after it was listed).
func TestCopyIfChangedRefusesSymlinkSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.jsonl")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "agent-a.jsonl")
	if err := os.Symlink(target, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.jsonl")
	if err := copyIfChanged(src, dst); err == nil {
		t.Fatal("a symlinked source was copied")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("the destination was written")
	}
}
