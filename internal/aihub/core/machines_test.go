package core

// machines_test.go: the machine-heartbeat permission model at
// the Core level — the WRITE decision. The cryptographic verdict is the
// server's (it verifies the signature, the ±300 s window and the replay
// cache), so these tests construct proofs directly: a signed proof carries
// the authenticated caller and the key the server verified, an unsigned one
// only the caller — the bounded transition for an already-registered
// v2.20.6 daemon.

import (
	"strconv"
	"strings"
	"sync"
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
	// identity.
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
		MachineID: "m-live", PublicKey: "pk-live", Mode: "interactive", Role: "interactive",
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

	// The additive superseded_at read: a fresh machine
	// carries no stamp; a supersession stamps it and the read model
	// carries the stamp (the /api/v1 mapping ignores it).
	if ms[0].SupersededAt != "" {
		t.Fatalf("a never-superseded machine carries a supersession stamp: %+v", ms[0])
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.supersedeOthersTx(tx, env.UserID, "m-none"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ms, _ = env.Admin.Machines()
	if ms[0].SupersededAt == "" {
		t.Fatalf("the supersession stamp is not read back: %+v", ms[0])
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
	refused := Fail(api.ErrAuthForbidden, "", "outside every project")
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

	// A different authenticated user → AUTH-403 saying another operator
	// and suggesting --machine-id, but naming no one ( changed
	// this: the collision refusal no longer discloses the operator's id).
	_, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-op", Role: "interactive", Force: true, Launcher: "claude",
	}, signedProof(op2, "pk-B"), nil)
	if !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("non-operator beat must be AUTH-403, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "--machine-id") || !strings.Contains(msg, "another operator") || strings.Contains(msg, op1) {
		t.Fatalf("collision refusal must say another operator, suggest --machine-id and name no one: %s", msg)
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
	// registerSigned leaves runtime_version empty and the version gate
	// refuses that (fail-closed), so the row is seeded with the pre-signing
	// version a v2.20.6 daemon would have stored.
	if _, err := env.DB.Exec(`UPDATE machines SET launcher='pi-web', runtime_version='v2.20.6' WHERE id='m-trans'`); err != nil {
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

	refused := Fail(api.ErrAuthForbidden, "", "outside every project")
	for _, c := range []struct {
		name string
		prf  HeartbeatProof
		gate error
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

// TestHeartbeatRefreshIgnoresTheGate pins R5 in the by-value gate's shape
// the server evaluates the registration gate before the
// core call, and core enforces the verdict only on the two registration
// acts — a refresh ignores it, a claim still answers it.
func TestHeartbeatRefreshIgnoresTheGate(t *testing.T) {
	env := newEnv(t)
	registerMachine(t, env, "m-refresh", env.UserID, "pk-r")
	gate := Fail(api.ErrAuthForbidden, "", "must not gate a refresh")
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-refresh"},
		signedProof(env.UserID, "pk-r"), gate); err != nil {
		t.Fatalf("refresh must ignore the registration gate: %v", err)
	}
	seedMachineRow(t, env, "m-claim-g", "", "")
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-claim-g", PublicKey: "pk-g"},
		signedProof(env.UserID, "pk-g"), gate); !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("claim must answer the gate refusal: %v", err)
	}
	if got := machineField(t, env, "m-claim-g", "operator_id"); got != "" {
		t.Fatalf("a gate-refused claim wrote operator_id %q", got)
	}
}

// ---------------------------------------------------------------------------
// one heartbeat is one transaction — the race loser answers
// the sequential outcome, never CORE-500, never a rebind.

// raceBeats releases n beats at a barrier and collects their answers and
// errors. The fix is structural (the store's single connection serializes
// the heartbeat transactions), so the assertion cannot flake: repetition
// with a fresh machine id per round is the evidence, per plan D6.
func raceBeats(n int, beat func(i int) (*HeartbeatResult, error)) ([]*HeartbeatResult, []error) {
	start := make(chan struct{})
	res := make([]*HeartbeatResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i], errs[i] = beat(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return res, errs
}

// TestHeartbeatConcurrentSameOperatorRegistration: four concurrent first
// registrations of one id by one operator — one registers, the other three
// answer 200 as the refresh they would have been had the requests run one
// after the other; exactly one row, bound to the operator's key.
func TestHeartbeatConcurrentSameOperatorRegistration(t *testing.T) {
	env := newEnv(t)
	for round := 0; round < 5; round++ {
		id := "m-race-same-" + strconv.Itoa(round)
		res, errs := raceBeats(4, func(i int) (*HeartbeatResult, error) {
			return env.Admin.HeartbeatAuthorized(MachineHeartbeat{
				MachineID: id, PublicKey: "pk-race", LiveWIIDs: []string{"wi-" + strconv.Itoa(i)},
			}, signedProof(env.UserID, "pk-race"), nil)
		})
		registered := 0
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d contender %d = %v, want the sequential outcome (never CORE-500)", round, i, err)
			}
			if res[i].Registered {
				registered++
			}
		}
		if registered != 1 {
			t.Fatalf("round %d answered %d registrations, want one", round, registered)
		}
		var n int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM machines WHERE id=?1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("round %d left %d rows (err %v), want 1", round, n, err)
		}
		if got := machineField(t, env, id, "operator_id"); got != env.UserID {
			t.Fatalf("round %d operator_id = %q, want %q", round, got, env.UserID)
		}
		if got := machineField(t, env, id, "public_key"); got != "pk-race" {
			t.Fatalf("round %d public_key = %q, want pk-race", round, got)
		}
	}
}

