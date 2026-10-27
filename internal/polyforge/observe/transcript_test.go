package observe

// transcript_test.go: the F3 commit-boundary transcript-sync regressions
// (real files on disk): the live harness transcript is copied onto the
// capsule's declared session path BEFORE the commit/push at every
// execution.json step boundary, a source that cannot
// be located is the once-per-attempt typed advisory (transcript_missing)
// and never blocks the push, and a resumed derivation that already
// writes into the capsule is not re-copied. The per-derivation transcript
// facts ride RTRecovered attachments — the production adoption path — so
// these tests exercise the monitor exactly as the daemon drives it.

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// liveFixture builds one real live transcript (a harness-store-shaped
// .jsonl) plus the declared capsule session path.
type liveFixture struct {
	capsuleDir string
	dst        string
	src        string
	body       []byte
}

func newLiveFixture(t *testing.T, capsuleDir, liveDir string) liveFixture {
	t.Helper()
	f := liveFixture{capsuleDir: capsuleDir, dst: filepath.Join(capsuleDir, "session", "session-ra_wi_test.jsonl")}
	f.src = filepath.Join(liveDir, "2026-09-30T01-02-03-000Z_sess.jsonl")
	f.body = []byte("{\"type\":\"session\",\"id\":\"s1\"}\n{\"type\":\"message_end\"}\n")
	if err := os.MkdirAll(filepath.Dir(f.src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.src, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// stepStarted announces one dispatch through the production path — the
// driver's step_started fact naming the dispatch's own transcript — and
// waits until the monitor adopted it.
func stepStarted(t *testing.T, m *Monitor, spawn int64, step, dst string, resumed bool) {
	t.Helper()
	m.link.Fact(supervise.StepStarted{
		Step: step, SpawnN: spawn, Model: "m", Harness: "pi",
		TranscriptPath: dst, Resumed: resumed,
		StartedAt: time.Now().Add(-time.Minute),
	})
	soon(t, 2*time.Second, "dispatch adopted", func() bool { return m.activity().Step == step })
}

// pushOnceRecorded is a PushFunc that records whether the capsule's
// declared session path existed (with the live bytes) AT PUSH TIME — the
// ordering pin: the copy must precede the commit. The flag is atomic: the
// push runs in the monitor's sub-goroutine, the poller in the test.
func pushOnceRecorded(f liveFixture, sawCopy *atomic.Bool) PushFunc {
	return func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
		if raw, err := os.ReadFile(f.dst); err == nil && string(raw) == string(f.body) {
			sawCopy.Store(true)
		}
		return "sha_sync", true, gitops.PushTimings{}, nil
	}
}

func TestStepBoundarySyncsTranscriptBeforePush(t *testing.T) {
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newLiveFixture(t, capsuleDir, filepath.Join(dir, "pi-sessions"))

	var sawCopy atomic.Bool
	h := newFakeHandle("ra_wi_test")
	m, ob := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = pushOnceRecorded(f, &sawCopy)
		c.locateLive = func(string, time.Time, string, string) string { return f.src }
	})
	stepStarted(t, m, 1, "code", f.dst, false)

	// one execution.json step-boundary write: the version bump schedules
	// the async commit pipeline (sync → push).
	m.writeExec(t, 1, "code")

	soon(t, 5*time.Second, "the step-boundary push never ran", func() bool {
		return sawCopy.Load()
	})
	// the capsule copy is byte-identical to the live source, and the
	// source was never modified.
	raw, err := os.ReadFile(f.dst)
	if err != nil || string(raw) != string(f.body) {
		t.Fatalf("capsule session copy wrong: %v (%q)", err, raw)
	}
	srcRaw, serr := os.ReadFile(f.src)
	if serr != nil || string(srcRaw) != string(f.body) {
		t.Fatal("the live source must be untouched by the sync")
	}
	if len(eventsOf(m, EvPublished)) == 0 {
		t.Fatal("the push must have published")
	}
	expectNoOB(t, ob, 100*time.Millisecond, "no recovery may fire for a healthy transcript sync")
}

