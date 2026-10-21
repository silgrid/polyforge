package core

// machines_test.go: the M3 heartbeat face at the Core level — upsert
// idempotency (register once, refresh forever), the keep-vs-replace
// semantics of the liveness projection (mode, live_wi_ids), the admin
// read model.

import (
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestHeartbeatRegistersOnceThenRefreshes(t *testing.T) {
	env := newEnv(t)
	h1, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-reg", RuntimeVersion: "v1.5"})
	if err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if !h1.Registered || h1.UserID == "" {
		t.Fatalf("first heartbeat must register: %+v", h1)
	}

	// Repeat: same identity, refresh only.
	h2, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-reg"})
	if err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}
	if h2.Registered || h2.UserID != h1.UserID {
		t.Fatalf("repeat must refresh in place: %+v", h2)
	}

	// Registration issues no api key: the daemon runs on its
	// operator-issued key, so nothing could ever use one.
	keys, err := env.Admin.ListAPIKeys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	for _, k := range keys {
		if k.UserID == h1.UserID {
			t.Fatalf("machine registration issued api key %s", k.ID)
		}
	}

	// Timestamp refresh: backdate, heartbeat, observe the stamp move to
	// ~now (server-stamped; the payload has no client timestamp field).
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := env.DB.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id='m-reg'`, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-reg"}); err != nil {
		t.Fatalf("refresh heartbeat: %v", err)
	}
	var at string
	if err := env.DB.QueryRow(`SELECT last_heartbeat_at FROM machines WHERE id='m-reg'`).Scan(&at); err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("stamp %q unparseable: %v", at, err)
	}
	if age := time.Since(parsed); age < -time.Minute || age > time.Minute {
		t.Fatalf("heartbeat did not refresh last_heartbeat_at (age %s)", age)
	}
}

func TestHeartbeatLivenessProjectionKeepVsReplace(t *testing.T) {
	env := newEnv(t)
	// Register with a full projection.
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{
		MachineID: "m-live", Mode: "interactive",
		LiveWIIDs: []string{"wi_b", "wi_a"}, RuntimeVersion: "v1.5",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	ms, err := env.Admin.Machines()
	if err != nil || len(ms) != 1 {
		t.Fatalf("machines: %v %+v", err, ms)
	}
	if ms[0].Mode != "interactive" || ms[0].RuntimeVersion != "v1.5" {
		t.Fatalf("projection lost: %+v", ms[0])
	}
	// live_wi_ids is stored (order preserved) and read back.
	if len(ms[0].LiveWIIDs) != 2 || ms[0].LiveWIIDs[0] != "wi_b" || ms[0].LiveWIIDs[1] != "wi_a" {
		t.Fatalf("live_wi_ids round-trip: %+v", ms[0].LiveWIIDs)
	}

	// Empty mode / nil live_wi_ids: KEEP the stored values.
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-live"}); err != nil {
		t.Fatalf("keep heartbeat: %v", err)
	}
	ms, _ = env.Admin.Machines()
	if ms[0].Mode != "interactive" || len(ms[0].LiveWIIDs) != 2 {
		t.Fatalf("empty payload must keep the stored projection: %+v", ms[0])
	}

	// Empty (non-nil) live_wi_ids: REPLACE with nothing held live.
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-live", LiveWIIDs: []string{}}); err != nil {
		t.Fatalf("replace heartbeat: %v", err)
	}
	ms, _ = env.Admin.Machines()
	if len(ms[0].LiveWIIDs) != 0 {
		t.Fatalf("empty slice must clear live_wi_ids: %+v", ms[0].LiveWIIDs)
	}
	if ms[0].Mode != "interactive" {
		t.Fatalf("empty mode must keep stored mode: %+v", ms[0])
	}

	// A new mode replaces; a second machine appears in the same listing.
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-live", Mode: "drain"}); err != nil {
		t.Fatalf("mode replace: %v", err)
	}
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "m-two", Mode: "batch"}); err != nil {
		t.Fatalf("second machine: %v", err)
	}
	ms, _ = env.Admin.Machines()
	if len(ms) != 2 {
		t.Fatalf("machines rows: %d", len(ms))
	}
	if ms[0].Mode != "drain" {
		t.Fatalf("mode not replaced: %+v", ms[0])
	}
	// Oldest heartbeat first: m-live's last refresh predates m-two's row.
	if ms[0].ID != "m-live" || ms[1].ID != "m-two" {
		t.Fatalf("listing order (oldest heartbeat first): %s, %s", ms[0].ID, ms[1].ID)
	}
}

func TestHeartbeatBadInput(t *testing.T) {
	env := newEnv(t)
	if _, err := env.Admin.Heartbeat(MachineHeartbeat{MachineID: "  "}); !IsCode(err, "CORE-400") {
		t.Fatalf("blank machine_id must be CORE-400: %v", err)
	}
}
