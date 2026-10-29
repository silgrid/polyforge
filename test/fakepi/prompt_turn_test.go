package main

// prompt_turn_test.go: a spawn scripted with an error_turn or end_turn
// answers its prompt with that turn ITSELF — one turn, the way pi answers
// a prompt — and never with a greeting turn first. The greeting used to
// end normally ahead of the scripted turn, which the drive loop emitted
// only after its boot settle and a journal write; under load that gap ate
// the supervisor's rule-1 grace and the greeting was judged
// ended_without_result instead of the scripted error being judged
// model_error.

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// capture redirects the record stream for the test.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := stdout
	stdout = &buf
	t.Cleanup(func() { stdout = prev })
	t.Chdir(t.TempDir()) // the journal and prompt files land here
	return &buf
}

// turns answers the agent_end records of the stream: each one's stop
// reason ("" = ended normally) and assistant text.
func turns(t *testing.T, out string) (stops, texts []string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec struct {
			Type     string `json:"type"`
			Messages []struct {
				StopReason string `json:"stopReason"`
				Content    []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "agent_end" || len(rec.Messages) == 0 {
			continue
		}
		last := rec.Messages[len(rec.Messages)-1]
		text := ""
		if len(last.Content) > 0 {
			text = last.Content[0].Text
		}
		stops, texts = append(stops, last.StopReason), append(texts, text)
	}
	return stops, texts
}

func envFor(act planAction) *driveEnv {
	act.Do = "envelope"
	return &driveEnv{spawnN: 1, plan: &plan{Actions: []planAction{act}}}
}

func TestErrorTurnAnswersThePromptWithoutAGreeting(t *testing.T) {
	out := capture(t)
	d := envFor(planAction{ID: "grill", ErrorTurn: "402 insufficient balance"})
	d.answerPrompt("p1", "do the step")
	stops, texts := turns(t, out.String())
	if len(stops) != 1 || stops[0] != "error" {
		t.Fatalf("turns %v %v, want exactly one error-ended turn answering the prompt", stops, texts)
	}
	if strings.Contains(out.String(), "instruction received") {
		t.Fatalf("a greeting turn preceded the scripted error turn:\n%s", out.String())
	}
	if !strings.HasPrefix(out.String(), `{"command":"prompt"`) {
		t.Fatalf("the prompt echo must come first:\n%s", out.String())
	}
	journal, err := os.ReadFile(journalPath)
	if err != nil || !strings.Contains(string(journal), `"type":"error_turn"`) {
		t.Fatalf("journal %q (%v), want the error_turn entry", journal, err)
	}
	// a second prompt to the same process gets the greeting: the scripted
	// turn fires once
	out.Reset()
	d.answerPrompt("p2", "again")
	if stops, texts := turns(t, out.String()); len(stops) != 1 || stops[0] != "" || !strings.Contains(texts[0], "instruction received") {
		t.Fatalf("second prompt turns %v %v, want the greeting", stops, texts)
	}
}

func TestErrorTurnRetryRoundsRideThePromptAnswer(t *testing.T) {
	out := capture(t)
	envFor(planAction{ID: "grill", ErrorTurn: "429 rate limit", ErrorRetries: 2}).answerPrompt("p1", "do the step")
	stops, _ := turns(t, out.String())
	if len(stops) != 3 || stops[0] != "error" || stops[2] != "error" {
		t.Fatalf("turns %v, want pi's two auto-retry rounds and the final error turn", stops)
	}
	if strings.Contains(out.String(), "instruction received") {
		t.Fatalf("a greeting turn preceded the scripted rounds:\n%s", out.String())
	}
}

func TestEndTurnAnswersThePromptWithoutAGreeting(t *testing.T) {
	out := capture(t)
	envFor(planAction{ID: "grill", EndTurn: true}).answerPrompt("p1", "do the step")
	stops, texts := turns(t, out.String())
	if len(stops) != 1 || stops[0] != "" || texts[0] != "I have finished my reply." {
		t.Fatalf("turns %v %v, want exactly the scripted ok-ended turn", stops, texts)
	}
}

func TestPlainActionStillGreets(t *testing.T) {
	out := capture(t)
	envFor(planAction{ID: "grill", Outcome: "completed"}).answerPrompt("p1", "do the step")
	if stops, texts := turns(t, out.String()); len(stops) != 1 || !strings.Contains(texts[0], "instruction received") {
		t.Fatalf("turns %v %v, want the greeting", stops, texts)
	}
	out.Reset()
	envFor(planAction{ID: "grill", Silent: true}).answerPrompt("p1", "do the step")
	if stops, _ := turns(t, out.String()); len(stops) != 0 {
		t.Fatalf("a silent spawn answered with a turn: %s", out.String())
	}
}
