package daemon

// machineface_test.go: the role negotiation projection. A heartbeat
// without machine_face_version disables ONLY the cross-machine face; the
// single-machine idle rule keeps the message poll off; a superseded
// daemon drops to runner while its execution and query face continue.

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

func newFaceTestDaemon() *Daemon {
	return &Daemon{
		cfg:    Config{MachineID: "m-face"},
		face:   machineFaceState{desiredRole: api.MachineRoleInteractive},
		runCtx: context.Background(),
	}
}

func TestApplyHeartbeatOldHubDisablesOnlyTheFace(t *testing.T) {
	d := newFaceTestDaemon()
	d.applyHeartbeat(&aihubclient.MachineHeartbeatResult{MachineID: "m-face"})
	if d.face.available {
		t.Fatal("a heartbeat without machine_face_version must disable the face")
	}
	if d.face.queryStarted || d.face.messageStarted {
		t.Fatalf("an old hub must not start a face loop: %+v", d.face)
	}
	// Local execution is untouched (the run context is still live).
	if d.runCtx.Err() != nil {
		t.Fatal("an old hub must not cancel local execution")
	}
	// A later new-shape heartbeat enables the face.
	d.applyHeartbeat(&aihubclient.MachineHeartbeatResult{
		MachineID: "m-face", MachineFaceVersion: api.MachineFaceVersion,
		Role: api.MachineRoleInteractive, MultiMachine: true,
	})
	if !d.face.available || d.face.role != api.MachineRoleInteractive || !d.face.multiMachine {
		t.Fatalf("new-shape heartbeat not applied: %+v", d.face)
	}
	if !d.face.queryStarted || !d.face.messageStarted {
		t.Fatalf("interactive multi-machine faces did not start: %+v", d.face)
	}
}

func TestSingleMachineUserStaysIdle(t *testing.T) {
	d := newFaceTestDaemon()
	d.applyHeartbeat(&aihubclient.MachineHeartbeatResult{
		MachineID: "m-face", MachineFaceVersion: api.MachineFaceVersion,
		Role: api.MachineRoleInteractive, MultiMachine: false,
	})
	if d.messageFaceWanted() {
		t.Fatal("a single-machine user must keep the message face idle")
	}
	if !d.face.queryStarted {
		t.Fatal("the query face runs for every registered daemon")
	}
	// A runner never holds the message face, peers or not.
	d.face.multiMachine = true
	d.face.role = api.MachineRoleRunner
	if d.messageFaceWanted() {
		t.Fatal("a runner must never hold the message face")
	}
}

func TestSupersededDaemonDropsToRunnerAndKeepsExecution(t *testing.T) {
	d := newFaceTestDaemon()
	d.applyHeartbeat(&aihubclient.MachineHeartbeatResult{
		MachineID: "m-face", MachineFaceVersion: api.MachineFaceVersion,
		Role: api.MachineRoleInteractive, MultiMachine: true,
	})
	if !d.messageFaceWanted() {
		t.Fatal("precondition: interactive peer holds the message face")
	}
	d.applyHeartbeat(&aihubclient.MachineHeartbeatResult{
		MachineID: "m-face", MachineFaceVersion: api.MachineFaceVersion,
		Role: api.MachineRoleRunner, MultiMachine: true,
		Superseded: true, SupersededBy: "m-new",
	})
	if d.face.role != api.MachineRoleRunner || !d.face.superseded || d.face.supersededBy != "m-new" {
		t.Fatalf("superseded state: %+v", d.face)
	}
	if d.messageFaceWanted() {
		t.Fatal("a superseded daemon must stop the message face")
	}
	if d.runCtx.Err() != nil {
		t.Fatal("supersession must not interrupt local execution")
	}
	if !d.face.queryStarted {
		t.Fatal("the query face keeps running after supersession")
	}
	st := d.faceStatus()
	if st["role"] != api.MachineRoleRunner || st["superseded"] != true {
		t.Fatalf("status projection: %+v", st)
	}
}
