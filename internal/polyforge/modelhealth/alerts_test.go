package modelhealth

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestEdgeTriggerAndRearm(t *testing.T) {
	alerted := map[string]bool{}
	a := Alert{Model: "m1", Rule: RuleDeaths24h, Rules: []string{RuleDeaths24h}, ModelDeaths: 3}
	if got := evaluateLocked(alerted, []Alert{a}); len(got) != 1 {
		t.Fatalf("first crossing must emit once: %+v", got)
	}
	if got := evaluateLocked(alerted, []Alert{a}); len(got) != 0 {
		t.Fatalf("a further evaluation of the same episode must not re-emit: %+v", got)
	}
	// condition clears -> disarms
	if got := evaluateLocked(alerted, nil); len(got) != 0 || alerted[alertKey("m1", "")] {
		t.Fatalf("clearing must disarm the episode: %+v %v", got, alerted)
	}
	// a later deterioration reports afresh
	if got := evaluateLocked(alerted, []Alert{a}); len(got) != 1 {
		t.Fatalf("re-arm must emit afresh: %+v", got)
	}
}

func TestBuildSeedsReportedEpisode(t *testing.T) {
	// A previously emitted model_unhealthy for a still-unhealthy model is
	// in the events stream: a restart must not duplicate the episode.
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-time.Hour), "glm", "", "code", 1),
		failEvent("wi", 2, at(-time.Hour+time.Minute), "code", "model_error", 1),
		startEvent("wi", 3, at(-50*time.Minute), "glm", "", "code", 2),
		failEvent("wi", 4, at(-50*time.Minute+time.Minute), "code", "model_error", 2),
		startEvent("wi", 5, at(-40*time.Minute), "glm", "", "code", 3),
		failEvent("wi", 6, at(-40*time.Minute+time.Minute), "code", "model_error", 3),
		entry("", kindUnhealthy, 1, at(-30*time.Minute), map[string]any{"model": "glm", "rule": RuleDeaths24h, "rules": []string{RuleDeaths24h}, "model_deaths": 3}),
	}
	r := build(es, w14Now, 30*24*time.Hour)
	if !r.reported[alertKey("glm", "")] {
		t.Fatalf("the model_unhealthy entry must seed the reported episode: %+v", r.reported)
	}
	if got := evaluateLocked(clone(r.reported), r.alerts); len(got) != 0 {
		t.Fatalf("a restart over a still-unhealthy, already-reported episode must emit nothing: %+v", got)
	}
	if len(r.alerts) != 1 {
		t.Fatalf("the episode is still current: %+v", r.alerts)
	}
}

func clone(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