// Each step's transcript is synced to its OWN capsule file: a later step's
// live transcript never lands on an earlier step's path, and after a
// recovery nothing is synced until the successor's step_started names the
// dispatch's transcript.
func TestEachStepSyncsToItsOwnTranscript(t *testing.T) {
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "pi-sessions")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	src1, src2 := filepath.Join(live, "one.jsonl"), filepath.Join(live, "two.jsonl")
	dst1 := filepath.Join(capsuleDir, "session", "session-ra_wi_test.jsonl")
	dst2 := filepath.Join(capsuleDir, "session", "session-ra_wi_test#1.jsonl")
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		raw, _ := os.ReadFile(p)
		return string(raw)
	}
	write(src1, "step one\n")
	write(src2, "step two\n")

	h := newFakeHandle("ra_wi_test")
	var pushes atomic.Int32
	var current atomic.Value // the live file the "harness" currently writes
	current.Store(src1)
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.locateLive = func(string, time.Time, string, string) string { return current.Load().(string) }
		c.push = func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			pushes.Add(1)
			return "sha_steps", true, gitops.PushTimings{}, nil
		}
	})
	pushedAfter := func(what string, n int32) {
		soon(t, 5*time.Second, what, func() bool { return pushes.Load() >= n })
	}

	stepStarted(t, m, 1, "one", dst1, false)
	m.writeExec(t, 1, "one")
	pushedAfter("step one push", 1)
	if got := read(dst1); got != "step one\n" {
		t.Fatalf("step one transcript = %q", got)
	}

	current.Store(src2)
	stepStarted(t, m, 2, "two", dst2, false)
	m.writeExec(t, 2, "two")
	pushedAfter("step two push", 2)
	if got := read(dst2); got != "step two\n" {
		t.Fatalf("step two transcript = %q", got)
	}
	if got := read(dst1); got != "step one\n" {
		t.Fatalf("step one's transcript was overwritten by step two: %q", got)
	}

	// a recovery drops the dead dispatch: later commits sync nothing
	// until the successor's own step_started.
	write(src2, "step two, later\n")
	m.rtQ.Push(Attach{Msg: protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_test", SpawnN: 2}, Handle: h})
	soon(t, 2*time.Second, "recovery adopted", func() bool { return m.currentSpawn() == 2 })
	m.writeExec(t, 3, "two")
	pushedAfter("post-recovery push", 3)
	if got := read(dst2); got != "step two\n" {
		t.Fatalf("a commit between a recovery and the next step_started synced %q onto the dead dispatch's file", got)
	}
	if got := read(dst1); got != "step one\n" {
		t.Fatalf("step one's transcript changed across the recovery: %q", got)
	}
}

func TestTranscriptMissingWarnsOnceAndPushProceeds(t *testing.T) {
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	// no live file anywhere and no runtime-recorded path: the locator
	// (wired like the daemon wires it, to a scratch root holding nothing)
	// answers "" — the typed advisory path.
	h := newFakeHandle("ra_wi_test")
	var pushed atomic.Bool
	located := 0
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.locateLive = func(capsuleDir string, spawnAt time.Time, _, _ string) string {
			located++
			return ""
		}
		c.push = func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			pushed.Store(true)
			return "sha_nomiss", true, gitops.PushTimings{}, nil
		}
	})
	stepStarted(t, m, 1, "code", filepath.Join(capsuleDir, "session", "session-ra_wi_test.jsonl"), false)

	// two step boundaries: the warning fires ONCE, both pushes proceed.
	m.writeExec(t, 1, "code")
	soon(t, 5*time.Second, "first push never ran", func() bool { return pushed.Load() })
	soon(t, 5*time.Second, "the transcript_missing advisory never fired", func() bool {
		return len(eventsOf(m, EvTranscriptMissing)) == 1
	})
	pushed.Store(false)
	m.writeExec(t, 2, "review")
	soon(t, 5*time.Second, "second push never ran", func() bool { return pushed.Load() })
	if n := len(eventsOf(m, EvTranscriptMissing)); n != 1 {
		t.Fatalf("transcript_missing fired %d times, want exactly once per attempt", n)
	}
	if located == 0 {
		t.Fatal("the on-demand locator must have been consulted")
	}
	if _, err := os.Stat(filepath.Join(capsuleDir, "session", "session-ra_wi_test.jsonl")); !os.IsNotExist(err) {
		t.Fatal("no source means no capsule copy — nothing to fabricate")
	}
}

