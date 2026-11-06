package core

// w403_wrap_attrs_test.go ( A2/AC4): the wrapped session-end
// may carry attrs — the develop lane's hand-off to the integration lane
// (`integration: {state: landed, …}`) — merged key by key into the work
// item's attrs in the same fenced transaction; every other attr stays.
// Any other verdict carrying attrs, and the Core-owned blocked_seal key,
// are refused.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// landedHandoff is the develop lane's hand-off record as the wrap carries
// it (one repo, merged at mc0…).
func landedHandoff(pr string) map[string]any {
	head, merge := strings.Repeat("a", 40), strings.Repeat("c", 40)
	return map[string]any{"state": "landed",
		"repos":         []any{map[string]any{"repo": "app", "pr": pr, "head": head, "light_verified_head": head, "merge_commit": merge}},
		"merge_commits": map[string]any{"app": []any{merge}}}
}

func TestW403WrappedEndMergesAttrs(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d"
	id := mustCreateExecWI(t, env, "publish goal", map[string]any{"assignee": "u_1"}, "")
	mustSessionClaim(t, m, env, id, secret)
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked,
		Attrs: map[string]any{"integration": map[string]any{"state": "landed"}}}, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("attrs on a blocked end must be refused: %v", err)
	}
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped,
		Attrs: map[string]any{blockedSealKey: map[string]any{"head": "x"}}}, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("the Core-owned blocked_seal key must be refused: %v", err)
	}
	in := landedHandoff("https://github.com/o/r/pull/7")
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped, Attrs: map[string]any{"integration": in}}, env.UserID); err != nil {
		t.Fatalf("wrapped end with attrs: %v", err)
	}
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := wi.Attrs["integration"].(map[string]any)
	if wi.Status != api.StatusWrapped || got["state"] != "landed" || len(got["repos"].([]any)) != 1 || got["merge_commits"] == nil || wi.Attrs["assignee"] != "u_1" {
		t.Fatalf("the wrap must merge integration and keep the other attrs: status %s attrs %v", wi.Status, wi.Attrs)
	}
}
