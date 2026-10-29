package core

// heartbeat_collision_test.go — the machine-id collision
// refusal stays the typed AUTH-403, still says the machine is
// registered to another operator and suggests a different
// --machine-id, but contains neither the operator's id nor the display
// name. Self-contained against the pre-change tree for the external
// oracle (red on the base, green on the branch).

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestHeartbeatCollisionNamesNoOperator(t *testing.T) {
	env := newEnv(t)
	// An owner with a distinctive display name, so the absence check is
	// meaningful for both the id and the name.
	owner, err := env.Admin.CreateUser("w74-hb-owner-display", "w74-hb-owner@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	other, err := env.Admin.CreateUser("w74-hb-other", "w74-hb-other@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("other: %v", err)
	}
	registerMachine(t, env, "m-w74-hb", owner.ID, "pk-w74-hb")

	_, err = env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-w74-hb", LiveWIIDs: []string{"wi-other"},
	}, signedProof(other.ID, "pk-w74-hb"), nil)
	if !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("collision = %v, want %s", err, api.ErrAuthForbidden)
	}
	msg := err.Error()
	if !strings.Contains(msg, "--machine-id") {
		t.Fatalf("collision no longer suggests a different --machine-id: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "another operator") {
		t.Fatalf("collision no longer says the machine belongs to another operator: %s", msg)
	}
	if strings.Contains(msg, owner.ID) || strings.Contains(msg, "w74-hb-owner-display") {
		t.Fatalf("collision leaked the existing operator %s: %s", owner.ID, msg)
	}
	// Nothing changed on the row.
	if got := machineField(t, env, "m-w74-hb", "operator_id"); got != owner.ID {
		t.Fatalf("collision changed operator_id to %q", got)
	}
	if got := machineField(t, env, "m-w74-hb", "live_wi_ids"); got != "" {
		t.Fatalf("collision changed live_wi_ids to %q", got)
	}
}
