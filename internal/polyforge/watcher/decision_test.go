package watcher

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// TestEveryDocumentedKindIsClassified ( AC1): every decision
// kind the retired chat watcher woke a chat for is still classified — the
// daemon's decision feed records it for the Pi Web panel — with its class
// and its attention mark.
func TestEveryDocumentedKindIsClassified(t *testing.T) {
	want := map[string]struct {
		class     string
		attention bool
	}{
		// a step's `pf ask` (workflow contract v2 §5.2): the needs_input
		// class, Pi Web's attention channel
		"awaiting_owner":       {"needs_input", true},
		"blocked":              {"blocked", true},
		"canceled":             {"canceled", false},
		"candidates_exhausted": {"candidates_exhausted", true},
		"carrier_push_failed":  {"carrier_push_failed", true},
		"completed":            {"completed", false},
		"daemon_restarted":     {"daemon_restarted", false},
		"failed":               {"failed", true},
		"failed_gate":          {"failed", true},
		"paused":               {"paused", false},
		"step_dead":            {"step_dead", false},
		"submit_dropped":       {"submit_dropped", true},
		"submit_rejected":      {"submit_rejected", true},
		"terminal":             {"terminal", false},
		"wrapped":              {"completed", false},
	}
	var kinds []string
	for k := range want {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if got := Kinds(); !reflect.DeepEqual(got, kinds) {
		t.Fatalf("the table's kinds drifted from the documented set:\n got %v\nwant %v", got, kinds)
	}
	for kind, w := range want {
		d, ok := classifyAny(kind)
		if !ok {
			t.Fatalf("%s: not classified", kind)
		}
		if d.Class != w.class || d.Attention != w.attention || d.Source != kind || d.Phrase == "" {
			t.Fatalf("%s: got %+v, want class %s attention %v source %s", kind, d, w.class, w.attention, kind)
		}
	}
}

// classifyAny classifies a kind the way the daemon meets it: an event of
// that kind, the terminal event carrying it as its status, or a hub status.
func classifyAny(kind string) (Decision, bool) {
	if d, ok := Classify(kind, nil); ok && kind != "terminal" {
		return d, true
	}
	if kind == "terminal" {
		return Classify("terminal", json.RawMessage(`{"status":"something_new"}`))
	}
	if d, ok := Classify("terminal", json.RawMessage(`{"status":"`+kind+`"}`)); ok && d.Source == kind {
		return d, true
	}
	return StatusDecision(kind)
}

func TestTerminalEventIsClassifiedByItsStatus(t *testing.T) {
	d, ok := Classify("terminal", json.RawMessage(`{"status":"failed_gate","gate":"review"}`))
	if !ok || d.Class != "failed" || d.Source != "failed_gate" || !d.Status || !d.Attention {
		t.Fatalf("terminal failed_gate = %+v", d)
	}
	d, ok = Classify("terminal", json.RawMessage(`{"status":"completed"}`))
	if !ok || d.Class != "completed" || d.Attention {
		t.Fatalf("terminal completed = %+v", d)
	}
}

func TestDecisionIdentifiersAreFilteredAndBodiesNeverLeak(t *testing.T) {
	d, _ := Classify("awaiting_owner", json.RawMessage(`{"question_id":"q-1; rm -rf /","question":"secret text"}`))
	if d.QuestionID != "q-1__rm_-rf__" {
		t.Fatalf("question id = %q", d.QuestionID)
	}
	d, _ = Classify("carrier_push_failed", json.RawMessage(`{"repo":"app","error":"token=abc"}`))
	if d.Repo != "app" || d.Phrase != eventRules["carrier_push_failed"].Phrase {
		t.Fatalf("carrier = %+v", d)
	}
}

func TestUnknownKindsAreNotDecisions(t *testing.T) {
	for _, k := range []string{"step_started", "answered", "job_started", "rewind", ""} {
		if _, ok := Classify(k, nil); ok {
			t.Fatalf("%q classified", k)
		}
	}
	if _, ok := StatusDecision("running"); ok {
		t.Fatal("running is not a decision")
	}
}

func TestTaskDecisions(t *testing.T) {
	for state, att := range map[string]bool{"completed": false, "failed": true, "capped": true, "interrupted": true} {
		d, ok := TaskDecision(state)
		if !ok || d.Class != state || d.Attention != att || d.Source != "task" {
			t.Fatalf("%s = %+v", state, d)
		}
	}
	if _, ok := TaskDecision("running"); ok {
		t.Fatal("a running task is not a decision")
	}
}

// TestFinalStatuses: a final status ends the work item; blocked and paused
// do not (the decision feed keeps reading them; Pi Web's discussion
// suspends on them — test/w413_discussion_question_ids_test.go).
func TestFinalStatuses(t *testing.T) {
	for st, final := range map[string]bool{"completed": true, "failed_gate": true,
		"wrapped": true, "failed": true, "canceled": true, "blocked": false, "paused": false} {
		d, ok := StatusDecision(st)
		if !ok || d.Final != final {
			t.Fatalf("%s: %+v", st, d)
		}
	}
	if d, _ := Classify("terminal", json.RawMessage(`{"status":"new_status"}`)); !d.Final {
		t.Fatal("an unknown terminal status is final")
	}
}

// TestReleaseDecisions (PR #860 review 2): the runtime's release reasons —
// a close's state through the status table, and a supersede (another
// session holds the work item; a local seal the hub never confirmed) as
// the informational, final superseded decision.
func TestReleaseDecisions(t *testing.T) {
	d, ok := ReleaseDecision("superseded")
	if !ok || d.Class != "superseded" || d.Attention || !d.Final || !d.Status || d.Phrase == "" {
		t.Fatalf("superseded = %+v", d)
	}
	for _, r := range []string{"wrapped", "failed", "blocked", "paused", "canceled"} {
		got, ok := ReleaseDecision(r)
		want, _ := StatusDecision(r)
		if !ok || got != want {
			t.Fatalf("%s = %+v, want the status table's %+v", r, got, want)
		}
	}
	if _, ok := ReleaseDecision("queued"); ok {
		t.Fatal("a re-queued work item is no decision (its open entries resolve)")
	}
}
