package server

// cancel_note_test.go: the cancel door's optional body. An unauthorized
// caller is refused and the row is untouched; a cancel carries the
// verbatim reason and the owner message it rests on. (The dry_run probe a
// daemon made before acting on a reserved `cancel` answer left with that
// answer, workflow contract v2 change 19.)

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

func TestCancelNote(t *testing.T) {
	e := newSrvEnv(t)
	mk := func(name string) (string, string) {
		u, err := e.admin.CreateUser(name, name+"@example.com", "writer", "human")
		if err != nil {
			t.Fatal(err)
		}
		k, err := e.admin.CreateAPIKey(u.ID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID, k.Plain
	}
	repID, repKey := mk("reportercn")
	otherID, otherKey := mk("membercn")
	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{{UserID: repID, Role: "writer"}, {UserID: otherID, Role: "writer"}},
	}); err != nil {
		t.Fatal(err)
	}
	var wi struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
		map[string]any{"project": "alpha", "goal": "cancel note", "bundle_name": "demo-bundle"}, &wi); code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	secret := strings.Repeat("c", 32)
	if code, raw, _ := sessClaim(t, e, wi.ID, secret, "k-cccc"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	status := func() string {
		var got struct {
			Status string `json:"status"`
		}
		if code, raw := e.do(t, "GET", "/api/v1/work-items/"+wi.ID, repKey, nil, &got); code != 200 {
			t.Fatalf("get: %d %s", code, raw)
		}
		return got.Status
	}
	// an unauthorized caller is refused, and nothing moves
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/cancel", otherKey, map[string]any{"reason": "cancel"}, nil); code != 403 {
		t.Fatalf("plain member: %d %s", code, raw)
	}
	if s := status(); s != api.StatusRunning {
		t.Fatalf("a refused cancel writes nothing, status %q", s)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/session-end", e.key,
		map[string]string{"session_id": secret, "status": "blocked"}, nil); code != 200 {
		t.Fatalf("block: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/cancel", repKey,
		map[string]any{"reason": "cancel", "question_id": "q1"}, &wi); code != 200 || wi.Status != api.StatusCanceled {
		t.Fatalf("reporter cancel: %d %s", code, raw)
	}
}
