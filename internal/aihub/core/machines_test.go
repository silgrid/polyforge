package core

// machines_test.go: the workbench#57 machine-heartbeat permission model at
// the Core level — the WRITE decision. The cryptographic verdict is the
// server's (it verifies the signature, the ±300 s window and the replay
// cache), so these tests construct proofs directly: a signed proof carries
// the authenticated caller and the key the server verified, an unsigned one
// only the caller — the bounded transition for an already-registered
// v2.20.6 daemon.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// signedProof / unsignedProof are the two server verdicts this package sees.
func signedProof(callerID, keyB64 string) HeartbeatProof {
	return HeartbeatProof{CallerID: callerID, Signed: true, KeyB64: keyB64}
}

func unsignedProof(callerID string) HeartbeatProof { return HeartbeatProof{CallerID: callerID} }

// registerMachine performs one signed first registration.
func registerMachine(t *testing.T, env *testEnv, machineID, operatorID, keyB64 string) *HeartbeatResult {
	t.Helper()
	res, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: machineID, PublicKey: keyB64},
		signedProof(operatorID, keyB64), nil)
	if err != nil {
		t.Fatalf("register %s: %v", machineID, err)
	}
	return res
}

// seedMachineRow inserts a legacy machines row by raw SQL (a v2.20.6 shape
// carries no user_id any more): operator and public key are NULL when their
// argument is "".
func seedMachineRow(t *testing.T, env *testEnv, machineID, operatorID, publicKey string) {
	t.Helper()
	var op, pk any
	if operatorID != "" {
		op = operatorID
	}
	if publicKey != "" {
		pk = publicKey
	}
	if _, err := env.DB.Exec(`INSERT INTO machines(id, last_heartbeat_at, role, public_key, operator_id)
		VALUES (?1,?2,'runner',?3,?4)`, machineID, Now(), pk, op); err != nil {
		t.Fatalf("seed machines row %s: %v", machineID, err)
	}
}

// machineField reads one machines column as a string (” for NULL).
func machineField(t *testing.T, env *testEnv, machineID, column string) string {
	t.Helper()
	var v string
	if err := env.DB.QueryRow(`SELECT COALESCE(`+column+`,'') FROM machines WHERE id=?1`, machineID).Scan(&v); err != nil {
		t.Fatalf("read %s.%s: %v", machineID, column, err)
	}
	return v
}

func TestHeartbeatRegistersOnceThenRefreshes(t *testing.T) {
	env := newEnv(t)
	h1 := registerMachine(t, env, "m-reg", env.UserID, "pk-reg")
	if !h1.Registered || h1.OperatorID != env.UserID {
		t.Fatalf("first heartbeat must register with the caller as operator: %+v", h1)
	}

	// A repeat is a refresh in place, same operator.
	h2, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-reg", RuntimeVersion: "v2.20.8"},
		signedProof(env.UserID, "pk-reg"), nil)
	if err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}
	if h2.Registered || h2.OperatorID != h1.OperatorID {
		t.Fatalf("repeat must refresh in place: %+v", h2)
	}

	// Registration mints no machine user and issues no api key: the daemon
	// runs on its operator-issued key, and operator_id is the machine's one
	// identity (workbench#57).
	var minted int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email LIKE '%@machines.local'`).Scan(&minted); err != nil {
		t.Fatalf("count machine users: %v", err)
	}
	if minted != 0 {
		t.Fatalf("machine registration minted %d @machines.local user(s)", minted)
	}

	// Timestamp refresh: backdate, heartbeat, observe the stamp move to
	// ~now (server-stamped; the payload has no client timestamp field).
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := env.DB.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id='m-reg'`, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-reg"},
		signedProof(env.UserID, "pk-reg"), nil); err != nil {
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
	proof := signedProof(env.UserID, "pk-live")
	// Register with a full projection.
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-live", PublicKey: "pk-live", Mode: "interactive",
		LiveWIIDs: []string{"wi_b", "wi_a"}, RuntimeVersion: "v1.5",
	}, proof, nil); err != nil {
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
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-live"}, proof, nil); err != nil {
		t.Fatalf("keep heartbeat: %v", err)
	}
	ms, _ = env.Admin.Machines()
	if ms[0].Mode != "interactive" || len(ms[0].LiveWIIDs) != 2 {
		t.Fatalf("empty payload must keep the stored projection: %+v", ms[0])
	}

	// Empty (non-nil) live_wi_ids: REPLACE with nothing held live.
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-live", LiveWIIDs: []string{}}, proof, nil); err != nil {
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
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-live", Mode: "drain"}, proof, nil); err != nil {
		t.Fatalf("mode replace: %v", err)
	}
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-two", PublicKey: "pk-two", Mode: "batch",
	}, signedProof(env.UserID, "pk-two"), nil); err != nil {
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
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "  "}, signedProof(env.UserID, "pk"), nil); !IsCode(err, "CORE-400") {
		t.Fatalf("blank machine_id must be CORE-400: %v", err)
	}
}

