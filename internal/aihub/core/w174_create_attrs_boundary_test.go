package core

// w174_create_attrs_boundary_test.go (polyforge#174 u2): the create
// boundary accepts one new-write shape only — a top-level attrs key
// that carries a dot is CORE-400 with a detail showing the nested path
// it names (every reader — the list filter's json_extract, WriteAttr's
// `if` — walks dot paths, so a flat dotted key is a value no query
// reaches; the old flat rows move with the v0.2.4 migration). The
// refusal is create-only: the reserved-key rules keep their own shape.
// Authored red on the create that stored flat dotted keys — the defect
// this ticket's queue filter never matched.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestW174CreateRefusesTopLevelDottedAttrKeys: a create's attrs accept
// one shape — the nested path a dotted key names; the flat key is
// refused with the nested form spelled out, deterministically (the
// lexicographically first dotted key named), and the kept faces
// (nested paths, plain keys, the reserved-key refusals) stay.
func TestW174CreateRefusesTopLevelDottedAttrKeys(t *testing.T) {
	env := newEnv(t)

	// the flat dotted key the old CLI wrote is refused, with the nested
	// write spelled out in the detail (Q3 B: CORE-400 + the nested form).
	_, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "flat split marker", Reporter: env.UserID,
		Attrs: map[string]any{"split.origin": "wi_O"},
	})
	if !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("a flat dotted attrs key not refused CORE-400: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, `"split.origin"`) ||
		!strings.Contains(got, `{"split":{"origin":<value>}}`) {
		t.Fatalf("the refusal must name the key and the nested form it names: %v", err)
	}

	// deterministic: several dotted keys name the first one sorted.
	_, err = env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "several flat keys", Reporter: env.UserID,
		Attrs: map[string]any{"split.unit": "u", "split.origin": "o", "deploy.env": "prod"},
	})
	if !IsCode(err, api.ErrCoreBadInput) || !strings.Contains(err.Error(), `"deploy.env"`) {
		t.Fatalf("the refusal is not deterministic on the first sorted key: %v", err)
	}

	// the nested path a dotted key names is the accepted shape, and a
	// deep path and a plain key keep working.
	for _, attrs := range []map[string]any{
		{"split": map[string]any{"origin": "wi_O", "unit": "bg-ui"}},
		{"split": map[string]any{"origin": map[string]any{"deep": true}}},
		{"team": "x"},
	} {
		if _, err := env.WIs.Create(CreateRequest{
			Project: "alpha", Goal: "accepted shape", Reporter: env.UserID,
			Attrs: attrs,
		}); err != nil {
			t.Fatalf("a create with %v refused: %v", attrs, err)
		}
	}

	// the kept reserved-key refusals keep their own shape.
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "planted seal", Reporter: env.UserID,
		Attrs: map[string]any{"blocked_seal": map[string]any{"head": "h", "machine": "m"}},
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "EXECUTION_STATE_REJECTED" {
		t.Fatalf("a planted blocked_seal not refused as before: %v", err)
	}
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "planted params", Reporter: env.UserID,
		Attrs: map[string]any{api.StepParamsKey: map[string]any{"build": map[string]any{"image": "reg/x:1"}}},
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "EXECUTION_STATE_REJECTED" {
		t.Fatalf("a planted step_params not refused as before: %v", err)
	}
}
