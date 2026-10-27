package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w177: the claim-time recalled-memories snapshot actually recalls. A
// real aihub, a real `pf daemon`: a work item whose goal + markdown
// content is a realistic task description, and a project memory sharing
// some of its words (never all of them). The daemon sends the goal (no
// content) to the any-term search (match=any); this pins it end to end: the relevant memory lands in
// runtime-state/recalled-memories.json, an unrelated one does not.
func TestW177ClaimRecallHitsRelevantMemory(t *testing.T) {
	inst := newV1Install(t)
	mkMem := func(content string) string {
		t.Helper()
		var m struct {
			ID string `json:"id"`
		}
		if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
			"project": v1Project, "type": "experience.debug", "visibility": "project",
			"content": content,
		}, &m); err != nil || m.ID == "" {
			t.Fatalf("create memory: %v (%+v)", err, m)
		}
		return m.ID
	}
	relevant := mkMem("Lesson: the provider retry ladder must back off exponentially; a flat ladder gives up on HTTP 429.")
	unrelated := mkMem("CSS grid layout quirks in the dashboard sidebar.")

	content := "## Context\nThe step agent hits a 429 from the provider and the retry ladder gives up after two attempts.\n\n- [ ] make the backoff exponential\n- [ ] log every attempt\n"
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    v1Project,
		Goal:       "w177: fix the flaky provider retry ladder in the daemon",
		Content:    &content,
		BundleName: strPtr("autopilot"),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	wi := item.ID
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}, nil)
	inst.trackWI(wi)

	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}

	snapPath := filepath.Join(d.capsuleDir(wi), "runtime-state", "recalled-memories.json")
	var snap struct {
		Query string `json:"query"`
		Items []struct {
			ID      string `json:"id"`
			Excerpt string `json:"excerpt"`
		} `json:"items"`
		Notice string `json:"notice"`
	}
	waitUntil(t, 30*time.Second, "the claim never wrote recalled-memories.json", func() bool {
		b, err := os.ReadFile(snapPath)
		return err == nil && json.Unmarshal(b, &snap) == nil
	})
	if snap.Query != "w177: fix the flaky provider retry ladder in the daemon" {
		t.Fatalf("snapshot query is not the goal: %q", snap.Query)
	}
	if len(snap.Items) != 1 || snap.Items[0].ID != relevant || !strings.Contains(snap.Items[0].Excerpt, "retry ladder") {
		t.Fatalf("snapshot items = %+v (notice %q), want exactly the relevant memory %s (never the unrelated %s)",
			snap.Items, snap.Notice, relevant, unrelated)
	}
	if snap.Notice != "" {
		t.Fatalf("populated recall carries a notice: %q", snap.Notice)
	}

	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
}