// TestHeartbeatRegistrationRequiresProof: a fresh machine_id needs the gate
// and a signature by the public key the body presents; no refusal writes
// anything.
func TestHeartbeatRegistrationRequiresProof(t *testing.T) {
	env := newEnv(t)
	cases := []struct {
		name string
		req  MachineHeartbeat
		prf  HeartbeatProof
		code string
	}{
		{"no public key", MachineHeartbeat{MachineID: "m-nokey"}, signedProof(env.UserID, "pk"), api.ErrAuthUnauthorized},
		{"unsigned", MachineHeartbeat{MachineID: "m-unsigned", PublicKey: "pk"}, unsignedProof(env.UserID), api.ErrAuthUnauthorized},
		{"signed by another key", MachineHeartbeat{MachineID: "m-mismatch", PublicKey: "pk-a"}, signedProof(env.UserID, "pk-b"), api.ErrAuthUnauthorized},
		{"no caller", MachineHeartbeat{MachineID: "m-nocaller", PublicKey: "pk"}, signedProof("", "pk"), api.ErrCoreBadInput},
	}
	for _, c := range cases {
		if _, err := env.Admin.HeartbeatAuthorized(c.req, c.prf, nil); !IsCode(err, c.code) {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
		if _, err := env.DB.Exec(`SELECT 1`); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM machines`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s: a refused registration left %d machines row(s)", c.name, n)
		}
	}

	// The gate refuses a key outside every project; nothing is written.
	refused := func() error { return Fail(api.ErrAuthForbidden, "", "outside every project") }
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-outsider", PublicKey: "pk"},
		signedProof(env.UserID, "pk"), refused); !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("gate refusal: %v, want %s", err, api.ErrAuthForbidden)
	}
	var n int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM machines`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a gate-refused registration left %d machines row(s) (err %v)", n, err)
	}
}

// TestHeartbeatOnlyTheOperatorHasEffect pins R3/R4/R9: another caller is the
// machine_id collision refusal (whatever it sends), the operator's own beat
// may move role/launcher/capability, and a different presented key is
// AUTH-403 without effect.
func TestHeartbeatOnlyTheOperatorHasEffect(t *testing.T) {
	env := newEnv(t)
	op1, op2 := env.UserID, env.UserIDB
	registerMachine(t, env, "m-op", op1, "pk-A")

	// A different authenticated user → AUTH-403 naming the operator and
	// suggesting --machine-id (the collision case), unchanged.
	_, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-op", Role: "interactive", Force: true, Launcher: "claude",
	}, signedProof(op2, "pk-B"), nil)
	if !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("non-operator beat must be AUTH-403, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "--machine-id") || !strings.Contains(msg, op1) {
		t.Fatalf("collision refusal must name the operator and suggest --machine-id: %s", msg)
	}
	if got := machineField(t, env, "m-op", "role"); got != "runner" {
		t.Fatalf("a non-operator beat changed the role to %q", got)
	}

	// The operator presenting a different key: AUTH-403, no rebind.
	_, err = env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-op", PublicKey: "pk-B"},
		signedProof(op1, "pk-B"), nil)
	if !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("operator rebind must be AUTH-403, got %v", err)
	}
	if got := machineField(t, env, "m-op", "public_key"); got != "pk-A" {
		t.Fatalf("the key was rebound to %q", got)
	}

	// The operator may move the role (no other interactive holder).
	res, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-op", Role: "interactive", Launcher: "claude",
	}, signedProof(op1, "pk-A"), nil)
	if err != nil || res.Role != "interactive" {
		t.Fatalf("operator role move: %+v %v", res, err)
	}
	if got := machineField(t, env, "m-op", "launcher"); got != "claude" {
		t.Fatalf("launcher = %q after the operator's beat", got)
	}
}

// TestHeartbeatUnsignedBeatTouchesLivenessOnly pins R6's transition: the
// operator's unsigned beat is accepted and writes only last_heartbeat_at,
// runtime_version, mode and live_wi_ids.
func TestHeartbeatUnsignedBeatTouchesLivenessOnly(t *testing.T) {
	env := newEnv(t)
	registerMachine(t, env, "m-trans", env.UserID, "pk-t")
	if _, err := env.DB.Exec(`UPDATE machines SET launcher='pi-web' WHERE id='m-trans'`); err != nil {
		t.Fatalf("seed launcher: %v", err)
	}
	before := map[string]string{
		"operator_id": env.UserID,
		"public_key":  "pk-t",
		"role":        "runner",
		"launcher":    "pi-web",
	}

	res, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-trans", RuntimeVersion: "v2.20.6", Mode: "run",
		LiveWIIDs: []string{"wi-u"}, Role: "interactive", Force: true,
		Launcher: "claude", PublicKey: "pk-other",
		Capability: &api.CapabilityCard{Methods: []string{api.MethodProgressQuery}, Version: 1},
	}, unsignedProof(env.UserID), nil)
	if err != nil {
		t.Fatalf("operator's unsigned transition beat: %v", err)
	}
	if res.Role != "runner" {
		t.Fatalf("unsigned beat changed the role in the answer: %+v", res)
	}
	for _, c := range []struct{ column, want string }{
		{"operator_id", before["operator_id"]},
		{"public_key", before["public_key"]},
		{"role", before["role"]},
		{"launcher", before["launcher"]},
		{"capability", ""},
		{"runtime_version", "v2.20.6"},
		{"mode", "run"},
	} {
		if got := machineField(t, env, "m-trans", c.column); got != c.want {
			t.Errorf("unsigned beat wrote %s = %q, want %q", c.column, got, c.want)
		}
	}
	if live := machineField(t, env, "m-trans", "live_wi_ids"); !strings.Contains(live, "wi-u") {
		t.Errorf("unsigned beat did not refresh live_wi_ids: %q", live)
	}

	// A second caller's unsigned beat is the collision refusal, unchanged.
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-trans", Mode: "x"},
		unsignedProof(env.UserIDB), nil); !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("a non-operator unsigned beat must be AUTH-403, got %v", err)
	}
	if got := machineField(t, env, "m-trans", "mode"); got != "run" {
		t.Fatalf("a refused unsigned beat changed mode to %q", got)
	}
}

