package core

// w79_step_params_test.go (u1, polyforge#79): a queued work item's
// per-ticket step params ride ONE reserved top-level attrs key,
// `step_params` — one entry object per param, {"<step-id>": {"<param>":
// {"value": …, "at": <write time>}}} — so any machine
// and any Cell that claims the ticket sees them. The key's one writer is
// the daemon on its machine-signed attrs route (the write validates the
// params against this machine's lane and merges them into the capsule
// at claim, clearing the key once that publish succeeded): a create
// cannot plant it (`pf file create --attr step_params=…` is the user
// face and plants no params a lane would consume), while WriteAttr — the
// signed machine face — writes it on a work item of any status and
// deletes it with null, exactly like every business key.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestW79StepParamsReservedAtCreate(t *testing.T) {
	env := newEnv(t)
	// a create cannot plant the key (the top level only: nested
	// step_params under another key stays business data) — not even as an
	// explicit JSON null, whose presence is still the filer's word on a
	// key the daemon alone writes
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "planted params", Reporter: env.UserID,
		Attrs: map[string]any{"step_params": map[string]any{"build": map[string]any{"image": "reg/x:1"}}},
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "EXECUTION_STATE_REJECTED" {
		t.Fatalf("a planted step_params not rejected: %v", err)
	}
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "planted null", Reporter: env.UserID,
		Attrs: map[string]any{api.StepParamsKey: nil},
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "EXECUTION_STATE_REJECTED" {
		t.Fatalf("an explicit null step_params not rejected: %v", err)
	}
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "free attrs", Reporter: env.UserID,
		Attrs: map[string]any{"custom": map[string]any{"step_params": "y"}},
	}); err != nil {
		t.Fatalf("a nested step_params is business data: %v", err)
	}

	// the machine-signed face writes it whole on a queued work item…
	id := mustCreateExecWI(t, env, "queued params", nil, "")
	wi, err := env.WIs.WriteAttr(id, attrsReq(api.StepParamsKey, `{"build":{"image":"reg/x:1"}}`, nil), env.UserID, "m-79")
	if err != nil {
		t.Fatalf("the daemon's key must be machine-writable: %v", err)
	}
	if got, _ := json.Marshal(wi.Attrs[api.StepParamsKey]); string(got) != `{"build":{"image":"reg/x:1"}}` {
		t.Fatalf("the key is replaced whole: %s", got)
	}
	// …and null deletes it (the claim's consumption record)
	wi, err = env.WIs.WriteAttr(id, attrsReq(api.StepParamsKey, `null`, nil), env.UserID, "m-79")
	if err != nil {
		t.Fatalf("null must delete the key: %v", err)
	}
	if _, has := wi.Attrs[api.StepParamsKey]; has {
		t.Fatalf("null deletes the key: %v", wi.Attrs)
	}
}