// TestHeartbeatConcurrentDifferentOperatorRegistration: two operators race
// the first registration — exactly one wins, the loser gets the unnamed
// collision 403 it would have gotten sequentially, and the row keeps the
// winner's operator and key.
func TestHeartbeatConcurrentDifferentOperatorRegistration(t *testing.T) {
	env := newEnv(t)
	ops := []string{env.UserID, env.UserIDB}
	for round := 0; round < 5; round++ {
		id := "m-race-diff-" + strconv.Itoa(round)
		_, errs := raceBeats(2, func(i int) (*HeartbeatResult, error) {
			return env.Admin.HeartbeatAuthorized(MachineHeartbeat{
				MachineID: id, PublicKey: "pk-" + strconv.Itoa(i), LiveWIIDs: []string{"wi-" + strconv.Itoa(i)},
			}, signedProof(ops[i], "pk-"+strconv.Itoa(i)), nil)
		})
		winner := -1
		for i, err := range errs {
			switch {
			case err == nil:
				if winner >= 0 {
					t.Fatalf("round %d has two winners: %v", round, errs)
				}
				winner = i
			case IsCode(err, api.ErrAuthForbidden):
				if msg := err.Error(); strings.Contains(msg, ops[1-i]) {
					t.Fatalf("round %d collision names the operator: %s", round, msg)
				}
			default:
				t.Fatalf("round %d contender %d = %v, want 200 or the collision 403", round, i, err)
			}
		}
		if winner < 0 {
			t.Fatalf("round %d answered no 200: %v", round, errs)
		}
		if got := machineField(t, env, id, "operator_id"); got != ops[winner] {
			t.Fatalf("round %d operator_id = %q, want the winner %q", round, got, ops[winner])
		}
		if got := machineField(t, env, id, "public_key"); got != "pk-"+strconv.Itoa(winner) {
			t.Fatalf("round %d public_key = %q, want the winner's", round, got)
		}
	}
}

