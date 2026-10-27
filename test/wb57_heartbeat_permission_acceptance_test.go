package test

// wb57_heartbeat_permission_acceptance_test.go: the e2e acceptance suite for
// workbench#57 (the machine-heartbeat permission model), authored BEFORE
// `code` against artifacts/spec.md only.
//
// Every case drives the shipped `aihub` binary as a real process over real
// HTTP with a real Ed25519 signing client (the machine face's own
// X-PF-* scheme, ed25519 hex over
// METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID) — no core-level
// shortcut and no symbol the pre-implementation tree lacks: responses and
// machines rows are read as raw JSON / generic sqlite columns, so the
// baseline reds are assertion-level, never build-level.
//
// Scenario document: artifacts/e2e-scenarios.md (WB57-S1..WB57-S12 plus the
// labelled guard). The guard, TestWB57Guard..., passes on base 1ce648bd by
// design and is therefore NOT part of the kept baseline (that baseline
// admits only assertion-level reds); see the scenario document.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// wb57Path is the one route this suite drives.
const wb57Path = "/api/v1/machines/heartbeat"

// ---------------------------------------------------------------------------
// Signing client helpers.

// wb57Key derives a deterministic Ed25519 identity plus its base64 wire
// spelling (the machine face's public_key encoding).
func wb57Key(t *testing.T, seed byte) (string, ed25519.PrivateKey) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), priv
}

// wb57Sign is the machine face's canonical signature (the spec reuses
// internal/common/machinesign; this file re-derives it rather than importing
// the package, so the suite tests the wire, not an internal symbol).
func wb57Sign(method, path string, body []byte, ts int64, machine string, key ed25519.PrivateKey) string {
	sum := sha256.Sum256(body)
	canon := method + "\n" + path + "\n" + hex.EncodeToString(sum[:]) + "\n" +
		strconv.FormatInt(ts, 10) + "\n" + machine
	return hex.EncodeToString(ed25519.Sign(key, []byte(canon)))
}

// wb57Ans is one heartbeat answer as the wire delivered it: status, the
// typed error code/detail when the body carried one, and the decoded JSON.
type wb57Ans struct {
	Status int
	Code   string
	Detail string
	Body   map[string]any
	Raw    string
}

func (a wb57Ans) String() string {
	return fmt.Sprintf("status=%d code=%q detail=%q body=%s", a.Status, a.Code, a.Detail, a.Raw)
}

// wb57JSON marshals a body once: the bytes signed are the bytes sent.
func wb57JSON(t *testing.T, v map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal heartbeat body: %v", err)
	}
	return b
}

