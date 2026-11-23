package driver

// autoanswer_review_test.go: the review-round findings on the automatic
// reply (polyforge#239) — the owner relay and the automatic commit are one
// serialized read-check-append; the Cell switch and timeout drive the
// behaviour at the next judgement; a restart rebuilds the audit event; and
// the automatic reply exists only on the step-ask path (an analysis task,
// which runs as a task, cannot reach it).

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The owner's relay and the automatic commit share askWriteMu: an owner
// reply that lands between the automatic check's first read and its commit
// closes the ask, so the automatic commit writes nothing — never two
// replies for one ask.
func TestAutoAnswerOwnerRelayIsSerialized(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clock := &autoAnswerClock{at: base}
	ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
	d, _, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
	d.cfg.Now = clock.now
	d.cfg.LaneVerifiable = func(string) bool { return true }
	var once sync.Once
	d.cfg.AutoAnswerBeforeCommit = func() {
		once.Do(func() {
			if _, err := d.OwnerMessage(context.Background(), "owner says", "alice"); err != nil {
				t.Errorf("the interleaved owner relay: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	clock.advance(31 * time.Minute)
	waitUntil(t, "the owner reply", func() bool {
		for _, l := range autoAnswerLines(t, dir) {
			if l.Kind == capsule.AskKindReply && l.By == "alice" && l.AskAt == ask.At {
				return true
			}
		}
		return false
	})
	waitUntil(t, "the resumed dispatch", func() bool { return len(sp.dispatched()) >= 1 })
	var ownerReplies, autoRepliesCount int
	for _, l := range autoAnswerLines(t, dir) {
		if l.Kind == capsule.AskKindReply && l.AskAt == ask.At && l.By == "alice" {
			ownerReplies++
		}
		if l.Kind == capsule.AskKindReply && l.AskAt == ask.At && l.By == capsule.AutoAnswerBy {
			autoRepliesCount++
		}
	}
	if ownerReplies != 1 || autoRepliesCount != 0 {
		t.Fatalf("replies for the ask: owner %d, automatic %d; want 1 and 0", ownerReplies, autoRepliesCount)
	}
}

// The reverse order: the automatic reply lands first; a later owner message
// is an interjection, not a second reply.
func TestAutoAnswerCommitFirstThenOwnerIsAMessage(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clock := &autoAnswerClock{at: base}
	ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
	d, _, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
	d.cfg.Now = clock.now
	d.cfg.LaneVerifiable = func(string) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	clock.advance(31 * time.Minute)
	waitUntil(t, "the automatic reply", func() bool { return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1 })
	if _, err := d.OwnerMessage(ctx, "after the automatic reply", "alice"); err != nil {
		t.Fatalf("owner message after the automatic reply: %v", err)
	}
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 1 {
		t.Fatalf("replies for the ask = %+v, want only the automatic one", got)
	}
	var messages int
	for _, l := range autoAnswerLines(t, dir) {
		if l.Kind == capsule.AskKindMessage && l.By == "alice" {
			messages++
		}
	}
	if messages != 1 {
		t.Fatalf("owner messages = %d, want the later interjection recorded once", messages)
	}
}

func boolPtr(v bool) *bool { return &v }

// The Cell switch and timeout are read at every judgement: off stays
// silent, on answers at the next tick, and a longer timeout moves the
// deadline while the reply still reports the real wait from the original
// T0.
func TestAutoAnswerConfigDrivesBehavior(t *testing.T) {
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	t.Run("switch off then on", func(t *testing.T) {
		dir := t.TempDir()
		clock := &autoAnswerClock{at: base}
		ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
		d, _, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
		d.cfg.Now = clock.now
		d.cfg.LaneVerifiable = func(string) bool { return true }
		var mu sync.Mutex
		lim := supervise.Defaults{AskWarmWindow: 150 * time.Millisecond, AskAutoReplyEnabled: boolPtr(false)}
		d.cfg.LimitsFunc = func() supervise.Defaults { mu.Lock(); defer mu.Unlock(); return lim }
		var checks atomic.Int64
		d.cfg.AutoAnswerChecked = func() { checks.Add(1) }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = d.Run(ctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		})
		clock.advance(60 * time.Minute)
		waitUntil(t, "the disabled checks", func() bool { return checks.Load() >= 5 })
		if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
			t.Fatalf("a disabled switch answered: %+v", got)
		}
		mu.Lock()
		on := true
		lim.AskAutoReplyEnabled = &on
		mu.Unlock()
		waitUntil(t, "the reply after the switch is on", func() bool { return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1 })
		if reply := autoReplies(autoAnswerLines(t, dir), ask.At)[0]; !strings.Contains(reply.Text, "超时 1h0m0s") {
			t.Fatalf("reply %q, want the real 60m wait", reply.Text)
		}
	})

	t.Run("a longer timeout moves the deadline from the original T0", func(t *testing.T) {
		dir := t.TempDir()
		clock := &autoAnswerClock{at: base}
		ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
		d, _, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
		d.cfg.Now = clock.now
		d.cfg.LaneVerifiable = func(string) bool { return true }
		lim := supervise.Defaults{AskWarmWindow: 150 * time.Millisecond, AskAutoReplyTimeout: 45 * time.Minute}
		d.cfg.LimitsFunc = func() supervise.Defaults { return lim }
		var checks atomic.Int64
		d.cfg.AutoAnswerChecked = func() { checks.Add(1) }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = d.Run(ctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		})
		clock.advance(31 * time.Minute)
		waitUntil(t, "the 31m checks", func() bool { return checks.Load() >= 5 })
		if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
			t.Fatalf("answered before the 45m deadline: %+v", got)
		}
		clock.advance(14 * time.Minute) // T0+45m
		waitUntil(t, "the 45m reply", func() bool { return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1 })
		if reply := autoReplies(autoAnswerLines(t, dir), ask.At)[0]; !strings.Contains(reply.Text, "超时 45m0s") {
			t.Fatalf("reply %q, want the real 45m wait", reply.Text)
		}
	})
}

// A crash after the automatic reply's append may have lost its event: a
// restart re-emits it from the reply (the authority), and never appends a
// second reply. The decision feed dedupes the re-emission by question id.
func TestAutoAnswerReplayAudit(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
	reply := capsule.AskLine{
		At: base.Add(31 * time.Minute).UTC().Format(time.RFC3339Nano), Step: "fix", Spawn: 1,
		Kind: capsule.AskKindReply, AskAt: ask.At, By: capsule.AutoAnswerBy,
		Text: autoAnswerReplyText([]string{"A：JSON", "B：两次"}, 31*time.Minute),
	}
	if err := capsule.AppendAsk(dir, reply); err != nil {
		t.Fatal(err)
	}
	d, log, _, _ := askEnv(t, dir, &fakeSpawner{}, nil, 150*time.Millisecond)
	d.replayAutoAnswerAudit()
	events := log.byKind(EvAutoAnswered)
	if len(events) != 1 || events[0].body["ask_at"] != ask.At || events[0].body["elapsed"] != "31m0s" {
		t.Fatalf("replayed events = %v", events)
	}
	d.replayAutoAnswerAudit()
	if got := len(log.byKind(EvAutoAnswered)); got != 2 {
		t.Fatalf("a second replay emitted %d events, want the same event again (the feed dedupes)", got)
	}
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 1 {
		t.Fatalf("the replay appended another reply: %+v", got)
	}
}

// The automatic reply exists only on the step-ask path: autoAnswer is called
// from ask.go and supervision.go alone, and the task package (the analysis
// confirmation path) imports neither the driver nor the ask record — so an
// analysis task structurally cannot reach automatic answering.
func TestAutoAnswerOnlyRunsOnStepAskPaths(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sites := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "autoAnswer" {
				sites[filepath.Base(f)] = true
			}
			return true
		})
	}
	if len(sites) != 2 || !sites["ask.go"] || !sites["supervision.go"] {
		t.Fatalf("autoAnswer call sites = %v, want ask.go and supervision.go alone", sites)
	}

	taskFiles, err := filepath.Glob("../task/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range taskFiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if strings.Contains(im.Path.Value, "/internal/polyforge/driver") {
				t.Errorf("%s imports the driver: an analysis task cannot reach autoAnswer", filepath.Base(f))
			}
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"capsule.AppendAsk", "AskKindAsk", "EvAwaitingOwner", "autoAnswer"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s names %s: the task path never writes an ask or calls autoAnswer", filepath.Base(f), bad)
			}
		}
	}
}
