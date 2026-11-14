package core

// w30_create_gate_test.go — polyforge#30, review round 1 finding #3: the
// create door's deactivation gate must hold INSIDE WIManager.Create's own
// transaction, not only in the server handler's pre-check. A deactivation
// transaction that commits between the handler's check and the create
// transaction would otherwise leave a queued work item in a deactivated
// project — unreachable by claim and by cancel, stranded until the project
// is restored — breaking the invariant a deactivated project holds
// terminal work items only. The repo's own convention for such invariants
// is the in-transaction check (OPEN_ON_BUNDLE checks the same way).

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestW30CreateRefusesDeactivatedProject: WIManager.Create itself refuses a
// deactivated project from inside its transaction, with the same typed
// PROJECT_DEACTIVATED answer the server face gives — no queued row is ever
// inserted, and the check needs no server handler to hold.
func TestW30CreateRefusesDeactivatedProject(t *testing.T) {
	env := w30StatusEnv(t)
	if _, err := env.Admin.DeactivateProject("w30"); err != nil {
		t.Fatalf("deactivate w30: %v", err)
	}
	w30ID := projectIDOfTest(t, env, "w30")
	for _, spelling := range []string{"w30", w30ID} {
		_, err := env.WIs.Create(CreateRequest{
			Project: spelling, Goal: "w30 must not take new work", Reporter: env.UserID,
		})
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrCoreForbidden {
			t.Fatalf("Create(%s) on the deactivated project: %v, want CORE-403", spelling, err)
		}
		var extra struct {
			Reason string `json:"reason"`
		}
		if len(ae.Extra) == 0 || json.Unmarshal(ae.Extra, &extra) != nil || extra.Reason != "PROJECT_DEACTIVATED" {
			t.Fatalf("Create(%s) extra %s, want the typed PROJECT_DEACTIVATED reason", spelling, ae.Extra)
		}
		if projID := projectIDOfTest(t, env, "w30"); countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID) != 0 {
			t.Fatal("the refused create left a work item behind")
		}
	}
	// The sibling project stays untouched: the gate answers the addressed
	// project alone.
	if id, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w30 create gate sibling", Reporter: env.UserID,
	}); err != nil || id == nil || id.ID == "" {
		t.Fatalf("Create on the active sibling: %v", err)
	}
}
