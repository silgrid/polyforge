package supervise

// replay_test.go: the D15 replay verification (polyforge#147 审计减法 A,
// spec Requirement "D15 MUST 先用真实采集的 pi 会话记录回放验证"). The
// fixture is a REAL collected pi session record stream — this work item's
// own step sessions (capsule session/session-*.jsonl, the actual
// `pf daemon`-driven step flows), never hand-built. The replay drives the
// real records through the existing judgment path — the standard Sink
// signals folded into a Tracker, judged by rules.go's Judge — tick by
// tick at the records' own timestamps, and asserts what the existing
// rules decide over the real agent_end / multi-turn / long-wait order.
//
// Per the spec: a no-misjudgment verdict writes ONLY the conclusion
// (artifacts/d15-replay-conclusion.md in the capsule) and changes no
// judgment code; this test stays as the fixture's parse-path guard.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// replayFixturePath is the real collected pi session record stream.
func replayFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "pi-session-replay.jsonl")
}

// replayClock is the replay's clock: it follows the records' own
// timestamps, so Judge is evaluated at each real tick.
type replayClock struct{ now time.Time }

func (c *replayClock) get() time.Time { return c.now }

// replaySignal derives the standard Sink signals of one REAL
// session-storage record (the session JSONL vocabulary: session /
// model_change / thinking_level_change / message / custom_message). The
// mapping mirrors what the RPC translator produces for the equivalent
// real event: model replies produce output, toolUse replies start their
// tool calls, toolResult records end them, a normally ended turn closes
// the turn, user prompts open the next model wait; session metadata and
// non-work records are noise.
func replaySignal(t *testing.T, s Sink, raw []byte) {
	t.Helper()
	var rec struct {
		Type      string          `json:"type"`
		Message   json.RawMessage `json:"message"`
		Timestamp string          `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("replay record is not valid JSON: %v (%s)", err, string(raw))
	}
	switch rec.Type {
	case "session", "model_change", "thinking_level_change", "custom_message":
		s.Noise() // session metadata / non-work records
	case "message":
		var msg struct {
			Role       string          `json:"role"`
			StopReason string          `json:"stopReason"`
			Content    json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(rec.Message, &msg); err != nil {
			t.Fatalf("replay message record undecodable: %v (%s)", err, string(rec.Message))
		}
		switch msg.Role {
		case "system":
			s.Noise() // boot context
		case "user":
			s.Alive()        // a new turn's prompt arrived (work)
			s.WaitingModel() // the next model call is owed
		case "assistant":
			s.ModelOutput(OutputText)
			switch msg.StopReason {
			case "toolUse":
				var blocks []struct {
					Type string          `json:"type"`
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"arguments"`
				}
				if len(msg.Content) > 0 && msg.Content[0] == '[' {
					if err := json.Unmarshal(msg.Content, &blocks); err != nil {
						t.Fatalf("replay toolCall blocks undecodable: %v", err)
					}
				}
				for _, b := range blocks {
					if b.Type == "toolCall" {
						s.ToolStart(b.ID, b.Name, string(b.Args))
					}
				}
			case "stop":
				s.TurnEnded(true, "") // the turn ended normally
			}
		case "toolResult":
			s.ToolEnd(toolCallIDOf(t, rec.Message), "")
		default:
			s.Noise()
		}
	default:
		s.Noise()
	}
}

