package server

// machines_test.go: the machines faces over real HTTP — the M3 liveness read
// face (admin-only auth, the stale projection against the configured
// threshold, the cannot-judge case) and, since workbench#57, the
// machine-heartbeat permission model: who may register, the operator-plus-
// signature refresh, replay/staleness, and the machine_id collision.

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

const hbPath = "/api/v1/machines/heartbeat"

type machinesResp struct {
	Machines []struct {
		ID                  string   `json:"id"`
		OperatorID          string   `json:"operator_id"`
		RuntimeVersion      string   `json:"runtime_version"`
		Mode                string   `json:"mode"`
		LiveWIIDs           []string `json:"live_wi_ids"`
		LastHeartbeatAt     string   `json:"last_heartbeat_at"`
		HeartbeatAgeSeconds *int64   `json:"heartbeat_age_seconds"`
		Stale               bool     `json:"stale"`
	} `json:"machines"`
	StaleAfterSeconds int64 `json:"stale_after_seconds"`
	Count             int   `json:"count"`
}

// registerSigned posts one signed first registration and fails on a refusal.
func (e *srvEnv) registerSigned(t *testing.T, key, machineID, pub string, priv ed25519.PrivateKey) {
	t.Helper()
	code, body := e.doSigned(t, "POST", hbPath, key, machineID, priv, 0, map[string]any{
		"machine_id": machineID, "public_key": pub, "launcher": "pi-web",
		"role": "runner", "live_wi_ids": []string{},
	})
	if code != http.StatusOK {
		t.Fatalf("register %s: %d %s", machineID, code, body)
	}
}

