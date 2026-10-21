package observe

// events_test.go: #21 — the seq is a per-WI MONOTONIC append counter,
// persisted on every line of the work item's own file, surviving daemon
// restarts; the SinceAfter cursor filters on it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSinceAfterCursor(t *testing.T) {
	l, err := NewEventLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 5; i++ {
		_ = l.Append("wi_a", "k", map[string]int{"i": i})
	}
	_ = l.Append("wi_b", "k", nil)
	_ = l.Append("", "machine", nil)
	got := l.SinceAfter("wi_a", 2)
	if len(got) != 3 || got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("after=2: %+v, want seqs 3..5", got)
	}
	if got := l.SinceAfter("wi_a", 5); len(got) != 0 {
		t.Fatalf("after=last: got %d entries, want 0", len(got))
	}
	if got := l.SinceAfter("wi_b", 0); len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("per-WI numbering: wi_b %+v, want its own seq 1", got)
	}
	if got := l.SinceAfter("", 0); len(got) != 1 || got[0].Kind != "machine" {
		t.Fatalf("daemon scope: %+v", got)
	}
	if got := l.SinceAfter("wi_unknown", 0); len(got) != 0 {
		t.Fatalf("unknown WI: got %d entries, want 0", len(got))
	}
	if n := l.LastSeq("wi_a"); n != 5 {
		t.Fatalf("LastSeq = %d, want 5", n)
	}
}

// A restarted daemon continues every work item's numbering from its file
// (a watcher's ?after cursor keeps working across the restart).
func TestEventSeqMonotonicAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	l, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = l.Append("wi_r", "k", nil)
	}
	_ = l.Close()
	l2, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n := l2.LastSeq("wi_r"); n != 3 {
		t.Fatalf("LastSeq after reopen = %d, want 3", n)
	}
	_ = l2.Append("wi_r", "k", nil)
	got := l2.SinceAfter("wi_r", 2)
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("after reopen: %+v, want seqs 3, 4", got)
	}
}

// One ?after answer is bounded; the next cursor continues.
func TestSinceAfterCapsOneAnswer(t *testing.T) {
	l, err := NewEventLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < HistoryCap+10; i++ {
		_ = l.Append("wi_h", "k", nil)
	}
	got := l.SinceAfter("wi_h", 0)
	if len(got) != HistoryCap || got[0].Seq != 1 {
		t.Fatalf("got %d entries from seq %d, want %d from 1", len(got), got[0].Seq, HistoryCap)
	}
	if rest := l.SinceAfter("wi_h", got[len(got)-1].Seq); len(rest) != 10 {
		t.Fatalf("the next cursor serves %d entries, want 10", len(rest))
	}
}

// §五: an events-file failure is a degradation only — it is reported and
// consumes no seq.
func TestEventAppendFailureConsumesNoSeq(t *testing.T) {
	dir := t.TempDir()
	l, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	blocker := filepath.Join(dir, EventsDir, eventsFileName("wi_f"))
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.Append("wi_f", "k", nil); err == nil {
		t.Fatal("an unwritable events file must report the failure")
	}
	if n := l.LastSeq("wi_f"); n != 0 {
		t.Fatalf("a failed append consumed seq %d", n)
	}
	_ = os.Remove(blocker)
	if err := l.Append("wi_f", "k", nil); err != nil {
		t.Fatal(err)
	}
	if got := l.SinceAfter("wi_f", 0); len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("after the failure: %+v, want seq 1", got)
	}
}

// A work item silent past the retention loses its file at open; others
// keep theirs.
func TestSilentWorkItemFilesDroppedAtOpen(t *testing.T) {
	dir := t.TempDir()
	l, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Append("wi_old", "k", nil)
	_ = l.Append("wi_new", "k", nil)
	_ = l.Close()
	old := time.Now().Add(-eventsRetention - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, EventsDir, eventsFileName("wi_old")), old, old); err != nil {
		t.Fatal(err)
	}
	l2, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n := l2.LastSeq("wi_old"); n != 0 {
		t.Fatalf("a silent work item's file survived the retention (seq %d)", n)
	}
	if n := l2.LastSeq("wi_new"); n != 1 {
		t.Fatalf("a recent work item's file was dropped (seq %d)", n)
	}
}

// An event line of any length keeps the numbering: after a restart the
// seq continues past it and the cursor serves what follows it.
func TestLongEventLineKeepsTheNumbering(t *testing.T) {
	dir := t.TempDir()
	l, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Append("wi_long", "k", map[string]string{"text": strings.Repeat("x", 2<<20)})
	_ = l.Append("wi_long", "k", nil)
	_ = l.Close()
	l2, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n := l2.LastSeq("wi_long"); n != 2 {
		t.Fatalf("LastSeq after reopen = %d, want 2", n)
	}
	_ = l2.Append("wi_long", "k", nil)
	if got := l2.SinceAfter("wi_long", 1); len(got) != 2 || got[1].Seq != 3 {
		t.Fatalf("after the long line: %+v, want seqs 2, 3", got)
	}
}

// An idle work item's last seq is learned from its file once, not on
// every poll; an id without a file adds nothing.
func TestLastSeqIsLearnedOnce(t *testing.T) {
	dir := t.TempDir()
	l, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Append("wi_idle", "k", nil)
	_ = l.Close()
	l2, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n := l2.LastSeq("wi_idle"); n != 1 {
		t.Fatalf("LastSeq = %d, want 1", n)
	}
	if err := os.WriteFile(filepath.Join(dir, EventsDir, eventsFileName("wi_idle")), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := l2.LastSeq("wi_idle"); n != 1 {
		t.Fatalf("LastSeq re-read the file: %d", n)
	}
	_ = l2.LastSeq("wi_nofile")
	if len(l2.logs) != 1 {
		t.Fatalf("file table: %d entries, want only wi_idle", len(l2.logs))
	}
}
