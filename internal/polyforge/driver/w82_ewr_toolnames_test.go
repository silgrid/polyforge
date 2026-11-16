package driver

// w82_ewr_toolnames_test.go (polyforge#82, AC2): the EWR retry's short
// reminder — the one instruction the first same-session re-dispatch
// after a turn that ended without `pf step handoff` sends (the model
// hallucinating an `mcp__…` tool name and reading the harness's
// `Tool … not found` as an environment fault is exactly the loop this
// reminder breaks) — carries the same tool-name correction sentence the
// step header carries (u1): folded-equal against the shipped
// step-header.md's `## Tool names` section, so the two copies cannot
// drift. The reminder re-sends no part of the step header, so the
// sentence reaches the resumed conversation on its own.

import (
	"context"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// TestW82EwrReminderCarriesToolNameCorrection (AC2): spawn 1 dispatches
// normally; spawn 2 is the EWR same-session resume (Resume,
// PriorEndedWithoutResult, RetryTriggered with a usable transcript and a
// journal line), so its prompt is the short reminder — which must carry
// the tool-name correction. Red on the pre-change tree: the reminder
// speaks only of the hand-off duty.
func TestW82EwrReminderCarriesToolNameCorrection(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: harness.Pi,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p1, _ := sp.last()
	writeCapsuleFile(t, capDir, strings.TrimPrefix(p1.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
	if err := ad.appendSpawnJournal(p1, "grill", "tok1", "", 1, "2026-10-25T00:00:00Z"); err != nil {
		t.Fatalf("appendSpawnJournal: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: harness.Pi,
		Resume: true, PriorEndedWithoutResult: true, RetryTriggered: true,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p2, _ := sp.last()
	if !p2.Resume {
		t.Fatalf("the EWR retry must resume the same harness session: %+v", p2)
	}
	prompt := p2.InitialPrompt
	if strings.Contains(prompt, dispatchHeading) {
		t.Fatalf("the EWR retry sends the short reminder, never the full prompt:\n%s", prompt)
	}
	// the same sentence the step header carries (u1): the reminder's copy
	// is the section body, whitespace folded — one fact, two places,
	// pinned equal.
	section, ok := w82ToolNamesSection(t)
	if !ok {
		t.Fatalf("the shipped step header carries no %q section", w82ToolNamesHeading)
	}
	if flat := strings.Join(strings.Fields(section), " "); !strings.Contains(strings.Join(strings.Fields(prompt), " "), flat) {
		t.Errorf("the EWR short reminder does not carry the tool-names correction sentence:\n%s", prompt)
	}
}
