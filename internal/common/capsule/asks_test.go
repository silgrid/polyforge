package capsule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ask line's wire shape: exactly {at, step, spawn, kind, text} on an
// ask (no empty optional key), and {at, kind, ask_at, text, by} on a
// reply; appended lines read back in order; fsynced, append-only.
func TestAsksAppendAndReadShape(t *testing.T) {
	dir := t.TempDir()
	if lines, err := ReadAsks(dir); lines != nil || err != nil {
		t.Fatalf("absent record: %v %v", lines, err)
	}
	ask := AskLine{At: "2026-11-10T20:00:00.000000001Z", Step: "fix", Spawn: 3, Kind: AskKindAsk, Text: "lean or compatible?"}
	if err := AppendAsk(dir, ask); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, AsksJSONL))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"at", "step", "spawn", "kind", "text"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("ask line misses %q: %s", k, raw)
		}
	}
	for _, k := range []string{"ask_at", "by", "question_id"} {
		if _, ok := keys[k]; ok {
			t.Fatalf("ask line carries an empty %q: %s", k, raw)
		}
	}
	if lines, _ := ReadAsks(dir); len(lines) != 1 || lines[0] != ask {
		t.Fatalf("read back %+v", lines)
	}
	if got := OpenAsk(mustRead(t, dir), "fix", 3); got == nil || got.At != ask.At {
		t.Fatalf("the ask is open: %+v", got)
	}
	reply := AskLine{At: "2026-11-10T20:05:00Z", Kind: AskKindReply, AskAt: ask.At, Text: "lean", By: "owner"}
	if err := AppendAsk(dir, reply); err != nil {
		t.Fatal(err)
	}
	lines := mustRead(t, dir)
	if len(lines) != 2 || lines[1] != reply {
		t.Fatalf("the reply read back: %+v", lines)
	}
	if got := OpenAsk(lines, "fix", 3); got != nil {
		t.Fatalf("a replied ask stays open: %+v", got)
	}
	if err := AppendAsk(dir, AskLine{Kind: AskKindAsk}); err == nil {
		t.Fatal("a line without its stamp must be refused")
	}
}

// An ask belongs to one dispatch (step, spawn): another spawn or step
// never sees it; a reply naming another stamp closes nothing; the newest
// ask of the dispatch decides.
func TestOpenAskKeyedByDispatchAndStamp(t *testing.T) {
	lines := []AskLine{
		{At: "t1", Step: "fix", Spawn: 3, Kind: AskKindAsk, Text: "q1"},
		{At: "t2", Kind: AskKindReply, AskAt: "t0", Text: "stray"},
	}
	if got := OpenAsk(lines, "fix", 3); got == nil || got.At != "t1" {
		t.Fatalf("a reply to another stamp closes nothing: %+v", got)
	}
	if OpenAsk(lines, "fix", 4) != nil || OpenAsk(lines, "review", 3) != nil {
		t.Fatal("another dispatch inherits no ask")
	}
	lines = append(lines, AskLine{At: "t3", Kind: AskKindReply, AskAt: "t1", Text: "a1"})
	if OpenAsk(lines, "fix", 3) != nil {
		t.Fatal("replied")
	}
	lines = append(lines, AskLine{At: "t4", Step: "fix", Spawn: 3, Kind: AskKindAsk, Text: "q2"})
	if got := OpenAsk(lines, "fix", 3); got == nil || got.At != "t4" {
		t.Fatalf("a second ask after the reply is open: %+v", got)
	}
	// a message line (a live interjection) closes nothing
	lines = append(lines, AskLine{At: "t5", Kind: AskKindMessage, Text: "fyi"})
	if got := OpenAsk(lines, "fix", 3); got == nil || got.At != "t4" {
		t.Fatalf("a message closes nothing: %+v", got)
	}
}

// A torn trailing line (a crash mid-append) is skipped by the reader and
// never swallows the next append: the new line starts on its own line.
func TestAsksTornTrailingLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, AsksJSONL)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	torn := `{"at":"t1","step":"fix","spawn":1,"kind":"ask","text":"q1"}` + "\n" + `{"at":"t2","step":"fix","sp`
	if err := os.WriteFile(p, []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}
	if lines := mustRead(t, dir); len(lines) != 1 || lines[0].At != "t1" {
		t.Fatalf("torn line not skipped: %+v", lines)
	}
	if err := AppendAsk(dir, AskLine{At: "t3", Step: "fix", Spawn: 2, Kind: AskKindAsk, Text: "q3"}); err != nil {
		t.Fatal(err)
	}
	lines := mustRead(t, dir)
	if len(lines) != 2 || lines[1].At != "t3" || lines[1].Spawn != 2 {
		t.Fatalf("the append after a torn line was lost: %+v", lines)
	}
}

