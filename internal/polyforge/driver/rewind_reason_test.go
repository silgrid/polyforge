package driver

// rewind_reason_test.go: the automatic rewind event's reason is masked
// before it enters the events file (polyforge#249 Q5; review r1#5). The
// hand-off reason is free text, so a credential shape in it must not reach
// a reader.

import (
	"context"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

func TestRewindReasonIsMasked(t *testing.T) {
	const sentinel = "sk-abcdefghijklmnopqrstuvwxyz"
	d, sp, _ := runEnv(t, spec("build"), spec("polish"), gateSpec("check", "build"))
	fail := fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusFail,
		"the branch moved since verification; token="+sentinel)}}
	sp.script = []fakeEntry{done(""), done(""), fail, done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var reason string
	for _, e := range eventsOfKind(d.log.(*eventLog), "rewind") {
		if s, _ := e["reason"].(string); s != "" {
			reason = s
		}
	}
	if reason == "" {
		t.Fatal("the automatic rewind carries a reason")
	}
	if strings.Contains(reason, sentinel) || strings.Contains(reason, "sk-") {
		t.Fatalf("the rewind reason leaks a credential: %q", reason)
	}
	if !strings.Contains(reason, "branch moved since verification") {
		t.Fatalf("the stable moved-base substring must survive masking: %q", reason)
	}
}
