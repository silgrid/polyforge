package api

import (
	"encoding/json"
	"testing"
)

func TestMachineFaceRoundTrip(t *testing.T) {
	hb := MachineHeartbeat{MachineID: "m1", LiveWIIDs: []string{}, PublicKey: "pk",
		Launcher: LauncherPiWeb, Role: MachineRoleInteractive, Capability: &CapabilityCard{
			Methods: []string{MethodProgressQuery, MethodArtifactRead}, Version: MachineFaceVersion}}
	raw, err := json.Marshal(hb)
	if err != nil {
		t.Fatal(err)
	}
	var back MachineHeartbeat
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.PublicKey != "pk" || back.Role != MachineRoleInteractive || !back.Capability.Has(MethodProgressQuery) {
		t.Fatalf("round-trip lost a field: %+v", back)
	}
	res := MachineHeartbeatResult{MachineFaceVersion: MachineFaceVersion, Role: MachineRoleRunner,
		Superseded: true, SupersededBy: "m2"}
	raw, _ = json.Marshal(res)
	var r backResult
	if err := json.Unmarshal(raw, &r); err != nil || r.MachineFaceVersion != 1 || r.SupersededBy != "m2" {
		t.Fatalf("result round-trip: %v %s", err, raw)
	}
}

type backResult struct {
	MachineFaceVersion int    `json:"machine_face_version"`
	SupersededBy       string `json:"superseded_by"`
}

func TestNewErrorStatuses(t *testing.T) {
	for code, want := range map[string]int{
		ErrCoreTooLarge: 413, ErrCoreSecret: 422, ErrCoreTooMany: 429,
		ErrCoreUnsupported: 501, ErrCoreUpstream: 503, ErrCoreStaleHolder: 504,
	} {
		if got := (&Error{Code: code}).HTTPStatus(); got != want {
			t.Fatalf("%s status = %d, want %d", code, got, want)
		}
	}
}

func TestMessagingWireShape(t *testing.T) {
	msg := Message{ID: "msg_x", Seq: 7, ConversationID: "conv_x", SenderID: "u1", Text: "hello"}
	raw, _ := json.Marshal(msg)
	want := `{"id":"msg_x","seq":7,"conversation_id":"conv_x","sender_id":"u1","text":"hello","created_at":""}`
	if string(raw) != want {
		t.Fatalf("message wire shape drifted:\n got %s\nwant %s", raw, want)
	}
}
