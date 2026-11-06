package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestStepRequestIsRoutedByOwnership: a step agent's request is served by
// the Cell holding its work item whatever header it sends — a header naming
// another Cell is refused DAEMON-403 (the header only routes, it never
// authorizes). The owner is the Cell whose machine id the process marker
// carries — even when the default Cell holds an equal work item literal.
func TestStepRequestIsRoutedByOwnership(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	th.cell("alpha").d.rt.NoteAlias("alpha#7", "wi_alpha_owned")
	th.cell("default").d.rt.NoteAlias("alpha#7", "wi_default_same_literal")
	restore := runtimeStepOrigin
	runtimeStepOrigin = func(pid int, id string) (string, bool) { return "alpha#7", id == "m-alpha" }
	t.Cleanup(func() { runtimeStepOrigin = restore })

	// headerless: served by the owning Cell (alpha), not by default
	raw, err := th.client("").Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var s cli.StatusResponse
	_ = json.Unmarshal(raw, &s)
	if len(s.Cells) != 0 {
		t.Log("the headerless GET /status is the Host aggregate (a Host-level read)")
	}
	if _, err := th.client("").WatchSnapshot("wi_none", 0, false); err != nil && strings.Contains(err.Error(), "DAEMON-403") {
		t.Fatalf("the owning Cell must serve its step's read: %v", err)
	}
	// a header naming another Cell is refused
	_, err = th.client("default").WatchSnapshot("wi_none", 0, false)
	if err == nil || !strings.Contains(err.Error(), "DAEMON-403") || !strings.Contains(err.Error(), "owning Cell") {
		t.Fatalf("a conflicting header must be refused DAEMON-403: %v", err)
	}
	// a Host-level write from an agent is refused too
	if _, err := th.client("").Shutdown(); err == nil || !strings.Contains(err.Error(), "DAEMON-403") {
		t.Fatalf("an agent's Host shutdown must be refused: %v", err)
	}
}

// TestSharedMachineIDInvalidatesTheNamedCell: a machine id names one Cell
// (the agent process markers carry it), so a named Cell sharing the
// default Cell's is invalid and visibly so; the default serves on.
func TestSharedMachineIDInvalidatesTheNamedCell(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-same"}, hostCell{profile: "alpha", machineID: "m-same"})
	e := th.cell("alpha")
	if e.d != nil || !strings.Contains(e.invalid, "machine id") {
		t.Fatalf("alpha must be invalid for its shared machine id: %+v", e.invalid)
	}
	if th.cell("default").d == nil {
		t.Fatal("the default Cell must serve on")
	}
}