// wb57Beat posts one heartbeat over real HTTP. priv != nil signs with the
// machine-face headers; ts == 0 means "now". The refusal arms assert the
// typed answer, never a transport error.
func wb57Beat(t *testing.T, inst *v1Install, bearer string, body []byte, machine string, priv ed25519.PrivateKey, ts int64) wb57Ans {
	t.Helper()
	req, err := http.NewRequest("POST", inst.url+wb57Path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build heartbeat request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	if priv != nil {
		if ts == 0 {
			ts = time.Now().Unix()
		}
		req.Header.Set("X-PF-Machine-ID", machine)
		req.Header.Set("X-PF-Timestamp", strconv.FormatInt(ts, 10))
		req.Header.Set("X-PF-Signature", wb57Sign("POST", wb57Path, body, ts, machine, priv))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("heartbeat %s: %v", machine, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	ans := wb57Ans{Status: resp.StatusCode, Raw: string(raw)}
	var typed api.Error
	if json.Unmarshal(raw, &typed) == nil {
		ans.Code, ans.Detail = typed.Code, typed.Detail
	}
	_ = json.Unmarshal(raw, &ans.Body)
	return ans
}

// wb57Register posts a first registration signed by priv and presenting pub.
// Cases whose subject is NOT the registration gate use a maintainer operator
// (one of the two identities the base tree admits) so their own assertion —
// not the gate — is the baseline red.
func wb57Register(t *testing.T, inst *v1Install, bearer, machine string, priv ed25519.PrivateKey, pub string) wb57Ans {
	t.Helper()
	body := wb57JSON(t, map[string]any{
		"machine_id": machine, "public_key": pub, "launcher": "pi-web",
		"role": "runner", "live_wi_ids": []string{},
	})
	return wb57Beat(t, inst, bearer, body, machine, priv, 0)
}

// wb57User mints one human user and an API key, optionally granting a
// project_members row with memberRole ("" = no membership row at all).
// A global admin is role "admin" with memberRole "".
func wb57User(t *testing.T, inst *v1Install, name, role, memberRole string) (userID, keyID, plain string) {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: name + "@wb57.invalid", UserType: "human", Role: role,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == "" {
		t.Fatalf("user answer for %s: %v (%s)", name, err, raw)
	}
	raw, err = inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: user.ID})
	if err != nil {
		t.Fatalf("mint key for %s: %v", name, err)
	}
	var key struct {
		Token struct {
			ID string `json:"id"`
		} `json:"token"`
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || key.Plain == "" || key.Token.ID == "" {
		t.Fatalf("key answer for %s: %v (%s)", name, err, raw)
	}
	if memberRole != "" {
		members := []aihubclient.ProjectMember{{UserID: user.ID, Role: memberRole}}
		if _, err := inst.client.PatchProject(v1Project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
			t.Fatalf("grant %s the %s membership: %v", name, memberRole, err)
		}
	}
	return user.ID, key.Token.ID, key.Plain
}

// ---------------------------------------------------------------------------
// State readers (the running aihub's on-disk db, and the GET /machines face).

func wb57DB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+inst.dataDir+"/aihub.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	return db
}

// wb57Row answers one machines row as a generic column map (nil = absent):
// the suite reads user_id/operator_id by name, so it works on both schemas.
func wb57Row(t *testing.T, inst *v1Install, id string) map[string]any {
	t.Helper()
	db := wb57DB(t, inst)
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM machines WHERE id=?1`, id)
	if err != nil {
		t.Fatalf("read machines row %q: %v", id, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("machines columns: %v", err)
	}
	if !rows.Next() {
		return nil
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatalf("scan machines row %q: %v", id, err)
	}
	out := map[string]any{}
	for i, c := range cols {
		if b, ok := vals[i].([]byte); ok {
			out[c] = string(b)
		} else {
			out[c] = vals[i]
		}
	}
	return out
}

// wb57CheckRow asserts the named fields of one machines row. It answers the
// row so a caller can keep asserting on it.
func wb57CheckRow(t *testing.T, inst *v1Install, id string, want map[string]any) map[string]any {
	t.Helper()
	row := wb57Row(t, inst, id)
	if row == nil {
		t.Fatalf("machines row %q is absent", id)
	}
	for k, v := range want {
		if row[k] != v {
			t.Fatalf("machines row %q field %s = %#v, want %#v (row: %v)", id, k, row[k], v, row)
		}
	}
	return row
}

// wb57Count counts rows matching one scalar query (a table missing on the
// pre-code tree counts as zero).
func wb57Count(t *testing.T, inst *v1Install, query string) int {
	t.Helper()
	db := wb57DB(t, inst)
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		return 0
	}
	return n
}

// wb57Columns answers the column names of one table.
func wb57Columns(t *testing.T, inst *v1Install, table string) []string {
	t.Helper()
	db := wb57DB(t, inst)
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?1)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		out = append(out, name)
	}
	return out
}

// wb57SeedRow inserts a legacy (v2.20.6-shape) machines row: a heartbeat-
// minted mac_ user owns it, public_key / operator_id are NULL when the
// argument is "". The user_id column is written only while the schema still
// has it (this change drops it), so the helper seeds the same GIVEN on both
// trees.
func wb57SeedRow(t *testing.T, inst *v1Install, id, operatorID, publicKey string) {
	t.Helper()
	db := wb57DB(t, inst)
	defer db.Close()
	mac := "mac_legacy_" + strings.NewReplacer("-", "_", ".", "_").Replace(id)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO users(id, display_name, email, kind, role, created_at)
		VALUES (?1,?2,?3,'machine','writer',?4)`, mac, "legacy "+id, mac+"@machines.local", now); err != nil {
		t.Fatalf("seed legacy machine user %q: %v", mac, err)
	}
	var op, pk any
	if operatorID != "" {
		op = operatorID
	}
	if publicKey != "" {
		pk = publicKey
	}
	if len(wb57Columns(t, inst, "machines")) == 0 || !wb57HasColumn(t, inst, "machines", "user_id") {
		_, err := db.Exec(`INSERT INTO machines(id, last_heartbeat_at, role, public_key, operator_id)
			VALUES (?1,?2,'runner',?3,?4)`, id, now, pk, op)
		if err != nil {
			t.Fatalf("seed legacy machines row %q: %v", id, err)
		}
		return
	}
	if _, err := db.Exec(`INSERT INTO machines(id, user_id, last_heartbeat_at, role, public_key, operator_id)
		VALUES (?1,?2,?3,'runner',?4,?5)`, id, mac, now, pk, op); err != nil {
		t.Fatalf("seed legacy machines row %q: %v", id, err)
	}
}