// memberUser mints one human user with an api key, optionally granting a
// membership row in project alpha ("" = no project_members row at all).
func memberUser(t *testing.T, e *srvEnv, name, memberRole string) (userID, key string) {
	t.Helper()
	u, err := e.admin.CreateUser(name, name+"@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	k, err := e.admin.CreateAPIKey(u.ID, name, "")
	if err != nil {
		t.Fatalf("key for %s: %v", name, err)
	}
	if memberRole != "" {
		projectID := e.adminProjectID(t, "alpha")
		if code, raw := e.do(t, "PATCH", "/api/v1/projects/"+projectID, e.key,
			map[string]any{"members": []map[string]string{{"user_id": u.ID, "role": memberRole}}}, nil); code != http.StatusOK {
			t.Fatalf("membership for %s: %d %s", name, code, raw)
		}
	}
	return u.ID, k.Plain
}

// machineRow answers one machines column as a string (” for NULL).
func (e *srvEnv) machineRow(t *testing.T, id, column string) string {
	t.Helper()
	var v string
	if err := e.db.QueryRow(`SELECT COALESCE(`+column+`,'') FROM machines WHERE id=?1`, id).Scan(&v); err != nil {
		t.Fatalf("read %s.%s: %v", id, column, err)
	}
	return v
}

func TestMachinesFaceOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	e.srv.StaleAfter = 2 * time.Second // test-sized window (production default 5m)

	// Two machines (both signed first registrations under the admin): one
	// stale (backdated stamp), one fresh.
	pubStale, privStale := machineKey(t)
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-stale", privStale, 0, map[string]any{
		"machine_id": "m-stale", "public_key": pubStale, "mode": "manual",
		"live_wi_ids": []string{"p#7"}, "runtime_version": "v1.5"}); code != http.StatusOK {
		t.Fatalf("register m-stale: %d %s", code, body)
	}
	pubFresh, privFresh := machineKey(t)
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-fresh", privFresh, 0, map[string]any{
		"machine_id": "m-fresh", "public_key": pubFresh, "mode": "auto"}); code != http.StatusOK {
		t.Fatalf("register m-fresh: %d %s", code, body)
	}
	backdated := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := e.db.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id='m-stale'`, backdated); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	var out machinesResp
	code, raw := e.do(t, "GET", "/api/v1/machines", e.key, nil, &out)
	if code != 200 {
		t.Fatalf("GET machines: %d", code)
	}
	if out.Count != 2 || len(out.Machines) != 2 || out.StaleAfterSeconds != 2 {
		t.Fatalf("listing shape: %+v", out)
	}
	if strings.Contains(string(raw), `"user_id"`) {
		t.Fatalf("GET /machines still reports user_id: %s", raw)
	}
	for i := range out.Machines {
		m := &out.Machines[i]
		if m.OperatorID != e.adminID {
			t.Fatalf("%s operator_id = %q, want %q", m.ID, m.OperatorID, e.adminID)
		}
		switch m.ID {
		case "m-stale":
			if !m.Stale {
				t.Fatalf("m-stale must be stale: %+v", m)
			}
			if m.Mode != "manual" || m.RuntimeVersion != "v1.5" || len(m.LiveWIIDs) != 1 || m.LiveWIIDs[0] != "p#7" {
				t.Fatalf("m-stale projection: %+v", m)
			}
			if m.HeartbeatAgeSeconds == nil || *m.HeartbeatAgeSeconds < 3500 {
				t.Fatalf("m-stale age: %+v", m.HeartbeatAgeSeconds)
			}
			if _, err := time.Parse(time.RFC3339, m.LastHeartbeatAt); err != nil {
				t.Fatalf("m-stale stamp: %v", err)
			}
		case "m-fresh":
			if m.Stale {
				t.Fatalf("m-fresh must not be stale: %+v", m)
			}
			if m.Mode != "auto" {
				t.Fatalf("m-fresh mode: %+v", m)
			}
			if m.HeartbeatAgeSeconds == nil || *m.HeartbeatAgeSeconds > 60 {
				t.Fatalf("m-fresh age: %+v", m.HeartbeatAgeSeconds)
			}
		default:
			t.Fatalf("unexpected machine %q", m.ID)
		}
	}
}

func TestMachinesFaceAuth(t *testing.T) {
	e := newSrvEnv(t)
	// No key → 401.
	if code, _ := e.do(t, "GET", "/api/v1/machines", "", nil, nil); code != 401 {
		t.Fatalf("no key: %d", code)
	}
	// Writer (non-admin) → 403: the face exposes every machine's fleet view.
	u2, err := e.admin.CreateUser("wr", "wr@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	k2, err := e.admin.CreateAPIKey(u2.ID, "wr", "")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if code, _ := e.do(t, "GET", "/api/v1/machines", k2.Plain, nil, nil); code != 403 {
		t.Fatalf("writer key: %d", code)
	}
	// Admin → 200 with an empty (non-null) listing.
	if code, body := e.do(t, "GET", "/api/v1/machines", e.key, nil, nil); code != 200 {
		t.Fatalf("admin: %d %s", code, body)
	} else {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		if string(raw["machines"]) == "null" {
			t.Fatalf("machines must be [] not null: %s", body)
		}
	}
}

// TestHeartbeatRegistrationGate pins workbench#57 R1: any project membership
// (viewer, writer or maintainer) and a global admin outside every project
// may register; a key that belongs to no project is AUTH-403 and writes
// nothing.
func TestHeartbeatRegistrationGate(t *testing.T) {
	e := newSrvEnv(t)

	// A non-admin key with no project_members row: forbidden, nothing
	// written (no machines row, no users row minted).
	_, outsiderKey := memberUser(t, e, "wb57-outsider", "")
	var usersBefore int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&usersBefore); err != nil {
		t.Fatalf("count users: %v", err)
	}
	pub, priv := machineKey(t)
	code, body := e.doSigned(t, "POST", hbPath, outsiderKey, "m-outsider", priv, 0, map[string]any{
		"machine_id": "m-outsider", "public_key": pub, "live_wi_ids": []string{},
	})
	if code != http.StatusForbidden || errCodeOf(body) != api.ErrAuthForbidden {
		t.Fatalf("non-member registration = %d %s, want 403 AUTH-403", code, body)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id='m-outsider'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused registration left a machines row: n=%d err=%v", n, err)
	}
	var usersAfter int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&usersAfter); err != nil || usersAfter != usersBefore {
		t.Fatalf("refused registration minted %d users row(s)", usersAfter-usersBefore)
	}

	// Every project role registers its own machine; the answer carries
	// operator_id, never user_id, and no machine user is minted.
	for _, role := range []string{"viewer", "writer", "maintainer"} {
		opID, opKey := memberUser(t, e, "wb57-role-"+role, role)
		pub, priv := machineKey(t)
		machine := "m-gate-" + role
		code, body := e.doSigned(t, "POST", hbPath, opKey, machine, priv, 0, map[string]any{
			"machine_id": machine, "public_key": pub, "live_wi_ids": []string{},
		})
		if code != http.StatusOK {
			t.Fatalf("%s registration = %d %s, want 200", role, code, body)
		}
		var hb struct {
			Registered bool   `json:"registered"`
			OperatorID string `json:"operator_id"`
		}
		if err := json.Unmarshal(body, &hb); err != nil || !hb.Registered || hb.OperatorID != opID {
			t.Fatalf("%s registration answer: %s", role, body)
		}
		if strings.Contains(string(body), `"user_id"`) {
			t.Fatalf("%s registration still reports user_id: %s", role, body)
		}
		if got := e.machineRow(t, machine, "operator_id"); got != opID {
			t.Fatalf("%s machine operator_id = %q, want %q", role, got, opID)
		}
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email LIKE '%@machines.local'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a registration minted a machine user: n=%d err=%v", n, err)
	}

	// A global admin outside every project qualifies too, and a signed
	// refresh of its own row works without a presented public key.
	pubAdmin, privAdmin := machineKey(t)
	e.registerSigned(t, e.key, "m-admin-out", pubAdmin, privAdmin)
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-admin-out", privAdmin, 0, map[string]any{
		"machine_id": "m-admin-out", "live_wi_ids": []string{"wi-admin"},
	}); code != http.StatusOK {
		t.Fatalf("admin refresh = %d %s", code, body)
	}
}

// TestHeartbeatRefreshMatrix pins R3/R7/R9 over HTTP: the operator's signed
// beat is accepted, a valid signature under another Bearer is AUTH-403, the
// operator's Bearer with a foreign signature is AUTH-401, a replay and a
// stale timestamp are AUTH-401, and no refusal writes.
func TestHeartbeatRefreshMatrix(t *testing.T) {
	e := newSrvEnv(t)
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-sig", pub, priv)
	_, otherKey := memberUser(t, e, "wb57-other", "writer")

	// (a) the operator's signed beat refreshes.
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-sig", priv, 0, map[string]any{
		"machine_id": "m-sig", "live_wi_ids": []string{"wi-a"}, "mode": "run",
	}); code != http.StatusOK {
		t.Fatalf("operator's signed beat = %d %s, want 200", code, body)
	}
	live := e.machineRow(t, "m-sig", "live_wi_ids")
	if !strings.Contains(live, "wi-a") {
		t.Fatalf("signed beat did not refresh live_wi_ids: %q", live)
	}

	// (b) the same valid signature under another user's Bearer: AUTH-403,
	// unchanged (and the collision message is actionable).
	code, body := e.doSigned(t, "POST", hbPath, otherKey, "m-sig", priv, 0, map[string]any{
		"machine_id": "m-sig", "live_wi_ids": []string{"wi-b"},
	})
	if code != http.StatusForbidden || errCodeOf(body) != api.ErrAuthForbidden {
		t.Fatalf("machine signature under another Bearer = %d %s, want 403", code, body)
	}
	if !strings.Contains(string(body), "--machine-id") {
		t.Fatalf("collision refusal does not suggest a different --machine-id: %s", body)
	}
	if got := e.machineRow(t, "m-sig", "live_wi_ids"); got != live {
		t.Fatalf("a refused beat changed live_wi_ids: %q", got)
	}

	// (c) the operator's Bearer with a foreign signature: AUTH-401.
	_, wrongPriv := machineKey(t)
	code, body = e.doSigned(t, "POST", hbPath, e.key, "m-sig", wrongPriv, 0, map[string]any{
		"machine_id": "m-sig", "live_wi_ids": []string{"wi-c"},
	})
	if code != http.StatusUnauthorized || errCodeOf(body) != api.ErrAuthUnauthorized {
		t.Fatalf("operator Bearer with a foreign signature = %d %s, want 401", code, body)
	}
	if got := e.machineRow(t, "m-sig", "live_wi_ids"); got != live {
		t.Fatalf("a bad signature changed live_wi_ids: %q", got)
	}

	// (d) a byte-identical replay of an accepted beat: AUTH-401, unchanged.
	ts := time.Now().Unix()
	replayBody := map[string]any{"machine_id": "m-sig", "live_wi_ids": []string{"wi-d"}}
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-sig", priv, ts, replayBody); code != http.StatusOK {
		t.Fatalf("beat to replay = %d %s, want 200", code, body)
	}
	live = e.machineRow(t, "m-sig", "live_wi_ids")
	if code, body := e.doSigned(t, "POST", hbPath, e.key, "m-sig", priv, ts, replayBody); code != http.StatusUnauthorized || errCodeOf(body) != api.ErrAuthUnauthorized {
		t.Fatalf("replayed beat = %d %s, want 401", code, body)
	}
	if got := e.machineRow(t, "m-sig", "live_wi_ids"); got != live {
		t.Fatalf("a replayed beat changed live_wi_ids: %q", got)
	}

	// (e) a correctly signed beat an hour old: AUTH-401 (outside ±300 s).
	code, body = e.doSigned(t, "POST", hbPath, e.key, "m-sig", priv, time.Now().Unix()-3600,
		map[string]any{"machine_id": "m-sig", "live_wi_ids": []string{"wi-e"}})
	if code != http.StatusUnauthorized || errCodeOf(body) != api.ErrAuthUnauthorized {
		t.Fatalf("stale signed beat = %d %s, want 401", code, body)
	}
	if got := e.machineRow(t, "m-sig", "live_wi_ids"); got != live {
		t.Fatalf("a stale beat changed live_wi_ids: %q", got)
	}

	// (f) a third party's unsigned beat: AUTH-403, no field changed.
	code, body = e.doSigned(t, "POST", hbPath, otherKey, "m-sig", nil, 0,
		map[string]any{"machine_id": "m-sig", "mode": "x", "live_wi_ids": []string{"wi-f"}})
	if code != http.StatusForbidden || errCodeOf(body) != api.ErrAuthForbidden {
		t.Fatalf("third-party unsigned beat = %d %s, want 403", code, body)
	}
	if got := e.machineRow(t, "m-sig", "live_wi_ids"); got != live {
		t.Fatalf("a third-party beat changed live_wi_ids: %q", got)
	}
	if got := e.machineRow(t, "m-sig", "mode"); got != "run" {
		t.Fatalf("a third-party beat changed mode to %q", got)
	}
}

// TestHeartbeatUnsignedTransition pins R6/guard: the operator's unsigned
// beat is accepted and only liveness moves; the identity fields a v2.20.6
// daemon restates are ignored.
func TestHeartbeatUnsignedTransition(t *testing.T) {
	e := newSrvEnv(t)
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-trans", pub, priv)
	pubOther, _ := machineKey(t)

	code, body := e.doSigned(t, "POST", hbPath, e.key, "m-trans", nil, 0, map[string]any{
		"machine_id": "m-trans", "runtime_version": "v2.20.6", "mode": "run",
		"live_wi_ids": []string{"wi-u"}, "role": "interactive", "launcher": "claude",
		"public_key": pubOther,
	})
	if code != http.StatusOK {
		t.Fatalf("operator's unsigned transition beat = %d %s, want 200", code, body)
	}
	for _, c := range []struct{ column, want string }{
		{"operator_id", e.adminID},
		{"public_key", pub},
		{"role", "runner"},
		{"launcher", "pi-web"},
		{"capability", ""},
		{"mode", "run"},
		{"runtime_version", "v2.20.6"},
	} {
		if got := e.machineRow(t, "m-trans", c.column); got != c.want {
			t.Errorf("unsigned beat wrote %s = %q, want %q", c.column, got, c.want)
		}
	}
	if live := e.machineRow(t, "m-trans", "live_wi_ids"); !strings.Contains(live, "wi-u") {
		t.Errorf("unsigned beat did not refresh live_wi_ids: %q", live)
	}
}

// TestHeartbeatRegistrationNeedsProof pins R2: a first registration without
// a public key, unsigned, or signed by a key other than the one it presents
// is AUTH-401 and creates nothing.
func TestHeartbeatRegistrationNeedsProof(t *testing.T) {
	e := newSrvEnv(t)
	pubA, _ := machineKey(t)
	_, privB := machineKey(t)
	cases := []struct {
		name string
		body map[string]any
		priv ed25519.PrivateKey
	}{
		{"no public key", map[string]any{"machine_id": "m-nokey", "live_wi_ids": []string{}}, privB},
		{"unsigned", map[string]any{"machine_id": "m-unsigned", "public_key": pubA, "live_wi_ids": []string{}}, nil},
		{"signed by another key", map[string]any{"machine_id": "m-mismatch", "public_key": pubA, "live_wi_ids": []string{}}, privB},
	}
	for _, c := range cases {
		code, body := e.doSigned(t, "POST", hbPath, e.key, c.body["machine_id"].(string), c.priv, 0, c.body)
		if code != http.StatusUnauthorized || errCodeOf(body) != api.ErrAuthUnauthorized {
			t.Errorf("%s = %d %s, want 401", c.name, code, body)
		}
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id=?1`, c.body["machine_id"]).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s left a machines row: n=%d err=%v", c.name, n, err)
		}
	}
}

func TestMachinesFaceUnparseableStamp(t *testing.T) {
	e := newSrvEnv(t)
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-weird", pub, priv)
	if _, err := e.db.Exec(`UPDATE machines SET last_heartbeat_at='not-a-time' WHERE id='m-weird'`); err != nil {
		t.Fatalf("corrupt stamp: %v", err)
	}
	var out machinesResp
	if code, _ := e.do(t, "GET", "/api/v1/machines", e.key, nil, &out); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if len(out.Machines) != 1 {
		t.Fatalf("listing: %+v", out)
	}
	m := out.Machines[0]
	// Cannot-judge is not dead: no stale claim, no invented age.
	if m.Stale || m.HeartbeatAgeSeconds != nil {
		t.Fatalf("unparseable stamp must report stale=false, age omitted: %+v", m)
	}
}
