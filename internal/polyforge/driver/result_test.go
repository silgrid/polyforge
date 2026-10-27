package driver

// result_test.go: the staging gates over the result envelope. The
// pinned vectors double as the contract documentation: fencing, the
// needs_input question pair, the gate verdict binding, and the MINIMAL
// CONTRACT (polyforge#147) — an envelope missing the retired protocol
// fields is never judged invalid for its absence; there is no protocol
// stamp, no envelope id, no artifact manifest and no handoff digest
// anywhere.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fixture is one assembled validation scene: a spec and a matching
// completed result with an output object.
type fixture struct {
	spec ResultSpec
	res  *StepResult
}

func newFixture(t *testing.T, handoff bool) *fixture {
	t.Helper()
	f := &fixture{}
	f.spec = ResultSpec{
		WIID:   "wi_17",
		StepID: "fix",
		SpawnN: 3,
	}
	f.res = &StepResult{
		WIID:    "wi_17",
		StepID:  "fix",
		SpawnN:  3,
		Outcome: OutcomeCompleted,
		Output:  json.RawMessage(`{"summary":"ok","lines":2}`),
	}
	_ = handoff // the handoff duty is a path-convention read; the envelope carries no digest reference
	return f
}

func TestValidateStepResultCompleted(t *testing.T) {
	f := newFixture(t, true)
	if err := ValidateStepResult(f.res, f.spec); err != nil {
		t.Fatalf("completed minimal-contract result must validate: %v", err)
	}
}

// TestValidateStepResultNeedsCodeChangeEdges (polyforge#222): the
// needs_code_change self-report is legal only on a completed result under
// a light rewind — the field without that context (or a non-boolean
// value) is an envelope rejection, the in-process rewrite lane; under the
// context both true (the self-report) and false (an explicit no) pass —
// the transition alone keys on true.
func TestValidateStepResultNeedsCodeChangeEdges(t *testing.T) {
	light := func() *fixture {
		f := newFixture(t, false)
		f.spec.LightRewind = true
		return f
	}
	for _, tc := range []struct {
		name   string
		light  bool
		output json.RawMessage
		wantOK bool
	}{
		{"self-report under a light rewind", true, json.RawMessage(`{"summary":"s","needs_code_change":true}`), true},
		{"explicit no under a light rewind", true, json.RawMessage(`{"summary":"s","needs_code_change":false}`), true},
		{"field without the light context", false, json.RawMessage(`{"summary":"s","needs_code_change":true}`), false},
		{"explicit no without the light context", false, json.RawMessage(`{"summary":"s","needs_code_change":false}`), false},
		{"non-boolean value", true, json.RawMessage(`{"summary":"s","needs_code_change":"true"}`), false},
	} {
		f := newFixture(t, false)
		f.spec.LightRewind = tc.light
		f.res.Output = tc.output
		err := ValidateStepResult(f.res, f.spec)
		if tc.wantOK && err != nil {
			t.Errorf("%s: rejected: %v", tc.name, err)
		}
		if !tc.wantOK {
			if err == nil || !strings.Contains(err.Error(), "needs_code_change") {
				t.Errorf("%s: want the needs_code_change rejection, got %v", tc.name, err)
			}
		}
	}
	// a non-completed outcome never reads the field (its output is the
	// verdict/question context, not a schema-shaped completion)
	f := light()
	f.res.Outcome = OutcomeGateFailed
	f.res.Output = json.RawMessage(`{"verdict":"fail","needs_code_change":"why"}`)
	if err := ValidateStepResult(f.res, f.spec); err != nil {
		t.Errorf("a gate_failed envelope is not judged on the completion field: %v", err)
	}
}

func TestValidateStepResultShape(t *testing.T) {
	f := newFixture(t, false)

	if err := ValidateStepResult(nil, f.spec); err == nil {
		t.Fatal("nil result must be rejected")
	}

	// The minimal contract: an envelope with ONLY the outcome + output
	// pair and the identity/fence fields validates — the retired
	// protocol fields are not required, and their absence is never an
	// error (polyforge#147).
	res := *f.res
	if err := ValidateStepResult(&res, f.spec); err != nil {
		t.Fatalf("minimal-contract envelope must validate: %v", err)
	}

	res = *f.res
	res.Outcome = Outcome("vibes")
	if err := ValidateStepResult(&res, f.spec); err == nil {
		t.Fatal("unknown outcome must be rejected")
	}
}