func wb57HasColumn(t *testing.T, inst *v1Install, table, column string) bool {
	t.Helper()
	for _, c := range wb57Columns(t, inst, table) {
		if c == column {
			return true
		}
	}
	return false
}

// wb57Machines reads GET /api/v1/machines as raw rows (no api struct: the
// user_id/operator_id presence is exactly what the cases assert).
func wb57Machines(t *testing.T, inst *v1Install) map[string]map[string]any {
	t.Helper()
	raw, err := inst.client.ListMachinesRaw()
	if err != nil {
		t.Fatalf("list machines: %v", err)
	}
	var list struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("machines answer undecodable: %v (%s)", err, raw)
	}
	out := map[string]map[string]any{}
	for _, m := range list.Machines {
		if id, _ := m["id"].(string); id != "" {
			out[id] = m
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// WB57-S1 (spec R1): first registration is open to every project role and to
// a global admin, and refused for a key that belongs to no project.

func TestWB57EveryProjectRoleMayRegister(t *testing.T) {
	inst := newV1Install(t)
	seeds := map[string]byte{"viewer": 0x01, "writer": 0x02, "maintainer": 0x03}
	for _, role := range []string{"viewer", "writer", "maintainer"} {
		opID, _, key := wb57User(t, inst, "wb57-role-"+role, "", role)
		pub, priv := wb57Key(t, seeds[role])
		machine := "wb57-gate-" + role
		ans := wb57Register(t, inst, key, machine, priv, pub)
		if ans.Status != http.StatusOK || ans.Body["registered"] != true {
			t.Fatalf("%s first registration was not accepted: %s", role, ans)
		}
		wb57CheckRow(t, inst, machine, map[string]any{"operator_id": opID, "public_key": pub})
	}

	// A non-admin key with no project_members row: forbidden, and nothing
	// written (no machines row, no users row).
	_, _, outsider := wb57User(t, inst, "wb57-outsider", "", "")
	usersBefore := wb57Count(t, inst, `SELECT COUNT(*) FROM users`)
	pub, priv := wb57Key(t, 0x04)
	ans := wb57Register(t, inst, outsider, "wb57-gate-outsider", priv, pub)
	if ans.Code != api.ErrAuthForbidden {
		t.Fatalf("non-member first registration = %s, want %s", ans, api.ErrAuthForbidden)
	}
	if row := wb57Row(t, inst, "wb57-gate-outsider"); row != nil {
		t.Fatalf("refused registration left a machines row: %v", row)
	}
	if after := wb57Count(t, inst, `SELECT COUNT(*) FROM users`); after != usersBefore {
		t.Fatalf("refused registration minted %d users row(s)", after-usersBefore)
	}
}

// ---------------------------------------------------------------------------
// WB57-S2 (spec R2): a first registration must carry a public key and a
// signature by that key; the v2.20.6 unsigned shape cannot register.

func TestWB57FirstRegistrationMustProveKeyPossession(t *testing.T) {
	inst := newV1Install(t)
	_, _, key := wb57User(t, inst, "wb57-proof", "", "maintainer")
	pub, priv := wb57Key(t, 0x11)
	pubA, _ := wb57Key(t, 0x12)
	_, privB := wb57Key(t, 0x13)

	// (a) correctly signed, but no public key in the body.
	body := wb57JSON(t, map[string]any{"machine_id": "wb57-nokey", "live_wi_ids": []string{}})
	if ans := wb57Beat(t, inst, key, body, "wb57-nokey", priv, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("registration without a public key = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	if row := wb57Row(t, inst, "wb57-nokey"); row != nil {
		t.Fatalf("keyless registration left a machines row: %v", row)
	}

	// (b) the v2.20.6 daemon shape: a public key, no X-PF-* signature headers.
	body = wb57JSON(t, map[string]any{"machine_id": "wb57-unsigned-reg", "public_key": pub, "live_wi_ids": []string{}})
	if ans := wb57Beat(t, inst, key, body, "", nil, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("unsigned first registration = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	if row := wb57Row(t, inst, "wb57-unsigned-reg"); row != nil {
		t.Fatalf("unsigned registration left a machines row: %v", row)
	}

	// (c) key A presented, request signed by key B.
	body = wb57JSON(t, map[string]any{"machine_id": "wb57-mismatch", "public_key": pubA, "live_wi_ids": []string{}})
	if ans := wb57Beat(t, inst, key, body, "wb57-mismatch", privB, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("registration presenting A signed by B = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	if row := wb57Row(t, inst, "wb57-mismatch"); row != nil {
		t.Fatalf("wrongly signed registration left a machines row: %v", row)
	}
}

// ---------------------------------------------------------------------------
// WB57-S3 (spec R3): an accepted beat is the operator's Bearer plus the
// bound key's signature; a different Bearer is AUTH-403, a bad signature
// AUTH-401, and neither changes the row.

func TestWB57HeartbeatRequiresOperatorBearerAndValidSignature(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-sig-op", "", "maintainer")
	_, _, otherKey := wb57User(t, inst, "wb57-sig-other", "", "writer")
	pub, priv := wb57Key(t, 0x21)
	if ans := wb57Register(t, inst, opKey, "wb57-sig", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-sig: %s", ans)
	}

	// (a) the operator's signed beat is accepted and refreshes liveness.
	body := wb57JSON(t, map[string]any{"machine_id": "wb57-sig", "live_wi_ids": []string{"wi-a"}, "mode": "run"})
	if ans := wb57Beat(t, inst, opKey, body, "wb57-sig", priv, 0); ans.Status != http.StatusOK {
		t.Fatalf("operator's signed beat = %s, want 200", ans)
	}
	row := wb57CheckRow(t, inst, "wb57-sig", map[string]any{"operator_id": opID, "public_key": pub, "role": "runner"})
	live := row["live_wi_ids"]

	// (b) a valid signature sent under another user's Bearer: 403, unchanged.
	body = wb57JSON(t, map[string]any{"machine_id": "wb57-sig", "live_wi_ids": []string{"wi-b"}})
	if ans := wb57Beat(t, inst, otherKey, body, "wb57-sig", priv, 0); ans.Code != api.ErrAuthForbidden {
		t.Fatalf("machine signature under another Bearer = %s, want %s", ans, api.ErrAuthForbidden)
	}
	wb57CheckRow(t, inst, "wb57-sig", map[string]any{"public_key": pub, "role": "runner", "live_wi_ids": live})

	// (c) the operator's Bearer with a signature by another key: 401, unchanged.
	_, wrongPriv := wb57Key(t, 0x29)
	body = wb57JSON(t, map[string]any{"machine_id": "wb57-sig", "live_wi_ids": []string{"wi-c"}})
	if ans := wb57Beat(t, inst, opKey, body, "wb57-sig", wrongPriv, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("operator Bearer with a foreign signature = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	wb57CheckRow(t, inst, "wb57-sig", map[string]any{"public_key": pub, "role": "runner", "live_wi_ids": live})
}

// ---------------------------------------------------------------------------
// WB57-S4 (spec R3/R4): a beat from anyone but the operator has no effect —
// signed or unsigned, even a liveness-only body.

func TestWB57NonOperatorBeatChangesNothing(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-third-op", "", "maintainer")
	_, _, otherKey := wb57User(t, inst, "wb57-third-other", "", "writer")
	pub, priv := wb57Key(t, 0x31)
	if ans := wb57Register(t, inst, opKey, "wb57-third", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-third: %s", ans)
	}
	before := wb57CheckRow(t, inst, "wb57-third", map[string]any{"operator_id": opID, "public_key": pub, "role": "runner", "launcher": "pi-web"})
	live := before["live_wi_ids"]

	// (a) signed by the bound key, but under another user's Bearer.
	body := wb57JSON(t, map[string]any{"machine_id": "wb57-third", "live_wi_ids": []string{"wi-third"}})
	if ans := wb57Beat(t, inst, otherKey, body, "wb57-third", priv, 0); ans.Status == http.StatusOK {
		t.Fatalf("third-party signed beat was accepted: %s", ans)
	}
	wb57CheckRow(t, inst, "wb57-third", map[string]any{"operator_id": opID, "public_key": pub, "role": "runner", "launcher": "pi-web", "live_wi_ids": live})

	// (b) unsigned, from another authenticated user.
	if ans := wb57Beat(t, inst, otherKey, body, "", nil, 0); ans.Code != api.ErrAuthForbidden {
		t.Fatalf("third-party unsigned beat = %s, want %s", ans, api.ErrAuthForbidden)
	}
	wb57CheckRow(t, inst, "wb57-third", map[string]any{"operator_id": opID, "public_key": pub, "role": "runner", "launcher": "pi-web", "live_wi_ids": live})
}

// ---------------------------------------------------------------------------
// WB57-S5 (spec R4): the operator's unsigned beat is the bounded transition
// only — it refreshes liveness and never identity, role, capability or key.

func TestWB57UnsignedOperatorBeatTouchesLivenessOnly(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-transition-op", "", "maintainer")
	pub, priv := wb57Key(t, 0x41)
	pubOther, _ := wb57Key(t, 0x42)
	if ans := wb57Register(t, inst, opKey, "wb57-transition", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-transition: %s", ans)
	}

	body := wb57JSON(t, map[string]any{
		"machine_id": "wb57-transition", "runtime_version": "v2.20.6",
		"mode": "run", "live_wi_ids": []string{"wi-u"},
		"role": "interactive", "launcher": "claude", "public_key": pubOther,
		"capability": map[string]any{"faces": []string{"relay"}},
	})
	ans := wb57Beat(t, inst, opKey, body, "", nil, 0)
	if ans.Status != http.StatusOK {
		t.Fatalf("operator's unsigned transition beat = %s, want 200", ans)
	}
	row := wb57CheckRow(t, inst, "wb57-transition", map[string]any{
		"operator_id": opID, "public_key": pub, "role": "runner", "launcher": "pi-web",
		"capability": nil, "mode": "run", "runtime_version": "v2.20.6",
	})
	if live, _ := row["live_wi_ids"].(string); !strings.Contains(live, "wi-u") {
		t.Fatalf("unsigned transition beat did not refresh live_wi_ids: %v", row)
	}
}

// ---------------------------------------------------------------------------
// WB57-S6 (spec R7): a byte-identical replay, and a timestamp outside the
// window, are AUTH-401 with no state change.

func TestWB57SignedBeatReplayAndStaleAreRefused(t *testing.T) {
	inst := newV1Install(t)
	_, _, opKey := wb57User(t, inst, "wb57-replay-op", "", "maintainer")
	pub, priv := wb57Key(t, 0x51)
	if ans := wb57Register(t, inst, opKey, "wb57-replay", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-replay: %s", ans)
	}

	ts := time.Now().Unix()
	first := wb57JSON(t, map[string]any{"machine_id": "wb57-replay", "live_wi_ids": []string{"wi-first"}})
	if ans := wb57Beat(t, inst, opKey, first, "wb57-replay", priv, ts); ans.Status != http.StatusOK {
		t.Fatalf("first signed beat = %s, want 200", ans)
	}
	second := wb57JSON(t, map[string]any{"machine_id": "wb57-replay", "live_wi_ids": []string{"wi-second"}})
	if ans := wb57Beat(t, inst, opKey, second, "wb57-replay", priv, 0); ans.Status != http.StatusOK {
		t.Fatalf("second signed beat = %s, want 200", ans)
	}
	live := wb57Row(t, inst, "wb57-replay")["live_wi_ids"]

	// The verbatim replay: same body bytes, same timestamp, same signature.
	if ans := wb57Beat(t, inst, opKey, first, "wb57-replay", priv, ts); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("replayed heartbeat = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	wb57CheckRow(t, inst, "wb57-replay", map[string]any{"live_wi_ids": live})

	// A correctly signed beat an hour old is outside the ±300 s window.
	if ans := wb57Beat(t, inst, opKey, second, "wb57-replay", priv, time.Now().Unix()-3600); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("stale signed heartbeat = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	wb57CheckRow(t, inst, "wb57-replay", map[string]any{"live_wi_ids": live})
}

// ---------------------------------------------------------------------------
// WB57-S7 (spec R5): a NULL-key legacy row binds only the key that signs the
// binding beat; an unsigned or wrongly signed beat leaves it unbound.

func TestWB57NullKeyRowBindsOnlyTheSigningKey(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-nullkey-op", "", "maintainer")
	pubK, privK := wb57Key(t, 0x61)
	_, privOther := wb57Key(t, 0x62)
	wb57SeedRow(t, inst, "wb57-nullkey", opID, "")
	body := wb57JSON(t, map[string]any{"machine_id": "wb57-nullkey", "public_key": pubK, "live_wi_ids": []string{}})

	// (a) unsigned, presenting a key: refused, still unbound.
	if ans := wb57Beat(t, inst, opKey, body, "", nil, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("unsigned bind on a NULL-key row = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	wb57CheckRow(t, inst, "wb57-nullkey", map[string]any{"public_key": nil})

	// (b) signed by a key other than the one it presents: refused, unbound.
	if ans := wb57Beat(t, inst, opKey, body, "wb57-nullkey", privOther, 0); ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("wrongly signed bind on a NULL-key row = %s, want %s", ans, api.ErrAuthUnauthorized)
	}
	wb57CheckRow(t, inst, "wb57-nullkey", map[string]any{"public_key": nil})

	// (c) the beat signed by the presented key binds it.
	if ans := wb57Beat(t, inst, opKey, body, "wb57-nullkey", privK, 0); ans.Status != http.StatusOK {
		t.Fatalf("signed bind on a NULL-key row = %s, want 200", ans)
	}
	wb57CheckRow(t, inst, "wb57-nullkey", map[string]any{"public_key": pubK, "operator_id": opID})
}

// ---------------------------------------------------------------------------
// WB57-S8 (spec R5): a NULL-operator legacy row is claimed only by a signed
// beat that passes the registration gate.

func TestWB57NullOperatorRowOnlyASignedEligibleBeatClaimsIt(t *testing.T) {
	inst := newV1Install(t)
	_, _, outsiderKey := wb57User(t, inst, "wb57-claim-outsider", "", "")
	viewerID, _, viewerKey := wb57User(t, inst, "wb57-claim-viewer", "", "viewer")
	pubK, privK := wb57Key(t, 0x71)
	wb57SeedRow(t, inst, "wb57-nullop", "", "")
	body := wb57JSON(t, map[string]any{"machine_id": "wb57-nullop", "public_key": pubK, "live_wi_ids": []string{}})

	// (a) a non-admin key with no membership: refused, operator stays NULL.
	if ans := wb57Beat(t, inst, outsiderKey, body, "wb57-nullop", privK, 0); ans.Status == http.StatusOK {
		t.Fatalf("non-member claimed a NULL-operator row: %s", ans)
	}
	wb57CheckRow(t, inst, "wb57-nullop", map[string]any{"operator_id": nil, "public_key": nil})

	// (b) unsigned, from an eligible member: still refused.
	if ans := wb57Beat(t, inst, viewerKey, body, "", nil, 0); ans.Status == http.StatusOK {
		t.Fatalf("unsigned beat claimed a NULL-operator row: %s", ans)
	}
	wb57CheckRow(t, inst, "wb57-nullop", map[string]any{"operator_id": nil, "public_key": nil})

	// (c) a project member's signed beat: that user becomes the operator.
	if ans := wb57Beat(t, inst, viewerKey, body, "wb57-nullop", privK, 0); ans.Status != http.StatusOK {
		t.Fatalf("member's signed claim of a NULL-operator row = %s, want 200", ans)
	}
	wb57CheckRow(t, inst, "wb57-nullop", map[string]any{"operator_id": viewerID, "public_key": pubK})
}

// ---------------------------------------------------------------------------
// WB57-S9 (spec R8): a machine_id registered to another operator is refused
// with actionable advice, and the existing row is untouched.

func TestWB57MachineIDCollisionIsRefusedWithAdvice(t *testing.T) {
	inst := newV1Install(t)
	opAID, _, keyA := wb57User(t, inst, "wb57-host-a", "", "maintainer")
	_, _, keyB := wb57User(t, inst, "wb57-host-b", "", "writer")
	pubA, privA := wb57Key(t, 0x81)
	_, privB := wb57Key(t, 0x82)
	if ans := wb57Register(t, inst, keyA, "m-host", privA, pubA); ans.Status != http.StatusOK {
		t.Fatalf("premise: A registers m-host: %s", ans)
	}
	live := wb57Row(t, inst, "m-host")["live_wi_ids"]

	body := wb57JSON(t, map[string]any{"machine_id": "m-host", "live_wi_ids": []string{"wi-b"}})
	ans := wb57Beat(t, inst, keyB, body, "m-host", privB, 0)
	if ans.Code != api.ErrAuthForbidden {
		t.Fatalf("collision heartbeat = %s, want %s", ans, api.ErrAuthForbidden)
	}
	if !strings.Contains(ans.Detail, "--machine-id") {
		t.Fatalf("collision refusal does not suggest a different --machine-id: %s", ans)
	}
	if !strings.Contains(ans.Detail, opAID) && !strings.Contains(ans.Detail, "wb57-host-a") {
		t.Fatalf("collision refusal does not name the existing operator: %s", ans)
	}
	wb57CheckRow(t, inst, "m-host", map[string]any{"operator_id": opAID, "public_key": pubA, "live_wi_ids": live})
	if n := wb57Count(t, inst, `SELECT COUNT(*) FROM machines`); n != 1 {
		t.Fatalf("collision left %d machines rows, want 1", n)
	}

	// The same operator keeps using the id.
	if ans := wb57Beat(t, inst, keyA, body, "m-host", privA, 0); ans.Status != http.StatusOK {
		t.Fatalf("owner's own heartbeat after a collision = %s, want 200", ans)
	}
}

// ---------------------------------------------------------------------------
// WB57-S10 (spec R3): admin /rekey is the only rebind, and the retired key
// stops being accepted afterwards.

func TestWB57AdminRekeyRebindsAndRetiresTheOldKey(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-rekey-op", "", "maintainer")
	pubOld, privOld := wb57Key(t, 0x91)
	pubNew, privNew := wb57Key(t, 0x92)
	if ans := wb57Register(t, inst, opKey, "wb57-rekey", privOld, pubOld); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-rekey: %s", ans)
	}
	if _, err := inst.client.RekeyMachine("wb57-rekey", pubNew); err != nil {
		t.Fatalf("admin rekey: %v", err)
	}
	wb57CheckRow(t, inst, "wb57-rekey", map[string]any{"public_key": pubNew, "operator_id": opID})

	body := wb57JSON(t, map[string]any{"machine_id": "wb57-rekey", "live_wi_ids": []string{"wi-old-key"}})
	live := wb57Row(t, inst, "wb57-rekey")["live_wi_ids"]
	if ans := wb57Beat(t, inst, opKey, body, "wb57-rekey", privOld, 0); ans.Status == http.StatusOK {
		t.Fatalf("heartbeat signed by the retired key was accepted: %s", ans)
	}
	wb57CheckRow(t, inst, "wb57-rekey", map[string]any{"public_key": pubNew, "live_wi_ids": live})

	if ans := wb57Beat(t, inst, opKey, body, "wb57-rekey", privNew, 0); ans.Status != http.StatusOK {
		t.Fatalf("heartbeat signed by the new key = %s, want 200", ans)
	}
}

// ---------------------------------------------------------------------------
// WB57-S11 (spec R6 read face): a machine's identity is operator_id; no
// aihub surface answers it with a machine user any more.

func TestWB57MachineIdentityIsOperatorIDNotUserID(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-face-op", "", "maintainer")
	pub, priv := wb57Key(t, 0xa1)
	ans := wb57Register(t, inst, opKey, "wb57-face", priv, pub)
	if ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-face: %s", ans)
	}
	if _, ok := ans.Body["user_id"]; ok {
		t.Fatalf("heartbeat result still reports user_id: %s", ans)
	}
	if ans.Body["operator_id"] != opID {
		t.Fatalf("heartbeat result operator_id = %v, want %q: %s", ans.Body["operator_id"], opID, ans)
	}

	rows := wb57Machines(t, inst)
	if len(rows) == 0 {
		t.Fatal("GET /machines answered no rows")
	}
	for id, row := range rows {
		if _, ok := row["user_id"]; ok {
			t.Fatalf("GET /machines row %q still reports user_id: %v", id, row)
		}
		if op, _ := row["operator_id"].(string); op == "" {
			t.Fatalf("GET /machines row %q has no operator_id: %v", id, row)
		}
	}
	if got := rows["wb57-face"]["operator_id"]; got != opID {
		t.Fatalf("GET /machines operator_id = %v, want %q", got, opID)
	}
}

// ---------------------------------------------------------------------------
// WB57-S12 (spec R6 schema): registering a machine no longer depends on a
// machine user — the fresh schema has no machines.user_id and a heartbeat
// writes no users row.

func TestWB57FreshSchemaHasNoMachineUserColumn(t *testing.T) {
	inst := newV1Install(t)
	if wb57HasColumn(t, inst, "machines", "user_id") {
		t.Fatalf("fresh machines schema still has user_id: %v", wb57Columns(t, inst, "machines"))
	}

	// Snapshot AFTER the operator exists: wb57User creates the operator's
	// users row, so the count must only cover the registration (owner
	// answered wb57-s12-user-count; the original snapshot was one user
	// too early and no correct implementation could pass it).
	_, _, opKey := wb57User(t, inst, "wb57-schema-op", "", "maintainer")
	usersBefore := wb57Count(t, inst, `SELECT COUNT(*) FROM users`)
	pub, priv := wb57Key(t, 0xb1)
	if ans := wb57Register(t, inst, opKey, "wb57-schema", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("register wb57-schema: %s", ans)
	}
	if after := wb57Count(t, inst, `SELECT COUNT(*) FROM users`); after != usersBefore {
		t.Fatalf("a heartbeat minted %d users row(s); machines must not depend on a machine user", after-usersBefore)
	}
	wb57CheckRow(t, inst, "wb57-schema", map[string]any{"public_key": pub})
}

// ---------------------------------------------------------------------------
// The labelled guard (spec R4): the operator's unsigned, liveness-only beat
// still works during the transition. This case passes on base 1ce648bd by
// design, so it is NOT a kept scenario in artifacts/e2e-scenarios.md's
// baseline (that list admits only assertion-level reds); it is kept as the
// named guard the spec asks for.

func TestWB57GuardUnsignedOperatorBeatAccepted(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := wb57User(t, inst, "wb57-guard-op", "", "maintainer")
	pub, priv := wb57Key(t, 0xc1)
	if ans := wb57Register(t, inst, opKey, "wb57-guard", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register wb57-guard: %s", ans)
	}
	body := wb57JSON(t, map[string]any{
		"machine_id": "wb57-guard", "runtime_version": "v2.20.6",
		"mode": "run", "live_wi_ids": []string{"wi-guard"},
	})
	if ans := wb57Beat(t, inst, opKey, body, "", nil, 0); ans.Status != http.StatusOK {
		t.Fatalf("guard: operator's unsigned liveness beat = %s, want 200", ans)
	}
	row := wb57CheckRow(t, inst, "wb57-guard", map[string]any{"operator_id": opID, "public_key": pub, "role": "runner"})
	if live, _ := row["live_wi_ids"].(string); !strings.Contains(live, "wi-guard") {
		t.Fatalf("guard: unsigned beat did not refresh liveness: %v", row)
	}
}