// TestHeartbeatNullKeyRowBindsOnlyTheSigningKey pins R8: a legacy row with
// no bound key binds the key that signed the binding beat; an unsigned or
// wrongly signed beat leaves it unbound.
func TestHeartbeatNullKeyRowBindsOnlyTheSigningKey(t *testing.T) {
	env := newEnv(t)
	seedMachineRow(t, env, "m-legacy", env.UserID, "")

	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-legacy", PublicKey: "pk-1"},
		unsignedProof(env.UserID), nil); !IsCode(err, api.ErrAuthUnauthorized) {
		t.Fatalf("unsigned bind must be AUTH-401, got %v", err)
	}
	if got := machineField(t, env, "m-legacy", "public_key"); got != "" {
		t.Fatalf("an unsigned beat bound the key %q", got)
	}

	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-legacy", PublicKey: "pk-1"},
		signedProof(env.UserID, "pk-2"), nil); !IsCode(err, api.ErrAuthUnauthorized) {
		t.Fatalf("bind signed by another key must be AUTH-401, got %v", err)
	}
	if got := machineField(t, env, "m-legacy", "public_key"); got != "" {
		t.Fatalf("a wrongly signed beat bound the key %q", got)
	}

	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-legacy", PublicKey: "pk-1"},
		signedProof(env.UserID, "pk-1"), nil); err != nil {
		t.Fatalf("signed bind: %v", err)
	}
	if got := machineField(t, env, "m-legacy", "public_key"); got != "pk-1" {
		t.Fatalf("public_key = %q after the signed bind", got)
	}
}

// TestHeartbeatNullOperatorRowNeedsGateAndSignature pins R8's second half:
// claiming a legacy row without an operator is a registration act — the gate
// admits the claimer and the beat must be signed by the key it presents.
func TestHeartbeatNullOperatorRowNeedsGateAndSignature(t *testing.T) {
	env := newEnv(t)
	seedMachineRow(t, env, "m-claim", "", "")

	refused := func() error { return Fail(api.ErrAuthForbidden, "", "outside every project") }
	for _, c := range []struct {
		name string
		prf  HeartbeatProof
		gate func() error
	}{
		{"unsigned, eligible", unsignedProof(env.UserID), nil},
		{"signed, outside every project", signedProof(env.UserID, "pk-c"), refused},
	} {
		if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-claim", PublicKey: "pk-c"}, c.prf, c.gate); err == nil {
			t.Errorf("%s: the claim was accepted", c.name)
		}
		if got := machineField(t, env, "m-claim", "operator_id"); got != "" {
			t.Fatalf("%s: operator_id = %q, want NULL", c.name, got)
		}
		if got := machineField(t, env, "m-claim", "public_key"); got != "" {
			t.Fatalf("%s: public_key = %q, want NULL", c.name, got)
		}
	}

	// A signed beat from an eligible member claims the row.
	res, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-claim", PublicKey: "pk-c"},
		signedProof(env.UserID, "pk-c"), nil)
	if err != nil {
		t.Fatalf("eligible signed claim: %v", err)
	}
	if res.OperatorID != env.UserID {
		t.Fatalf("claim answer operator_id = %q, want %q", res.OperatorID, env.UserID)
	}
	if got := machineField(t, env, "m-claim", "operator_id"); got != env.UserID {
		t.Fatalf("operator_id = %q after the claim", got)
	}
	if got := machineField(t, env, "m-claim", "public_key"); got != "pk-c" {
		t.Fatalf("public_key = %q after the claim", got)
	}
}

// TestHeartbeatRefreshNeverRechecksTheGate pins R5: membership gates only
// registration; a refresh is accepted without ever calling the gate.
func TestHeartbeatRefreshNeverRechecksTheGate(t *testing.T) {
	env := newEnv(t)
	registerMachine(t, env, "m-refresh", env.UserID, "pk-r")
	gateCalls := 0
	gate := func() error { gateCalls++; return Fail(api.ErrAuthForbidden, "", "must not run on a refresh") }
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-refresh"},
		signedProof(env.UserID, "pk-r"), gate); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if gateCalls != 0 {
		t.Fatalf("a refresh re-checked membership %d time(s)", gateCalls)
	}
}
