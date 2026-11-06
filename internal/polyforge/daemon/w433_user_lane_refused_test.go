package daemon

// w433_user_lane_refused_test.go (workflow contract §13.4
// "User lanes"): a refused user lane does not stop the daemon — unlike a
// built-in lane, whose defect refuses the start — it only makes that lane
// unavailable (a claim of a work item on it is refused) and is recorded
// once as a daemon-scope user_lane_refused event naming its defects; a
// user lane named like a built-in lane is one (WFC-122). Judging again
// over unchanged lanes records nothing new.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// userLaneRefusals answers the user_lane_refused events of the daemon's
// events log, by lane.
func userLaneRefusals(t *testing.T, td *testDaemon) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(td.DataDirPath(), observe.EventsDir, "daemon.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct {
			WI   string         `json:"work_item"`
			Kind string         `json:"kind"`
			Body map[string]any `json:"body"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != observe.EvUserLaneRefused {
			continue
		}
		if e.WI != "" {
			t.Fatalf("user_lane_refused must be daemon-scope, got wi %q", e.WI)
		}
		lane, _ := e.Body["lane"].(string)
		if _, dup := out[lane]; dup {
			t.Fatalf("lane %s recorded twice", lane)
		}
		out[lane] = e.Body
	}
	return out
}

func TestW433RefusedUserLaneOnlyDisablesItself(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "other", "steps:\n  - { id: fix }\n")
	var ws string
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		ws = c.Mapping.Workspaces()[0]
		user := workflow.UserLanesRoot(ws)
		// a good user lane, one with an unknown key, one named like the
		// built-in lane
		workflowtest.WriteLane(t, user, "good", "steps:\n  - { id: draft }\n")
		workflowtest.WriteLaneUnchecked(t, user, "broken", "steps:\n  - { id: draft, context_policy: fresh }\n")
		workflowtest.WriteLane(t, user, "other", "steps:\n  - { id: draft }\n")
	})
	got := userLaneRefusals(t, td)
	if len(got) != 2 || got["broken"] == nil || got["other"] == nil {
		t.Fatalf("one user_lane_refused event per refused user lane, want broken and other: %v", got)
	}
	if got["broken"]["workspace"] != ws || got["broken"]["dir"] != filepath.Join(workflow.UserLanesRoot(ws), "broken") ||
		!strings.Contains(strings.Join(anyStrings(got["broken"]["errors"]), "\n"), workflow.CodeUnknownKey) {
		t.Fatalf("the broken lane's event: %v", got["broken"])
	}
	if !strings.Contains(strings.Join(anyStrings(got["other"]["errors"]), "\n"), workflow.CodeShadowsBuiltin) {
		t.Fatalf("the shadowing lane's event must name WFC-122: %v", got["other"])
	}
	// only the refused lane is unavailable: its claim is refused, the good
	// user lane and the built-in one pass
	if err := td.rt.CheckBundle("wi_x", "proj", "broken"); err == nil || !strings.Contains(err.Error(), workflow.CodeUnknownKey) {
		t.Fatalf("a claim on the refused user lane must be refused naming its defect: %v", err)
	}
	for _, lane := range []string{"good", "other"} {
		if err := td.rt.CheckBundle("wi_x", "proj", lane); err != nil {
			t.Fatalf("lane %s must stay claimable: %v", lane, err)
		}
	}
	td.hub.mu.Lock()
	td.hub.bundles = map[string]string{"wi_broken": "broken"}
	td.hub.mu.Unlock()
	mkBare(t, td.cfg.GitRemoteBase, "wi_broken")
	_, cerr := td.client.ClaimWithModels("wi_broken", "normal", nil)
	var ae *api.Error
	if !errors.As(cerr, &ae) || ae.Code != api.ErrDaemonWorkspaceYaml || !strings.Contains(ae.Detail, workflow.CodeUnknownKey) {
		t.Fatalf("submit on the refused user lane = %v, want DAEMON-414 naming its defect", cerr)
	}
	// judged again over the same lanes: nothing new is recorded
	td.reportUserLanes()
	if again := userLaneRefusals(t, td); len(again) != 2 {
		t.Fatalf("an unchanged refusal was recorded again: %v", again)
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
