package server

// heartbeat_collision_test.go — over real HTTP: the
// machine-id collision refusal stays the typed AUTH-403, still says the
// machine is registered to another operator and suggests a different
// --machine-id, but names neither the operator's id nor the display
// name; the admin machine inventory still maps the machine to its
// operator. Self-contained against the pre-change tree for the external
// oracle (red on the base, green on the branch).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestHeartbeatCollisionNamesNoOperator(t *testing.T) {
	e := newSrvEnv(t)
	ownerID, ownerKey := memberUser(t, e, "w74-hb-owner", "maintainer")
	otherID, otherKey := memberUser(t, e, "w74-hb-other", "writer")
	pub, priv := machineKey(t)
	e.registerSigned(t, ownerKey, "w74-hb-machine", pub, priv)

	code, body := e.doSigned(t, "POST", hbPath, otherKey, "w74-hb-machine", priv, 0, map[string]any{
		"machine_id": "w74-hb-machine", "live_wi_ids": []string{"wi-other"},
	})
	if code != http.StatusForbidden || errCodeOf(body) != api.ErrAuthForbidden {
		t.Fatalf("collision = %d %s, want 403 %s", code, body, api.ErrAuthForbidden)
	}
	if !strings.Contains(string(body), "--machine-id") {
		t.Fatalf("collision no longer suggests a different --machine-id: %s", body)
	}
	if !strings.Contains(strings.ToLower(string(body)), "another operator") {
		t.Fatalf("collision no longer says the machine belongs to another operator: %s", body)
	}
	if strings.Contains(string(body), ownerID) || strings.Contains(string(body), "w74-hb-owner") {
		t.Fatalf("collision leaked the existing operator %s: %s", ownerID, body)
	}
	if strings.Contains(string(body), otherID) {
		t.Fatalf("collision leaked the caller %s: %s", otherID, body)
	}
	if got := e.machineRow(t, "w74-hb-machine", "operator_id"); got != ownerID {
		t.Fatalf("collision changed operator_id to %q", got)
	}

	// The admin machine inventory still maps the machine to its operator.
	var list machinesResp
	if code, raw := e.do(t, "GET", "/api/v1/machines", e.key, nil, &list); code != http.StatusOK {
		t.Fatalf("machines inventory: %d %s", code, raw)
	}
	found := false
	for _, m := range list.Machines {
		if m.ID == "w74-hb-machine" {
			found = true
			if m.OperatorID != ownerID {
				t.Fatalf("inventory operator_id = %q, want %q", m.OperatorID, ownerID)
			}
		}
	}
	if !found {
		t.Fatalf("inventory lost the machine row: %+v", list.Machines)
	}
}