// toolCallIDOf reads the toolCallId of a toolResult record.
func toolCallIDOf(t *testing.T, raw []byte) string {
	t.Helper()
	var m struct {
		ToolCallID string `json:"toolCallId"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("toolResult record undecodable: %v", err)
	}
	return m.ToolCallID
}

// TestD15ReplayRealSessionRecords: the real collected session stream is
// replayed through the existing judgment path (Sink signals → Tracker →
// Judge) at the records' own timestamps. The session-storage format is
// message-granular, so the replay judges under the production
// message-granularity default silence bound (30m); Y and T take the
// production defaults. The assertion: the existing rules produce NO kill
// verdict at any real tick of any real dispatch — the real multi-turn,
// toolUse/toolResult-interleaved order (including the stream's real
// 15-minute inter-record waits) is not misjudged.
func TestD15ReplayRealSessionRecords(t *testing.T) {
	raw, err := os.ReadFile(replayFixturePath(t))
	if err != nil {
		t.Fatalf("SYMPTOM: the D15 replay fixture is missing or unreadable (%v): the replay path cannot be exercised without the real collected record stream", err)
	}
	var lines [][]byte
	for _, line := range splitJSONLines(raw) {
		lines = append(lines, line)
	}
	if len(lines) < 10 {
		t.Fatalf("replay fixture carries only %d records — too thin to cover the real multi-turn order", len(lines))
	}

	def := Defaults{}.Fill()
	th := Thresholds{
		Silence:     def.SilenceFor(GranularityMessage), // the session JSONL is message-granular
		Tool:        def.Tool,
		StepTimeout: def.StepTimeout,
	}

	spawnN := int64(0)
	clock := &replayClock{}
	var tr *Tracker
	for _, line := range lines {
		// Each real dispatch's session begins with the session record.
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			t.Fatalf("replay record is not valid JSON: %v", err)
		}
		if kind.Type == "session" {
			spawnN++
			tr = newTracker(spawnN, clock.get)
			ts := recordTime(t, line)
			clock.now = ts
			tr.Begin(StepStarted{Step: "replay", SpawnN: spawnN, StartedAt: ts, Thresholds: th})
		}
		if tr == nil {
			t.Fatalf("replay record before the first session record: %s", string(line))
		}
		clock.now = recordTime(t, line)
		replaySignal(t, tr, line)
		// Tick the judgment at this real instant.
		if v := Judge(tr.Snapshot(clock.now), clock.now, def.ModelErrorGrace); v != nil {
			t.Fatalf("the existing judgment fired %q (%s) at a real tick of dispatch %d — the real pi event order was misjudged",
				v.Cause, v.Evidence, spawnN)
		}
	}

	// Positive control (not vacuous): a dedicated pass replays up to the
	// stream's first normally ended turn and ticks past the grace — the
	// existing rules judge an ended turn with no result as
	// ended_without_result, the kept semantic.
	if v := replayUntilFirstNormalTurnEnd(t, th); v == nil || v.Cause != CauseEndedWithoutResult {
		t.Fatalf("the ended-turn positive control did not judge ended_without_result: %+v", v)
	}
}

// replayUntilFirstNormalTurnEnd replays the real stream up to (and
// including) its first normally ended turn, then ticks the judgment past
// every grace at that real instant. The pass ends at the jump, so the
// tracker's monotonic stamps are never rewound.
func replayUntilFirstNormalTurnEnd(t *testing.T, th Thresholds) *StepDead {
	t.Helper()
	raw, err := os.ReadFile(replayFixturePath(t))
	if err != nil {
		t.Fatalf("the D15 replay fixture is missing or unreadable (%v)", err)
	}
	clock := &replayClock{}
	var tr *Tracker
	for _, line := range splitJSONLines(raw) {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			t.Fatalf("replay record is not valid JSON: %v", err)
		}
		if kind.Type == "session" {
			ts := recordTime(t, line)
			clock.now = ts
			tr = newTracker(1, clock.get)
			tr.Begin(StepStarted{Step: "replay", SpawnN: 1, StartedAt: ts, Thresholds: th})
			continue
		}
		if tr == nil {
			continue
		}
		clock.now = recordTime(t, line)
		if kind.Type == "message" {
			var msg struct {
				Role       string `json:"role"`
				StopReason string `json:"stopReason"`
			}
			_ = json.Unmarshal(extractMessage(t, line), &msg)
			if msg.Role == "assistant" && msg.StopReason == "stop" {
				// Feed the turn end, then tick past every grace and bound at
				// that real instant: an ended turn with no result follows is
				// judged ended_without_result. The pass ends at the jump, so
				// the tracker's monotonic stamps are never rewound.
				replaySignal(t, tr, line)
				clock.now = clock.now.Add(time.Hour)
				return Judge(tr.Snapshot(clock.now), clock.now, def_ModelErrorGrace())
			}
		}
		replaySignal(t, tr, line)
	}
	return nil
}

// def_ModelErrorGrace answers the production default model-error grace.
func def_ModelErrorGrace() time.Duration { return DefaultModelErrorGrace }

// extractMessage reads the nested message object of a message record.
func extractMessage(t *testing.T, line []byte) []byte {
	t.Helper()
	var rec struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &rec); err != nil {
		t.Fatalf("replay record is not valid JSON: %v", err)
	}
	return rec.Message
}

// TestD15ReplayRealGapsAreJudged: the no-kill result above is not vacuous —
// the same real stream replayed with a tool window INJECTED below the
// stream's real 15-minute inter-record tool wait trips the tool rule at
// that real gap (the injected tighter bound is the documented operator
// mechanism): the rules genuinely judge the real gaps; the no-kill at the
// production 30m default is correct, not blind.
func TestD15ReplayRealGapsAreJudged(t *testing.T) {
	raw, err := os.ReadFile(replayFixturePath(t))
	if err != nil {
		t.Fatalf("SYMPTOM: the D15 replay fixture is missing or unreadable (%v)", err)
	}
	def := Defaults{}.Fill()
	th := Thresholds{
		Silence:     def.SilenceFor(GranularityMessage),
		Tool:        10 * time.Minute, // below the stream's real 15-minute tool wait
		StepTimeout: def.StepTimeout,
	}

	spawnN := int64(0)
	clock := &replayClock{}
	var tr *Tracker
	fired := ""
	for _, line := range splitJSONLines(raw) {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			t.Fatalf("replay record is not valid JSON: %v", err)
		}
		if kind.Type == "session" {
			spawnN++
			tr = newTracker(spawnN, clock.get)
			ts := recordTime(t, line)
			clock.now = ts
			tr.Begin(StepStarted{Step: "replay", SpawnN: spawnN, StartedAt: ts, Thresholds: th})
		}
		if tr == nil {
			continue
		}
		clock.now = recordTime(t, line)
		replaySignal(t, tr, line)
		if v := Judge(tr.Snapshot(clock.now), clock.now, def.ModelErrorGrace); v != nil {
			fired = string(v.Cause)
			break
		}
	}
	if fired == "" {
		t.Fatalf("the real stream's 15-minute inter-record gap did not trip any rule at the token-granularity silence bound — the replay asserts nothing")
	}
}

// recordTime parses one record's RFC3339 timestamp.
func recordTime(t *testing.T, line []byte) time.Time {
	t.Helper()
	var rec struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &rec); err != nil || rec.Timestamp == "" {
		t.Fatalf("replay record carries no RFC3339 timestamp: %s", string(line))
	}
	ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil {
		t.Fatalf("replay record timestamp %q unparsable: %v", rec.Timestamp, err)
	}
	return ts
}

// splitJSONLines splits a JSONL fixture into its non-empty lines.
func splitJSONLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == '\n' {
			line := trimSpaceBytes(raw[start:i])
			if len(line) > 0 {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpaceBytes(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
