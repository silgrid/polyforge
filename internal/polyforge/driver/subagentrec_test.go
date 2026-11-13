package driver

// subagentrec_test.go: AC2 — a pi step's subagent
// conversation is recorded into the capsule from the step's own stdout,
// live and complete, beside the conversation's capsule transcript.
//
// testdata/subagents-424/pi-stdout.jsonl is a replay: the stdout records
// pi's subagent tool streams for one call (tool_execution_start, one
// tool_execution_update per child message, tool_execution_end), derived
// from a real recorded call — see testdata/subagents-424/README.md.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const replayCallID = "call_bcb397f329d148e18d52f97f"

func replayLines(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "subagents-424", "pi-stdout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func recorderCapsule(t *testing.T) (string, string) {
	t.Helper()
	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	return capDir, filepath.Join(capDir, capsule.DirSession, "session-tok.jsonl")
}

func readMeta(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func recordCount(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("a recorded line is not whole JSON: %q", l)
		}
		out = append(out, rec)
	}
	return out
}

// The replayed call: mid-run the capsule already holds the conversation so
// far (running), and at the end the whole child conversation — every
// message, the tool calls and their results included, not only the result
// it returned — with the meta naming the parent's tool call, the task and
// the agent.
func TestPiSubagentRecorderReplay(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	lines := replayLines(t)
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	body := filepath.Join(dir, replayCallID+"-0.jsonl")
	meta := filepath.Join(dir, replayCallID+"-0.meta.json")

	// through the third update (start, update×3, an unrelated record)
	for _, l := range lines[:5] {
		rec.Feed(l)
	}
	if got := recordCount(t, body); len(got) != 3 {
		t.Fatalf("mid-run: %d records, want the 3 messages streamed so far", len(got))
	}
	if m := readMeta(t, meta); m["finished"] != false || m["toolUseId"] != replayCallID || m["agentType"] != "merge-conflict-reviewer" {
		t.Fatalf("mid-run meta %v", m)
	}
	for _, l := range lines[5:] {
		rec.Feed(l)
	}
	got := recordCount(t, body)
	if len(got) != 7 {
		t.Fatalf("%d records, want the whole 7-message child conversation", len(got))
	}
	roles := []string{}
	for _, r := range got {
		if r["type"] != "message" {
			t.Fatalf("record type %v", r["type"])
		}
		roles = append(roles, r["message"].(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,toolResult,assistant,toolResult,assistant" {
		t.Fatalf("roles %v", roles)
	}
	if got[2]["timestamp"] != "2026-11-02T13:24:44.446Z" {
		t.Fatalf("the record time is the message's own: %v", got[2]["timestamp"])
	}
	m := readMeta(t, meta)
	if m["finished"] != true || m["exitCode"] != float64(0) || m["isError"] != false ||
		!strings.HasPrefix(m["description"].(string), "Review the merge resolution") || m["index"] != float64(0) {
		t.Fatalf("final meta %v", m)
	}
}

// The recorder's output is the layout and shape the Pi Web progress reader
// is tested against (pi-web/lib/__fixtures__/subagents-424/): the two are
// held byte-equal here, so a change on either side fails one test.
func TestPiSubagentRecorderMatchesThePiWebFixture(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	for _, l := range replayLines(t) {
		rec.Feed(l)
	}
	fixture := filepath.Join("..", "..", "..", "pi-web", "lib", "__fixtures__", "subagents-424", "pi", "session", "session-tok", capsule.DirSubagents)
	for _, name := range []string{replayCallID + "-0.jsonl", replayCallID + "-0.meta.json"} {
		want, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s differs from the Pi Web fixture:\n got %s\nwant %s", name, got, want)
		}
	}
}

// A replay fed twice (a re-sent update) and a recorder that first sees a
// call mid-way (a restarted reader) both leave exactly the conversation —
// nothing doubled.
func TestPiSubagentRecorderNeverDoubles(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	lines := replayLines(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	for _, l := range lines {
		rec.Feed(l)
		rec.Feed(l)
	}
	body := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents, replayCallID+"-0.jsonl")
	if n := len(recordCount(t, body)); n != 7 {
		t.Fatalf("re-sent updates: %d records, want 7", n)
	}
	late := newPiSubagentRecorder(capDir, transcript)
	for _, l := range lines[4:] {
		late.Feed(l)
	}
	if n := len(recordCount(t, body)); n != 7 {
		t.Fatalf("a recorder joining mid-call: %d records, want 7", n)
	}
}

// subagentUpdate is one subagent tool record of the extension's real
// streaming shape: a started task carries exitCode 0 while it runs (the
// extension seeds every started child with 0), a parallel task not yet
// started is the -1 placeholder with no messages.
func subagentUpdate(id, typ, mode string, isError bool, results ...map[string]any) string {
	key := "partialResult"
	if typ == "tool_execution_end" {
		key = "result"
	}
	raw, _ := json.Marshal(map[string]any{
		"type": typ, "toolCallId": id, "toolName": "subagent", "isError": isError,
		key: map[string]any{"details": map[string]any{"mode": mode, "results": results}},
	})
	return string(raw)
}

func childResult(agent string, exitCode int, stopReason string, messages int) map[string]any {
	msgs := []any{}
	for i := 0; i < messages; i++ {
		msgs = append(msgs, map[string]any{"role": "user", "timestamp": 1793625884435 + i})
	}
	r := map[string]any{"agent": agent, "task": "task " + agent, "exitCode": exitCode, "messages": msgs}
	if stopReason != "" {
		r["stopReason"] = stopReason
	}
	return r
}

// Parallel tasks are one file per task. A started task streams exitCode 0
// while it runs, so no task is finished before the call's
// tool_execution_end; a placeholder not yet started gets no file; at the
// end each task's error is its own (exit code, or an error/aborted stop).
// A tool call id with characters outside the file-name set is renamed with
// its hash. Other tools' records and malformed lines record nothing.
func TestPiSubagentRecorderParallelAndNames(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	id := "call_x|fc/../01"
	rec.Feed(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"details":{"results":[{"messages":[{}]}]}}}`)
	rec.Feed(`{"type":"tool_execution_update","toolName":"subagent" garbage`)
	rec.Feed(subagentUpdate(id, "tool_execution_update", "parallel", false, childResult("a", 0, "", 1), childResult("b", -1, "", 0)))
	stem := subagentFileStem(id, 0)
	if strings.ContainsAny(stem, "|/.") || !strings.HasPrefix(stem, "call_x_fc____01_") {
		t.Fatalf("stem %q", stem)
	}
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Fatalf("files %v, want only the started task's transcript and meta", ents)
	}
	// both running, both exitCode 0 — the extension's real mid-run shape
	rec.Feed(subagentUpdate(id, "tool_execution_update", "parallel", false, childResult("a", 0, "stop", 3), childResult("b", 0, "", 2)))
	for i := 0; i < 2; i++ {
		if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, i)+".meta.json")); m["finished"] != false {
			t.Fatalf("task %d is still running (exitCode 0 mid-run), meta %v", i, m)
		}
	}
	rec.Feed(subagentUpdate(id, "tool_execution_end", "parallel", false, childResult("a", 0, "stop", 3), childResult("b", 2, "", 2)))
	if ents, _ := os.ReadDir(dir); len(ents) != 4 {
		t.Fatalf("files %v, want a transcript and a meta per task", ents)
	}
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 0)+".meta.json")); m["finished"] != true || m["isError"] != false {
		t.Fatalf("task A meta %v", m)
	}
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 1)+".meta.json")); m["finished"] != true || m["isError"] != true || m["exitCode"] != float64(2) {
		t.Fatalf("task B meta %v", m)
	}
}

// A recorded parallel call (testdata/subagents-424/pi-parallel-stdout.jsonl,
// two tasks, real stdout of `pi --mode json` with the extension): every
// streamed update shows started tasks with exitCode 0, so both stay
// unfinished through the last update and finish only at the call's end,
// each with its whole 5-message conversation.
func TestPiSubagentRecorderRecordedParallel(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	raw, err := os.ReadFile(filepath.Join("testdata", "subagents-424", "pi-parallel-stdout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	var id string
	end := -1
	for i, l := range lines {
		var probe piToolExecution
		if json.Unmarshal([]byte(l), &probe) == nil && probe.ToolName == "subagent" {
			id = probe.ToolCallID
			if probe.Type == "tool_execution_end" {
				end = i
			}
		}
	}
	if id == "" || end < 0 {
		t.Fatal("the recording holds no subagent call")
	}
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	for _, l := range lines[:end] {
		rec.Feed(l)
	}
	for i := 0; i < 2; i++ {
		if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, i)+".meta.json")); m["finished"] != false {
			t.Fatalf("task %d finished before the call ended: %v", i, m)
		}
	}
	for _, l := range lines[end:] {
		rec.Feed(l)
	}
	for i, want := range []string{"Read a.txt", "Read b.txt"} {
		m := readMeta(t, filepath.Join(dir, subagentFileStem(id, i)+".meta.json"))
		if m["finished"] != true || m["isError"] != false || !strings.Contains(m["description"].(string), want) {
			t.Fatalf("task %d final meta %v", i, m)
		}
		if n := len(recordCount(t, filepath.Join(dir, subagentFileStem(id, i)+".jsonl"))); n != 5 {
			t.Fatalf("task %d: %d records, want its 5 messages", i, n)
		}
	}
}

// A chain stopped by a failed step: the earlier steps finished as soon as
// a later one started, and the call's error is the failed step's only —
// the steps before it stay successful.
func TestPiSubagentRecorderChainErrorIsTheFailedStepOnly(t *testing.T) {
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	id := "call_chain1"
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	rec.Feed(subagentUpdate(id, "tool_execution_update", "chain", false, childResult("a", 0, "stop", 3), childResult("b", 0, "", 1)))
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 0)+".meta.json")); m["finished"] != true || m["isError"] != false {
		t.Fatalf("a completed earlier step: %v", m)
	}
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 1)+".meta.json")); m["finished"] != false {
		t.Fatalf("the running step: %v", m)
	}
	rec.Feed(subagentUpdate(id, "tool_execution_end", "chain", true, childResult("a", 0, "stop", 3), childResult("b", 0, "error", 2)))
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 0)+".meta.json")); m["isError"] != false {
		t.Fatalf("the failed chain marked its successful earlier step failed: %v", m)
	}
	if m := readMeta(t, filepath.Join(dir, subagentFileStem(id, 1)+".meta.json")); m["finished"] != true || m["isError"] != true {
		t.Fatalf("the failed step (stopReason error): %v", m)
	}
}

// A transcript outside the capsule's session/ (the agent-writable journal
// named it) disables the recorder: nothing is written anywhere.
func TestPiSubagentRecorderUntrustedTranscript(t *testing.T) {
	capDir, _ := recorderCapsule(t)
	outside := filepath.Join(t.TempDir(), "session-evil.jsonl")
	rec := newPiSubagentRecorder(capDir, outside)
	for _, l := range replayLines(t) {
		rec.Feed(l)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(outside), "session-evil")); err == nil {
		t.Fatal("the recorder wrote beside an untrusted transcript")
	}
}

// A pi dispatch's stdout tap feeds the recorder beside the dispatch's own
// capsule transcript, alongside the harness translator; a claude dispatch
// gets no recorder (its sidecars are copied from its native session).
func TestSpawnStepRecordsPiSubagentsFromStdout(t *testing.T) {
	capDir := t.TempDir()
	writeExtLane(t, capDir, nil)
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirSession), 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &captureSpawner{}
	link := supervise.NewLink("wi_sp")
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:      WIInfo{ID: "wi_sp", CapsuleDir: capDir, BundleName: "autopilot-x", SessionHandle: "ab12cd34"},
		Spawner: sp, Link: link, PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	for _, l := range replayLines(t) {
		p.OnRecord(l)
	}
	dir := capsule.SubagentDir(p.TranscriptPath)
	if n := len(recordCount(t, filepath.Join(dir, replayCallID+"-0.jsonl"))); n != 7 {
		t.Fatalf("the dispatch's stdout recorded %d child messages, want 7", n)
	}
	if s := link.Tracker(1).Snapshot(time.Now()); s.WorkSignals == 0 {
		t.Fatalf("the translator must still see the records: %+v", s)
	}
}
