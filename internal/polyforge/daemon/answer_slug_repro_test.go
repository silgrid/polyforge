package daemon

// Repro test for polyforge#249: `pf answer <slug>` never reaches the
// driver. The answer route hands the RAW path value to
// DriverAnswerHandle — Runtime.Resolve knows only job keys (already
// canonical) and registered aliases, never a bare slug — while its
// liveness fallback canonicalizes through aihub (canonWI), so an
// answer addressed by slug on a live work item parked on a pending
// question always falls to the misleading DAEMON-409 "between
// derivations" refusal: the driver is live and waiting for this very
// answer, and the refusal's own retry advice never succeeds. The
// sibling routes (resume-paused, pause/wrap) canonicalize first;
// answer forgot.
//
// Expected behavior per the contract (SKILL.md): slugs resolve
// wherever the CLI accepts an id — answer accepts an id, so the slug
// spelling must deliver to the same live driver the id spelling
// reaches.
//
// Before the fix this test fails at the "REPORTED FAILURE" assertion:
// the slug answer is refused DAEMON-409 "between derivations"
// (identically on every retry) while the id spelling delivered on the
// very same rig right before it.

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// An answer addressed BY SLUG delivers to the live driver of the
// canonical job — the needs_input park of the live repro
// (polyforge#246 / w246-repro-test-vs-yaml-quote, answered by id at
// 16:27:38 after the slug spelling was refused).
func TestAnswerBySlugReachesTheLiveDriver(t *testing.T) {
	const (
		canonical = "wi_A0hzl6aqY3Eu" // the id spelling the live repro delivered by
		slug      = "polyforge#246"   // the slug spelling the live repro was refused by
		question  = "w246-repro-test-vs-yaml-quote"
	)
	aa := &answerAgent{answered: make(chan string, 1)}
	hub := newFakeHub()
	// aihub resolves the slug to the canonical id — the liveness
	// fallback reads exactly this (canonWI), the driver-branch lookup
	// does not.
	hub.canonical = map[string]string{slug: canonical}
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hub
		inner := fakeEngineStarter()
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			h, err := inner(ctx, in)
			if err != nil {
				return nil, err
			}
			aa.Agent = h
			return aa, nil
		}
	})
	// the ticket runs under its canonical id (the queue and the runtime
	// key everything by it; the alias table never learns the slug — the
	// claim was by id, exactly the live repro's shape).
	td.submitClaim(t, canonical)
	td.waitMonitored(t, canonical)
	// one open pending question: the driver is live and parked waiting
	// for this very answer.
	td.seedPendingQuestion(t, canonical, question, "repro",
		"w246: does the repro prefer a failing test or the yaml quote?")
	payload := cli.AnswerPayload{QuestionID: question, AnswerText: "A", AnsweredBy: "u_1"}

	// control: the ID spelling delivers on this exact rig — the driver
	// branch, the pending question and the answer handle are all live
	// (the live repro's 16:27:38 delivery). This is what pins the
	// failure to the slug spelling alone.
	if _, err := td.client.Answer(canonical, payload); err != nil {
		t.Fatalf("control: answer by id %s must deliver on this rig: %v", canonical, err)
	}
	select {
	case q := <-aa.answered:
		if q != question {
			t.Fatalf("control: delivered question = %q, want %q", q, question)
		}
	default:
		t.Fatalf("control: answer by id never reached the driver's handle")
	}

	// answer BY SLUG — the SKILL.md contract's exact spelling.
	if _, err := td.client.Answer(slug, payload); err != nil {
		// the refusal's own advice is "retry after the daemon re-derives
		// it" — the live repro took it, twice, never successfully. Take
		// it the same way: the refusal must be identical every time.
		last := err
		for i := 0; i < 2; i++ {
			_, last = td.client.Answer(slug, payload)
		}
		t.Fatalf("REPORTED FAILURE (polyforge#249): answer by slug %q was refused — first: %v; after two retries per the refusal's own advice: %v — while the id spelling delivered on this very rig and the driver IS live (job %s) with question %q pending; \"between derivations\" is not what is happening and the retry never succeeds",
			slug, err, last, canonical, question)
	}
	select {
	case q := <-aa.answered:
		if q != question {
			t.Fatalf("delivered question = %q, want %q", q, question)
		}
	default:
		t.Fatalf("REPORTED FAILURE (polyforge#249): the slug answer was accepted but never reached the driver's handle")
	}
}