func TestResumedDerivationWritingInCapsuleIsNotReCopied(t *testing.T) {
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newLiveFixture(t, capsuleDir, filepath.Join(dir, "pi-sessions"))
	// a RESUMED derivation continues the capsule copy itself: src == dst,
	// and the copy already exists (it is the very file the resume came
	// from).
	if err := os.MkdirAll(filepath.Dir(f.dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dst, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	h := newFakeHandle("ra_wi_test")
	var pushed atomic.Bool
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			pushed.Store(true)
			return "sha_resume", true, gitops.PushTimings{}, nil
		}
	})
	stepStarted(t, m, 1, "code", f.dst, true) // the resume source IS the capsule copy

	// stamp a distinct mtime; a needless self-copy would rewrite it.
	before := time.Now().Add(-time.Hour)
	if err := os.Chtimes(f.dst, before, before); err != nil {
		t.Fatal(err)
	}
	m.writeExec(t, 1, "code")
	soon(t, 5*time.Second, "push never ran", func() bool { return pushed.Load() })
	st, err := os.Stat(f.dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(before) {
		t.Fatal("a resumed derivation writing into the capsule must not be re-copied over itself")
	}
}

// The sync face's source read goes through the ONE session-jsonl parser
// (wi_LMSxPTQTtz5x step sync_face_tail): sessionjsonl.CopyReadonly reads the live
// harness transcript via the sessionjsonl package's whole-file tail
// extraction, so the session-jsonl storage format has exactly one
// parsing source in the repo (the stdout-wire parser stream.go is a
// different vocabulary and stays untouched). These regressions pin the
// two faces of that route: byte-identical copies (torn trailing line,
// blank and malformed lines preserved verbatim) and the typed failure
// posture (a source that cannot be read never leaves a destination
// behind).

// TestSyncTranscriptCopyByteIdentical pins the byte-identity regression.
func TestSyncTranscriptCopyByteIdentical(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "live.jsonl")
	dst := filepath.Join(dir, "capsule", "session", "session-ra_wi_x.jsonl")
	body := "{\"type\":\"session\",\"id\":\"s1\"}\n" +
		"\n" + // blank line: preserved verbatim
		"not json at all\n" + // malformed line: preserved verbatim
		"{\"type\":\"message_end\"}\n" +
		"{\"type\":\"spe" // torn trailing line: preserved verbatim
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sessionjsonl.CopyReadonly(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("the synced transcript must be byte-identical to the source:\n got %q\nwant %q", got, body)
	}
	// The copy is byte-verbatim (the shared parser faces were deleted
	// with the audit's dead-code sweep; the byte-identical assertion
	// above is the pin).
	// tmp+rename semantics: no temp residue next to the destination.
	ents, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != "session-ra_wi_x.jsonl" {
			t.Fatalf("unexpected residue next to the synced transcript: %s", e.Name())
		}
	}
}

// TestSyncTranscriptCopyMissingSource keeps the typed failure posture.
func TestSyncTranscriptCopyMissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "session", "out.jsonl")
	if err := sessionjsonl.CopyReadonly(filepath.Join(dir, "absent.jsonl"), dst); err == nil {
		t.Fatal("copying a missing source must fail")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("a failed copy must not leave a destination behind: %v", err)
	}
}
