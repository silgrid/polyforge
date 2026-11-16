package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// polyforge#73 AC4: the Host aggregate's `ops status` task counts (running,
// completed — the fields pi-web's Tasks badge and count key read) are the sum
// over every serving Cell, not the default Cell's own summary. A Cell's own
// status (a profile header) keeps its own counts. The tasks are seeded straight
// into each Cell's report store — the same directory and document shape the
// daemon's own task manager reads (task.NewStore(cellDataDir)), so Summarize
// counts them without a spawn.
func TestHostAggregateTaskCountsSumEveryServingCell(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	now := time.Now().UTC()
	seed := func(profile string, ids []string, state task.State) {
		t.Helper()
		store := task.NewStore(th.cellDataDir(profile))
		for _, id := range ids {
			if err := store.Save(task.Task{ID: id, State: state, CreatedAt: now,
				Spec: task.Spec{Project: "proj", Prompt: "seed " + id, MaxDuration: time.Minute, MaxTokens: 5}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed("default", []string{"task_00000000000000d1", "task_00000000000000d2"}, task.StateCompleted)
	seed("default", []string{"task_00000000000000d3"}, task.StateRunning)
	seed("alpha", []string{"task_00000000000000a1"}, task.StateCompleted)
	seed("alpha", []string{"task_00000000000000a2", "task_00000000000000a3"}, task.StateRunning)

	statusOf := func(profile string) cli.StatusResponse {
		t.Helper()
		raw, err := th.client(profile).Status()
		if err != nil {
			t.Fatalf("status %q: %v", profile, err)
		}
		var s cli.StatusResponse
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	agg := statusOf("")
	if agg.Tasks == nil {
		t.Fatal("the Host aggregate carries no task summary")
	}
	if agg.Tasks.Running != 3 || agg.Tasks.Completed != 3 {
		t.Fatalf("the aggregate's task counts must sum every serving Cell: running %d completed %d, want 3/3", agg.Tasks.Running, agg.Tasks.Completed)
	}
	if agg.Tasks.Limit != task.DefaultLimit {
		t.Fatalf("the aggregate's task limit is the configured bound %d, got %d", task.DefaultLimit, agg.Tasks.Limit)
	}
	if s := statusOf("alpha"); s.Tasks == nil || s.Tasks.Running != 2 || s.Tasks.Completed != 1 {
		t.Fatalf("alpha's own status keeps its own counts (2 running, 1 completed): %+v", s.Tasks)
	}
	if s := statusOf("default"); s.Tasks == nil || s.Tasks.Running != 1 || s.Tasks.Completed != 2 {
		t.Fatalf("the default Cell's own status keeps its own counts (1 running, 2 completed): %+v", s.Tasks)
	}
}