// OpenAskOf reads the current dispatch off the execution: the current
// step in_progress under its dispatch ordinal — anything else is no
// asking dispatch.
func TestOpenAskOfCurrentDispatch(t *testing.T) {
	dir := t.TempDir()
	if err := AppendAsk(dir, AskLine{At: "t1", Step: "fix", Spawn: 3, Kind: AskKindAsk, Text: "q"}); err != nil {
		t.Fatal(err)
	}
	ex := &Execution{WorkflowStatus: WorkflowStatusRunning, CurrentStep: "fix",
		Steps: map[string]*Step{"fix": {Status: StepInProgress, Dispatch: 3}}}
	if got := OpenAskOf(dir, ex); got == nil || got.At != "t1" {
		t.Fatalf("the current dispatch's ask: %+v", got)
	}
	ex.Steps["fix"].Dispatch = 4
	if OpenAskOf(dir, ex) != nil {
		t.Fatal("another dispatch ordinal")
	}
	ex.Steps["fix"].Dispatch = 3
	ex.WorkflowStatus = WorkflowStatusBlocked
	if OpenAskOf(dir, ex) != nil {
		t.Fatal("a blocked workflow asks nothing")
	}
	ex.WorkflowStatus = WorkflowStatusRunning
	ex.Steps["fix"].Status = StepDone
	if OpenAskOf(dir, ex) != nil {
		t.Fatal("a done step asks nothing")
	}
	if OpenAskOf(dir, nil) != nil {
		t.Fatal("no execution")
	}
}

func mustRead(t *testing.T, dir string) []AskLine {
	t.Helper()
	lines, err := ReadAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// The relay's lines (PR 6d): a reply and a message name the step they
// address; a delivered line names its message by `msg_at` (wire shape
// `{at, step, spawn, kind, msg_at}`, no text); Undelivered answers, per
// step, the reply and message lines no delivered line names — never a
// v1 answer mirrored as a reply (no step), never another step's — and
// stays empty once every message has its delivered line.
func TestAsksDeliveredAndUndelivered(t *testing.T) {
	dir := t.TempDir()
	ask := AskLine{At: "2026-11-10T20:00:00.000000001Z", Step: "fix", Spawn: 1, Kind: AskKindAsk, Text: "lean or compatible?"}
	reply := AskLine{At: "2026-11-10T20:05:00Z", Step: "fix", Spawn: 1, Kind: AskKindReply, AskAt: ask.At, Text: "lean", By: "usr_root"}
	msg := AskLine{At: "2026-11-10T20:06:00Z", Step: "fix", Spawn: 1, Kind: AskKindMessage, Text: "and keep the tests", By: "usr_root"}
	v1 := AskLine{At: "2026-11-10T20:07:00Z", Kind: AskKindReply, AskAt: "2026-11-10T19:00:00Z", Text: "a v1 answer", By: "usr_root"}
	other := AskLine{At: "2026-11-10T20:08:00Z", Step: "review", Spawn: 3, Kind: AskKindMessage, Text: "for review"}
	for _, l := range []AskLine{ask, reply, msg, v1, other} {
		if err := AppendAsk(dir, l); err != nil {
			t.Fatal(err)
		}
	}
	lines := mustRead(t, dir)
	if u := Undelivered(lines, "fix"); len(u) != 2 || u[0].At != reply.At || u[1].At != msg.At {
		t.Fatalf("undelivered of fix: %+v", u)
	}
	if u := Undelivered(lines, "review"); len(u) != 1 || u[0].At != other.At {
		t.Fatalf("undelivered of review: %+v", u)
	}
	if u := Undelivered(lines, "publish"); u != nil {
		t.Fatalf("undelivered of a step nobody wrote to: %+v", u)
	}
	delivered := AskLine{At: "2026-11-10T20:09:00Z", Step: "fix", Spawn: 1, Kind: AskKindDelivered, MsgAt: reply.At}
	if err := AppendAsk(dir, delivered); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, AsksJSONL))
	if err != nil {
		t.Fatal(err)
	}
	all := strings.TrimSpace(string(raw))
	last := all[strings.LastIndex(all, "\n")+1:]
	var keys map[string]any
	if err := json.Unmarshal([]byte(last), &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"at", "step", "spawn", "kind", "msg_at"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("delivered line misses %q: %s", k, last)
		}
	}
	for _, k := range []string{"text", "ask_at", "by"} {
		if _, ok := keys[k]; ok {
			t.Fatalf("delivered line carries an empty %q: %s", k, last)
		}
	}
	lines = mustRead(t, dir)
	if u := Undelivered(lines, "fix"); len(u) != 1 || u[0].At != msg.At {
		t.Fatalf("after the reply's delivery: %+v", u)
	}
	if err := AppendAsk(dir, AskLine{At: "2026-11-10T20:10:00Z", Step: "fix", Spawn: 2, Kind: AskKindDelivered, MsgAt: msg.At}); err != nil {
		t.Fatal(err)
	}
	if u := Undelivered(mustRead(t, dir), "fix"); len(u) != 0 {
		t.Fatalf("after every delivery: %+v", u)
	}
	// a reply closes the ask whatever its delivery state
	if open := OpenAsk(mustRead(t, dir), "fix", 1); open != nil {
		t.Fatalf("a replied ask reads open: %+v", open)
	}
}
