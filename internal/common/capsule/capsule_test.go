package capsule

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNoCapsuleStructCarriesLineageKeys guards the fossil cleanup: the
// v1 generation/attempt_id lineage keys must not creep back into the
// frozen capsule shapes (the round-trip JSON of every struct in this
// package carries none of them).
func TestNoCapsuleStructCarriesLineageKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		blob []byte
	}{
		{"Identity", mustJSON(t, Identity{})},
		{"Execution", mustJSON(t, Execution{})},
		{"WorkItemDoc", mustJSON(t, WorkItemDoc{})},
	} {
		for _, key := range []string{"generation", "attempt_id"} {
			if strings.Contains(string(tc.blob), `"`+key+`"`) {
				t.Errorf("%s round-trip still carries the v1 lineage key %q: %s", tc.name, key, tc.blob)
			}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return blob
}

// TestCapsuleAuditFieldsRoundTrip pins the post-subtraction audit
// contract (the effective_candidates audit field is deleted — the
// workspace yaml is the governing source at every dispatch): a capsule
// carrying the retired key decodes loose (unknown keys ignored — old
// capsules still parse), and an audit-less Identity marshals WITHOUT
// the key.
func TestCapsuleAuditFieldsRoundTrip(t *testing.T) {
	const governed = `{
	  "work_item_id": "wi_gov",
	  "project": "e2e",
	  "effective_candidates": {"fix": ["a", "b"], "grill": []}
	}`
	var idn Identity
	if err := json.Unmarshal([]byte(governed), &idn); err != nil {
		t.Fatalf("a capsule carrying the retired audit key must parse: %v", err)
	}

	// A legacy capsule (old keys included) decodes loose.
	const legacy = `{"work_item_id": "wi_old", "generation": 4, "workspace_yaml": {"projects": {}}, "repo_sources": {"a": "/x"}}`
	var old Identity
	if err := json.Unmarshal([]byte(legacy), &old); err != nil {
		t.Fatalf("legacy capsule must parse: %v", err)
	}

	// An audit-less Identity marshals without the key at all.
	blob, err := json.Marshal(Identity{WorkItemID: "wi_plain", Repos: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "effective_candidates") {
		t.Fatalf("audit-less identity marshals an effective_candidates key: %s", blob)
	}
}

// TestStepLightRecheckMarkerRoundTrip (polyforge#260): the pending light
// recheck marker — the mechanical fast lane's durable state carrier —
// survives the execution.json round-trip verbatim (the bound's raw bytes
// and the diff base), an absent marker marshals without the key, and a
// legacy execution.json without it decodes loose.
func TestStepLightRecheckMarkerRoundTrip(t *testing.T) {
	st := Step{
		Status: StepInProgress,
		PendingLightRecheck: &LightRecheck{
			Bound:    json.RawMessage(`[{"check":"lint","repo":"demo","command":"gofmt -l .","files":["a.go"]}]`),
			DiffBase: map[string]string{"demo": "0123456789abcdef"},
		},
	}
	blob, err := json.Marshal(&st)
	if err != nil {
		t.Fatal(err)
	}
	var back Step
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if back.PendingLightRecheck == nil {
		t.Fatalf("the marker did not round-trip: %s", blob)
	}
	if string(back.PendingLightRecheck.Bound) != string(st.PendingLightRecheck.Bound) {
		t.Fatalf("the bound did not round-trip verbatim: %s vs %s",
			back.PendingLightRecheck.Bound, st.PendingLightRecheck.Bound)
	}
	if back.PendingLightRecheck.DiffBase["demo"] != "0123456789abcdef" {
		t.Fatalf("the diff base did not round-trip: %+v", back.PendingLightRecheck.DiffBase)
	}
	// an absent marker marshals without the key (omitempty)
	blob, err = json.Marshal(Step{Status: StepPending})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "pending_light_recheck") {
		t.Fatalf("an absent marker must not marshal: %s", blob)
	}
	// a legacy execution.json without the marker decodes loose
	var legacy Execution
	if err := json.Unmarshal([]byte(`{"steps":{"code":{"status":"pending"}}}`), &legacy); err != nil {
		t.Fatalf("legacy execution.json must parse: %v", err)
	}
}