func TestValidateStepResultBindingAndFence(t *testing.T) {
	f := newFixture(t, false)

	res := *f.res
	res.WIID = "wi_999"
	if err := ValidateStepResult(&res, f.spec); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("foreign wi_id must fence with ErrIdentityMismatch, got %v", err)
	}
	res = *f.res
	res.StepID = "grill"
	if err := ValidateStepResult(&res, f.spec); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("foreign step_id must fence with ErrIdentityMismatch, got %v", err)
	}

	// The fence: a result from a superseded spawn (stale spawn_n) is
	// rejected even though everything else about it is perfect.
	res = *f.res
	res.SpawnN = f.spec.SpawnN - 1
	if err := ValidateStepResult(&res, f.spec); !errors.Is(err, ErrStaleSpawn) {
		t.Fatalf("stale spawn_n must fence with ErrStaleSpawn, got %v", err)
	}
	res.SpawnN = f.spec.SpawnN + 1
	if err := ValidateStepResult(&res, f.spec); !errors.Is(err, ErrStaleSpawn) {
		t.Fatalf("future spawn_n must fence with ErrStaleSpawn, got %v", err)
	}
}

func TestValidateStepResultNonCompletedOutcomes(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeGateFailed, OutcomeNeedsInput, OutcomeIncomplete} {
		f := newFixture(t, false)
		f.res.Outcome = outcome
		// A non-completed outcome rides other channels (the gate
		// verdict, the pending question): no output is owed.
		f.res.Output = nil
		if outcome == OutcomeNeedsInput {
			f.res.QuestionID, f.res.Question = "q1", "which one?" // a needs_input envelope owes its question
		}
		if err := ValidateStepResult(f.res, f.spec); err != nil {
			t.Fatalf("%s result without output must validate: %v", outcome, err)
		}
	}
	// A needs_input envelope without its question pair is rejected.
	f := newFixture(t, false)
	f.res.Outcome = OutcomeNeedsInput
	if err := ValidateStepResult(f.res, f.spec); err == nil {
		t.Fatal("needs_input without question_id/question must be rejected")
	}
}

// TestValidateStepResultOutputIsUnconstrained: the declared-output
// schema lane is gone (polyforge#147 C5) — a completed envelope's
// output is whatever the step reports, judged by readers, never by the
// driver.
func TestValidateStepResultOutputIsUnconstrained(t *testing.T) {
	f := newFixture(t, false)
	for _, out := range []string{`["not","an","object"]`, `"a bare string"`, `42`, ``} {
		f.res.Output = json.RawMessage(out)
		if err := ValidateStepResult(f.res, f.spec); err != nil {
			t.Fatalf("output %q must not be constrained: %v", out, err)
		}
	}
}

func TestStepResultJSONRoundTrip(t *testing.T) {
	// Pin the envelope's wire tags: the staging side persists result
	// envelopes verbatim, so a renamed tag is a protocol break. The
	// MINIMAL CONTRACT (polyforge#147): identity/fence + outcome/output
	// (+ the needs_input question pair); no retired protocol fields.
	f := newFixture(t, false)
	f.res.Outcome = OutcomeNeedsInput
	f.res.QuestionID = "q_hand"
	f.res.Question = "which variant?"
	raw, err := json.Marshal(f.res)
	if err != nil {
		t.Fatal(err)
	}
	var back StepResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.WIID != f.res.WIID || back.StepID != f.res.StepID || back.SpawnN != f.res.SpawnN ||
		back.Outcome != f.res.Outcome {
		t.Fatalf("round trip lost envelope binding: %+v", back)
	}
	if back.QuestionID != "q_hand" || back.Question != "which variant?" {
		t.Fatalf("round trip lost the question pair: %+v", back)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"wi_id", "step_id", "spawn_n", "outcome", "output",
		"question_id", "question", "submitted_at"} {
		if _, ok := probe[tag]; !ok {
			t.Fatalf("envelope JSON must carry tag %q", tag)
		}
	}
	// The retired protocol fields must not reappear on the wire; an
	// envelope without evidence carries no command_evidence key either.
	for _, tag := range []string{"protocol_version", "result_id", "artifacts", "handoff_ref", "command_evidence"} {
		if _, ok := probe[tag]; ok {
			t.Fatalf("minimal-contract envelope must not carry tag %q", tag)
		}
	}
	// The V4 command evidence (the one field an evidence-owing step's
	// envelope adds) round-trips under its command_evidence tag.
	f.res.Outcome, f.res.QuestionID, f.res.Question = OutcomeCompleted, "", ""
	f.res.CommandEvidence = rawEvidence([]CommandEvidence{{
		Argv: []string{"go", "test", "./..."}, Cwd: "/w/repo", ExitCode: 0, DurationMs: 4242, LogPath: "logs/v.log",
	}})
	raw, err = json.Marshal(f.res)
	if err != nil {
		t.Fatal(err)
	}
	probe = nil
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe["command_evidence"]; !ok {
		t.Fatalf("an evidence-carrying envelope must serialize command_evidence: %s", raw)
	}
	back = StepResult{}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	backEv, err := decodeCommandEvidence(back.CommandEvidence)
	if err != nil || len(backEv) != 1 || backEv[0].DurationMs != 4242 || len(backEv[0].Argv) != 3 {
		t.Fatalf("round trip lost command evidence: %+v", back.CommandEvidence)
	}
}