// TestHeartbeatConcurrentLegacyClaimKeepsOneOwner: four operators race the
// claim of one NULL-operator legacy row — exactly one 200 owns it, the
// losers get the collision 403, and the row keeps the winner's operator_id
// and public_key (the old code answered several 200s and rebound the row).
func TestHeartbeatConcurrentLegacyClaimKeepsOneOwner(t *testing.T) {
	env := newEnv(t)
	ops := []string{env.UserID, env.UserIDB}
	for i := 2; i < 4; i++ {
		u, err := env.Admin.CreateUser("w82-claim-op-"+strconv.Itoa(i), "w82-claim-op-"+strconv.Itoa(i)+"@example.com", "writer", "human")
		if err != nil {
			t.Fatalf("user: %v", err)
		}
		ops = append(ops, u.ID)
	}
	for round := 0; round < 5; round++ {
		id := "m-race-claim-" + strconv.Itoa(round)
		seedMachineRow(t, env, id, "", "")
		_, errs := raceBeats(4, func(i int) (*HeartbeatResult, error) {
			return env.Admin.HeartbeatAuthorized(MachineHeartbeat{
				MachineID: id, PublicKey: "pk-" + strconv.Itoa(i), LiveWIIDs: []string{"wi-" + strconv.Itoa(i)},
			}, signedProof(ops[i], "pk-"+strconv.Itoa(i)), nil)
		})
		winner := -1
		for i, err := range errs {
			switch {
			case err == nil:
				if winner >= 0 {
					t.Fatalf("round %d has two owners: %v", round, errs)
				}
				winner = i
			case IsCode(err, api.ErrAuthForbidden):
			default:
				t.Fatalf("round %d claimer %d = %v, want 200 or the collision 403", round, i, err)
			}
		}
		if winner < 0 {
			t.Fatalf("round %d answered no 200: %v", round, errs)
		}
		if got := machineField(t, env, id, "operator_id"); got != ops[winner] {
			t.Fatalf("round %d operator_id = %q, want the winner %q", round, got, ops[winner])
		}
		if got := machineField(t, env, id, "public_key"); got != "pk-"+strconv.Itoa(winner) {
			t.Fatalf("round %d public_key = %q, want the winner's (no rebind)", round, got)
		}
	}
}

// TestHeartbeatSequentialLostClaimAnd409Backstop: the deterministic
// companions of the race — a lost claim answered sequentially changes
// nothing, and a residual registration INSERT UNIQUE violation maps to
// CORE-409 (the backstop), never CORE-500.
func TestHeartbeatSequentialLostClaimAnd409Backstop(t *testing.T) {
	env := newEnv(t)
	seedMachineRow(t, env, "m-seq-claim", "", "")
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-seq-claim", PublicKey: "pk-1"},
		signedProof(env.UserID, "pk-1"), nil); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m-seq-claim", PublicKey: "pk-2"},
		signedProof(env.UserIDB, "pk-2"), nil)
	if !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("lost claim = %v, want the collision 403", err)
	}
	if got := machineField(t, env, "m-seq-claim", "operator_id"); got != env.UserID {
		t.Fatalf("lost claim rebound operator_id to %q", got)
	}
	if got := machineField(t, env, "m-seq-claim", "public_key"); got != "pk-1" {
		t.Fatalf("lost claim rebound public_key to %q", got)
	}

	// The backstop: a row that slipped past the transaction's read (not
	// reachable through HeartbeatAuthorized — one transaction serializes
	// the two) still maps the UNIQUE violation to CORE-409.
	seedMachineRow(t, env, "m-backstop", env.UserID, "pk-b")
	tx, terr := env.DB.Begin()
	if terr != nil {
		t.Fatalf("begin: %v", terr)
	}
	defer tx.Rollback()
	if err := env.Admin.insertMachineRow(tx, MachineHeartbeat{MachineID: "m-backstop", PublicKey: "pk-b"},
		env.UserIDB, api.MachineRoleRunner, nil); !IsCode(err, api.ErrCoreConflict) {
		t.Fatalf("residual UNIQUE = %v, want %s", err, api.ErrCoreConflict)
	}
}

// ---------------------------------------------------------------------------
// the unsigned-heartbeat version gate and ratchet.

func TestParseMachineVersion(t *testing.T) {
	for _, c := range []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v2.20.8", [3]int{2, 20, 8}, true},
		{"2.20.6", [3]int{2, 20, 6}, true},
		{"v1.5.0", [3]int{1, 5, 0}, true},
		{"v2.21.0", [3]int{2, 21, 0}, true},
		{"dev", [3]int{}, false},
		{"", [3]int{}, false},
		{"garbage", [3]int{}, false},
		{"v2.20", [3]int{}, false},
		{"v2.20.8.1", [3]int{}, false},
		{"v2.20.x", [3]int{}, false},
		{"v2.20.-1", [3]int{}, false},
		{"  v2.20.6  ", [3]int{2, 20, 6}, true},
	} {
		got, ok := parseMachineVersion(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseMachineVersion(%q) = %v %v, want %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if !belowSigningRelease("v2.20.7") || !belowSigningRelease("2.20.6") || !belowSigningRelease("v1.9.9") {
		t.Error("pre-signing versions must pass the gate")
	}
	for _, v := range []string{"v2.20.8", "v2.20.9", "v3.0.0", "dev", "", "garbage"} {
		if belowSigningRelease(v) {
			t.Errorf("belowSigningRelease(%q) = true, want false (fail closed)", v)
		}
	}
}

// TestHeartbeatUnsignedVersionGateAndRatchet pins the gate end to end: only
// a parseable pre-v2.20.8 request on a pre-v2.20.8 row is accepted; dev,
// empty or unparseable on either side is refused and writes nothing; a
// signed v2.20.9 beat ratchets the row closed for good.
func TestHeartbeatUnsignedVersionGateAndRatchet(t *testing.T) {
	env := newEnv(t)
	registerMachine(t, env, "m-gate", env.UserID, "pk-g")
	if _, err := env.DB.Exec(`UPDATE machines SET runtime_version='v2.20.6' WHERE id='m-gate'`); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	before := map[string]string{}
	for _, col := range []string{"last_heartbeat_at", "runtime_version", "mode", "live_wi_ids"} {
		before[col] = machineField(t, env, "m-gate", col)
	}
	for _, v := range []string{"v2.20.8", "v2.20.9", "dev", "", "garbage"} {
		_, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
			MachineID: "m-gate", RuntimeVersion: v, Mode: "bad", LiveWIIDs: []string{"wi-bad"},
		}, unsignedProof(env.UserID), nil)
		if !IsCode(err, api.ErrAuthUnauthorized) {
			t.Fatalf("unsigned request version %q = %v, want %s", v, err, api.ErrAuthUnauthorized)
		}
		for col, want := range before {
			if got := machineField(t, env, "m-gate", col); got != want {
				t.Fatalf("refused beat (version %q) wrote %s: %q, want %q", v, col, got, want)
			}
		}
	}

	// Fail closed on the stored side too.
	if _, err := env.DB.Exec(`UPDATE machines SET runtime_version='dev' WHERE id='m-gate'`); err != nil {
		t.Fatalf("seed dev: %v", err)
	}
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-gate", RuntimeVersion: "v2.20.6", Mode: "run",
	}, unsignedProof(env.UserID), nil); !IsCode(err, api.ErrAuthUnauthorized) {
		t.Fatalf("unsigned beat on a stored dev row = %v, want %s (fail closed)", err, api.ErrAuthUnauthorized)
	}

	// The ratchet: a signed v2.20.9 beat stores it, and the row is closed.
	if _, err := env.DB.Exec(`UPDATE machines SET runtime_version='v2.20.6' WHERE id='m-gate'`); err != nil {
		t.Fatalf("reseed version: %v", err)
	}
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-gate", RuntimeVersion: "v2.20.9", Mode: "signed", LiveWIIDs: []string{"wi-signed"},
	}, signedProof(env.UserID, "pk-g"), nil); err != nil {
		t.Fatalf("signed v2.20.9 beat: %v", err)
	}
	if got := machineField(t, env, "m-gate", "runtime_version"); got != "v2.20.9" {
		t.Fatalf("signed beat stored runtime_version %q, want v2.20.9", got)
	}
	ratcheted := map[string]string{}
	for _, col := range []string{"last_heartbeat_at", "runtime_version", "mode", "live_wi_ids"} {
		ratcheted[col] = machineField(t, env, "m-gate", col)
	}
	if _, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{
		MachineID: "m-gate", RuntimeVersion: "v2.20.6", Mode: "old", LiveWIIDs: []string{"wi-old"},
	}, unsignedProof(env.UserID), nil); !IsCode(err, api.ErrAuthUnauthorized) {
		t.Fatalf("unsigned beat after the ratchet = %v, want %s", err, api.ErrAuthUnauthorized)
	}
	for col, want := range ratcheted {
		if got := machineField(t, env, "m-gate", col); got != want {
			t.Fatalf("ratcheted-refused beat wrote %s: %q, want %q", col, got, want)
		}
	}
}
