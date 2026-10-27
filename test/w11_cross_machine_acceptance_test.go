package test

// w11_cross_machine_acceptance_test.go: the e2e acceptance suite for
// workbench#11 (cross-machine agent communication), authored BEFORE
// `code` against `artifacts/spec.md` + `artifacts/plan.md` only.
//
// Every scenario drives the shipped `aihub` and `pf` binaries as real
// processes (no implementation internal is imported and no symbol that
// does not yet exist is referenced): the new wire faces go through the
// typed client's generic Do, the new CLI verbs through the real pf
// binary, and the cross-machine query through two real `pf daemon`
// processes. The baseline reds are therefore assertion-level, never
// build-level.
//
// Scenario document: artifacts/e2e-scenarios.md (W11-S1..W11-S6).

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// w11 helpers.

// w11Key derives a deterministic Ed25519 identity and its base64 public
// half (the wire spelling).
func w11Key(t *testing.T, seed byte) (string, ed25519.PrivateKey) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), priv
}

// w11ErrCode extracts a typed api error code ("" when err is nil,
// "ERR:<msg>" when it is not a typed error).
func w11ErrCode(err error) string {
	if err == nil {
		return ""
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return "ERR:" + err.Error()
}

// w11Heartbeat posts one SIGNED heartbeat (workbench#57: the heartbeat is a
// machine-face request) and returns the decoded answer. priv is the key the
// body's public_key presents — or the row's bound key when the body presents
// none (the superseded refresh case).
func w11Heartbeat(t *testing.T, c *aihubclient.Client, body map[string]any, priv ed25519.PrivateKey) map[string]any {
	t.Helper()
	machine, _ := body["machine_id"].(string)
	if machine == "" || priv == nil {
		t.Fatalf("heartbeat %v needs a machine_id and a signing key", body)
	}
	c.SetMachineIdentity(machine, priv)
	var out map[string]any
	if err := c.SignedDo("POST", "/api/v1/machines/heartbeat", body, &out); err != nil {
		t.Fatalf("heartbeat %v: %v", machine, err)
	}
	return out
}

// w11MachineRows reads the machines liveness face as raw rows (the new
// role/launcher/public_key fields are not part of the frozen
// aihubclient.MachineStatus type).
func w11MachineRows(t *testing.T, c *aihubclient.Client) map[string]map[string]any {
	t.Helper()
	raw, err := c.ListMachinesRaw()
	if err != nil {
		t.Fatalf("list machines: %v", err)
	}
	var list struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("machines undecodable: %v (%s)", err, raw)
	}
	out := map[string]map[string]any{}
	for _, m := range list.Machines {
		if id, _ := m["id"].(string); id != "" {
			out[id] = m
		}
	}
	return out
}

// w11MintKey creates one human user plus an API key.
func w11MintKey(t *testing.T, inst *v1Install, name string) (string, string) {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: name + "@w11.invalid", UserType: "human", Role: "writer",
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
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || key.Plain == "" {
		t.Fatalf("key answer for %s: %v (%s)", name, err, raw)
	}
	return user.ID, key.Plain
}

// w11PfAs runs the real pf CLI against the install as the given key and
// returns combined output plus exit code (never fatal on non-zero: the
// scenarios assert both the success and the refusal arm).
func w11PfAs(t *testing.T, inst *v1Install, apiKey string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(), "PF_SERVER="+inst.url, "PF_API_KEY="+apiKey, "PF_USER_ID=usr_root")
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("pf %v: %v", args, err)
	}
	return out.String(), ee.ExitCode()
}

// w11DB opens the running aihub's on-disk database read-only.
func w11DB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	return db
}

// w11Count counts rows in a table; a missing table counts as zero (the
// pre-code tree has no messaging tables).
func w11Count(t *testing.T, inst *v1Install, table string) int {
	t.Helper()
	db := w11DB(t, inst)
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		return 0
	}
	return n
}

// w11HasTableLike reports whether aihub persists a table whose name
// contains one of subs — the relay-only probe (a persisted progress or
// artifact snapshot/inbox would surface here).
func w11HasTableLike(t *testing.T, inst *v1Install, subs ...string) bool {
	t.Helper()
	db := w11DB(t, inst)
	defer db.Close()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(name)
		for _, s := range subs {
			if strings.Contains(lower, s) {
				return true
			}
		}
	}
	return false
}

// w11Sign is the plan-pinned machine signature:
// ed25519 hex over METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID.
func w11Sign(method, path string, body []byte, ts int64, machine string, key ed25519.PrivateKey) string {
	sum := sha256.Sum256(body)
	canon := method + "\n" + path + "\n" + hex.EncodeToString(sum[:]) + "\n" +
		strconv.FormatInt(ts, 10) + "\n" + machine
	return hex.EncodeToString(ed25519.Sign(key, []byte(canon)))
}

// ---------------------------------------------------------------------------
// W11-S1: identity binding, launcher/operator and supersession.

func TestW11MachineRegistrationBindsIdentityAndSupersedes(t *testing.T) {
	inst := newV1Install(t)
	pkA, privA := w11Key(t, 0x11)
	pkB, privB := w11Key(t, 0x22)

	reg := w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-a", "public_key": pkA, "launcher": "pi-web",
		"role": "interactive", "live_wi_ids": []string{},
	}, privA)
	if reg["machine_face_version"] == nil {
		t.Fatalf("first registration did not advertise the machine face: %v", reg)
	}
	if reg["role"] != "interactive" {
		t.Fatalf("first registration role = %v, want interactive: %v", reg["role"], reg)
	}

	refused := w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-b", "public_key": pkB, "launcher": "claude",
		"role": "interactive", "live_wi_ids": []string{},
	}, privB)
	if s, _ := refused["interactive_refused"].(string); !strings.Contains(s, "w11-a") {
		t.Fatalf("second interactive not refused naming the holder: %v", refused)
	}
	if refused["role"] != "runner" {
		t.Fatalf("refused daemon role = %v, want runner: %v", refused["role"], refused)
	}

	forced := w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-b", "public_key": pkB, "launcher": "claude",
		"role": "interactive", "force": true, "live_wi_ids": []string{},
	}, privB)
	if forced["role"] != "interactive" {
		t.Fatalf("forced registration role = %v, want interactive: %v", forced["role"], forced)
	}

	superseded := w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-a", "live_wi_ids": []string{},
	}, privA)
	if superseded["superseded"] != true || superseded["superseded_by"] != "w11-b" {
		t.Fatalf("superseded daemon not told: %v", superseded)
	}
	if superseded["role"] != "runner" {
		t.Fatalf("superseded daemon role = %v, want runner: %v", superseded["role"], superseded)
	}

	rows := w11MachineRows(t, inst.client)
	if rows["w11-a"]["role"] != "runner" || rows["w11-b"]["role"] != "interactive" {
		t.Fatalf("machines list role projection wrong: %v", rows)
	}
	if rows["w11-a"]["launcher"] != "pi-web" || rows["w11-b"]["launcher"] != "claude" {
		t.Fatalf("machines list launcher projection wrong: %v", rows)
	}
	if rows["w11-a"]["public_key"] != pkA {
		t.Fatalf("machines list lost the bound public key: %v", rows["w11-a"])
	}
	if rows["w11-a"]["operator_id"] == "" {
		t.Fatalf("machines list lost the operator binding: %v", rows["w11-a"])
	}
}

// ---------------------------------------------------------------------------
// W11-S2: admin-only rekey.

func TestW11MachineRekeyIsAdminOnly(t *testing.T) {
	inst := newV1Install(t)
	pkOld, privOld := w11Key(t, 0x33)
	pkNew, _ := w11Key(t, 0x44)
	w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-rk", "public_key": pkOld, "launcher": "pi-web",
		"role": "runner", "live_wi_ids": []string{},
	}, privOld)

	_, nonAdmin := w11MintKey(t, inst, "w11-nonadmin")
	nc := aihubclient.New(inst.url, nonAdmin)
	var out map[string]any
	err := nc.Do("POST", "/api/v1/machines/w11-rk/rekey", map[string]any{"public_key": pkNew}, &out)
	if got := w11ErrCode(err); got != api.ErrAuthForbidden {
		t.Fatalf("non-admin rekey code = %q, want %s (%v)", got, api.ErrAuthForbidden, err)
	}
	if got := w11MachineRows(t, inst.client)["w11-rk"]["public_key"]; got != pkOld {
		t.Fatalf("non-admin rekey changed the binding: %v", got)
	}

	if err := inst.client.Do("POST", "/api/v1/machines/w11-rk/rekey", map[string]any{"public_key": pkNew}, &out); err != nil {
		t.Fatalf("admin rekey: %v", err)
	}
	if got := w11MachineRows(t, inst.client)["w11-rk"]["public_key"]; got != pkNew {
		t.Fatalf("admin rekey did not rebind: got %v want %v", got, pkNew)
	}

	outText, code := w11PfAs(t, inst, inst.adminKey, "machines", "rekey", "w11-rk", "--public-key", pkOld)
	if code != 0 {
		t.Fatalf("pf machines rekey exit %d: %s", code, outText)
	}
	if got := w11MachineRows(t, inst.client)["w11-rk"]["public_key"]; got != pkOld {
		t.Fatalf("pf machines rekey did not rebind: got %v want %v", got, pkOld)
	}
}

// ---------------------------------------------------------------------------
// W11-S3: signed machine face — accept, replay, stale, unsigned, mismatch.

func TestW11SignedMachineFaceRejectsReplayAndStale(t *testing.T) {
	inst := newV1Install(t)
	pk, priv := w11Key(t, 0x55)
	w11Heartbeat(t, inst.client, map[string]any{
		"machine_id": "w11-sig", "public_key": pk, "launcher": "pi-web",
		"role": "runner", "live_wi_ids": []string{},
	}, priv)
	_, nonAdmin := w11MintKey(t, inst, "w11-sig-other")
	path := "/api/v1/queries/w11-no-such-query/answer"
	body := []byte(`{}`)

	send := func(ts int64, machine, bearer string, signed bool) (int, string) {
		req, err := http.NewRequest("POST", inst.url+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		if signed {
			req.Header.Set("X-PF-Machine-ID", machine)
			req.Header.Set("X-PF-Timestamp", strconv.FormatInt(ts, 10))
			req.Header.Set("X-PF-Signature", w11Sign("POST", path, body, ts, machine, priv))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	ts := time.Now().Unix()
	status, respBody := send(ts, "w11-sig", inst.adminKey, true)
	if status == http.StatusUnauthorized || !strings.Contains(respBody, `"code"`) {
		t.Fatalf("valid signed request did not reach the handler: %d %s", status, respBody)
	}
	if strings.Contains(respBody, api.ErrAuthUnauthorized) {
		t.Fatalf("valid signed request refused as AUTH-401: %d %s", status, respBody)
	}

	if st, b := send(ts, "w11-sig", inst.adminKey, true); st != http.StatusUnauthorized || !strings.Contains(b, api.ErrAuthUnauthorized) {
		t.Fatalf("replayed request not refused: %d %s", st, b)
	}
	if st, b := send(ts-1000, "w11-sig", inst.adminKey, true); st != http.StatusUnauthorized || !strings.Contains(b, api.ErrAuthUnauthorized) {
		t.Fatalf("stale timestamp not refused: %d %s", st, b)
	}
	if st, b := send(ts, "w11-sig", inst.adminKey, false); st != http.StatusUnauthorized || !strings.Contains(b, api.ErrAuthUnauthorized) {
		t.Fatalf("unsigned request not refused: %d %s", st, b)
	}
	if st, b := send(ts, "w11-sig", nonAdmin, true); !strings.Contains(b, api.ErrAuthForbidden) {
		t.Fatalf("operator mismatch not refused as AUTH-403: %d %s", st, b)
	}
}

// ---------------------------------------------------------------------------
// W11-S4: remote progress query through the real relay.

func TestW11RemoteProgressQueryRelaysSnapshot(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "w11 remote progress", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "park": true},
	}, nil)

	holder := newV1Daemon(t, inst, optAuto(), optMachineID("w11-holder"))
	holder.start()
	waitUntil(t, 120*time.Second, "WI held running/blocked by the holder", func() bool {
		st := inst.wiStatus(t, wi)
		return st == "running" || st == "blocked"
	})
	waitUntil(t, 60*time.Second, "holder capsule snapshot known", func() bool {
		return watchSoft(holder, wi) != nil
	})
	// The GIVEN is the parked fix step. Hub running/blocked plus a Known
	// snapshot are already true in the holder's claim window, so sampling
	// there compares two different moments of a progressing WI (claiming /
	// no step now vs running / grill a few ms later). Park is a silent live
	// wedge: wait for the fake agent's park record and for the event
	// sequence to stop moving, then both samples see the same stable step.
	waitStepStatus(t, holder, wi, "fix", "in_progress", 90*time.Second)
	waitUntil(t, 30*time.Second, "the fake agent never parked at fix", func() bool {
		for _, rec := range fakeJournal(t, holder.capsuleDir(wi)) {
			if rec["type"] == "park" && rec["step"] == "fix" {
				return true
			}
		}
		return false
	})
	quietSeq, quietSince := int64(-1), time.Time{}
	waitUntil(t, 60*time.Second, "holder snapshot never went quiet", func() bool {
		snap := watchSoft(holder, wi)
		if snap == nil {
			return false
		}
		if quietSeq != snap.LastSeq {
			quietSeq, quietSince = snap.LastSeq, time.Now()
			return false
		}
		return time.Since(quietSince) >= 1500*time.Millisecond
	})

	caller := newV1Daemon(t, inst, optMachineID("w11-caller"))
	caller.start()

	out := caller.pf(t, "watch", wi, "--once", "--json")
	var remote map[string]any
	if err := json.Unmarshal([]byte(out), &remote); err != nil {
		t.Fatalf("remote --json snapshot undecodable: %v (%s)", err, out)
	}
	if remote["holder_machine"] != "w11-holder" {
		t.Fatalf("holder_machine = %v, want w11-holder (%s)", remote["holder_machine"], out)
	}

	localOut := holder.pf(t, "watch", wi, "--once", "--json")
	var local map[string]any
	if err := json.Unmarshal([]byte(localOut), &local); err != nil {
		t.Fatalf("local --json snapshot undecodable: %v (%s)", err, localOut)
	}
	delete(remote, "holder_machine")
	// The witness is the shape plus the identity/state fields, not value
	// equality over time-relative presentation fields (oracle (i)): two
	// live samples taken at different instants cannot be value-equal.
	if err := w11ShapeMatches("snapshot", remote, local); err != nil {
		t.Fatalf("remote snapshot shape differs from local: %v\n remote=%v\n local=%v", err, remote, local)
	}
	for _, field := range []string{"work_item", "slug", "known", "hub_status", "last_seq"} {
		if remote[field] != local[field] {
			t.Fatalf("remote %s = %v, local %v", field, remote[field], local[field])
		}
	}
	if got, want := w11Nested(remote, "execution", "current_step"), w11Nested(local, "execution", "current_step"); got != want {
		t.Fatalf("remote step = %v, local %v", got, want)
	}
	if got, want := w11Nested(remote, "job", "status"), w11Nested(local, "job", "status"); got != want {
		t.Fatalf("remote job status = %v, local %v", got, want)
	}
	if job, ok := w11Nested(remote, "job").(map[string]any); ok {
		if v, present := job["last_progress_ago_sec"]; present {
			if _, ok := v.(float64); !ok {
				t.Fatalf("last_progress_ago_sec is not a number: %T", v)
			}
		}
	}

	if w11HasTableLike(t, inst, "progress", "snapshot", "pending", "inbox", "relay") {
		t.Fatalf("aihub persisted a relay table; query responses must stay in memory")
	}
}

// w11Nested walks a decoded JSON object by keys; nil when a step is not a
// map or the key is absent.
func w11Nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// w11ShapeMatches compares the JSON key-set shape of two decoded
// snapshots at every level. Scalar values are not compared (event
// seq/timestamps are time-relative), and last_progress_ago_sec is
// excluded because its presence depends on when the sample ran; the test
// still checks its type when present.
func w11ShapeMatches(path string, remote, local any) error {
	switch rv := remote.(type) {
	case map[string]any:
		lv, ok := local.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: remote is %T, local is %T", path, remote, local)
		}
		for k, v := range rv {
			if k == "last_progress_ago_sec" {
				continue
			}
			lvv, ok := lv[k]
			if !ok {
				return fmt.Errorf("%s: local lacks key %q", path, k)
			}
			if err := w11ShapeMatches(path+"."+k, v, lvv); err != nil {
				return err
			}
		}
		for k := range lv {
			if k == "last_progress_ago_sec" {
				continue
			}
			if _, ok := rv[k]; !ok {
				return fmt.Errorf("%s: remote lacks key %q", path, k)
			}
		}
		return nil
	case []any:
		lv, ok := local.([]any)
		if !ok {
			return fmt.Errorf("%s: remote is %T, local is %T", path, remote, local)
		}
		n := len(rv)
		if len(lv) < n {
			n = len(lv)
		}
		for i := 0; i < n; i++ {
			if err := w11ShapeMatches(fmt.Sprintf("%s[%d]", path, i), rv[i], lv[i]); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// W11-S5: 1:1 conversation, permanent bounded paginated history.

func TestW11MessagingConversationAndHistory(t *testing.T) {
	inst := newV1Install(t)
	uid1, key1 := w11MintKey(t, inst, "w11-u1")
	uid2, key2 := w11MintKey(t, inst, "w11-u2")
	u1 := aihubclient.New(inst.url, key1)
	u2 := aihubclient.New(inst.url, key2)

	var first map[string]any
	if err := u1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": "w11 first"}, &first); err != nil {
		t.Fatalf("first send: %v", err)
	}
	convID, _ := first["conversation_id"].(string)
	if convID == "" || first["id"] == nil {
		t.Fatalf("first send answer missing id/conversation: %v", first)
	}

	var second map[string]any
	if err := u1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": "w11 second"}, &second); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if second["conversation_id"] != convID {
		t.Fatalf("second message created a new conversation (group): %v vs %v", second["conversation_id"], convID)
	}

	// The conversation read face sees exactly one direct 1:1 with both members.
	var convs struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := u1.Do("GET", "/api/v1/conversations", nil, &convs); err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(convs.Conversations) != 1 {
		t.Fatalf("want exactly one conversation: %v", convs.Conversations)
	}
	one := convs.Conversations[0]
	if one["kind"] != "direct" || one["id"] != convID {
		t.Fatalf("conversation not a direct 1:1: %v", one)
	}
	blob, _ := json.Marshal(one)
	if !strings.Contains(string(blob), uid1) || !strings.Contains(string(blob), uid2) {
		t.Fatalf("conversation membership missing a user: %s", blob)
	}

	// History is complete and paginates on the monotonic seq cursor.
	page := w11MsgRows(t, u1, convID, "")
	if len(page) != 2 {
		t.Fatalf("history should hold both messages: %v", page)
	}
	if got := w11MsgRows(t, u2, convID, ""); len(got) != 2 {
		t.Fatalf("recipient member cannot read the same history: %v", got)
	}
	seq1 := w11SeqOf(page, "w11 first")
	if seq1 <= 0 {
		t.Fatalf("first message carries no seq cursor: %v", page)
	}
	after := w11MsgRows(t, u1, convID, "?after="+strconv.FormatInt(seq1, 10))
	if len(after) != 1 || after[0]["text"] != "w11 second" {
		t.Fatalf("incremental pull after seq %d wrong: %v", seq1, after)
	}
	limited := w11MsgRows(t, u1, convID, "?limit=1")
	if len(limited) != 1 {
		t.Fatalf("page limit=1 not honored: %v", limited)
	}

	// Bounded text: over 4 KiB is refused.
	var ignored map[string]any
	err := u1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": strings.Repeat("x", 4097)}, &ignored)
	if got := w11ErrCode(err); got != "CORE-413" {
		t.Fatalf("over-4KiB text code = %q, want CORE-413 (%v)", got, err)
	}
}

// ---------------------------------------------------------------------------
// W11-S6: guards (secret, visibility, addressing, bulk, flood) + CLI verbs.

func TestW11MessagingGuardsAndCLI(t *testing.T) {
	inst := newV1Install(t)
	_, key1 := w11MintKey(t, inst, "w11-g1")
	uid2, _ := w11MintKey(t, inst, "w11-g2")
	_, key3 := w11MintKey(t, inst, "w11-g3")
	g1 := aihubclient.New(inst.url, key1)
	g3 := aihubclient.New(inst.url, key3)

	convBefore, msgBefore := w11Count(t, inst, "conversations"), w11Count(t, inst, "messages")
	for _, secret := range []string{
		"key pf_k1_ABCDEFGHIJKLMNOP",
		strings.Repeat("a", 64),
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"sk-abcdefghijklmnopqrstuvwxyz",
	} {
		var ignored map[string]any
		err := g1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": secret}, &ignored)
		if got := w11ErrCode(err); got != "CORE-422" {
			t.Fatalf("secret-shaped text code = %q, want CORE-422 (%v)", got, err)
		}
	}
	if w11Count(t, inst, "conversations") != convBefore || w11Count(t, inst, "messages") != msgBefore {
		t.Fatalf("a refused secret created a conversation or message row")
	}

	var clean map[string]any
	if err := g1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": "w11 clean"}, &clean); err != nil {
		t.Fatalf("clean send: %v", err)
	}
	convID, _ := clean["conversation_id"].(string)

	// Visibility: a non-member is refused; the admin may read.
	var ignored map[string]any
	err := g3.Do("GET", "/api/v1/conversations/"+convID+"/messages", nil, &ignored)
	if got := w11ErrCode(err); got != api.ErrAuthForbidden {
		t.Fatalf("non-member read code = %q, want %s (%v)", got, api.ErrAuthForbidden, err)
	}
	if err := inst.client.Do("GET", "/api/v1/conversations/"+convID+"/messages", nil, &ignored); err != nil {
		t.Fatalf("admin read refused: %v", err)
	}

	// Addressing is users only; unknown fields and non-user addresses are refused.
	for _, body := range []map[string]any{
		{"chat_id": "chat_1", "text": "hi"},
		{"wi": "wi_1", "text": "hi"},
		{"broadcast": true, "text": "hi"},
		{"machine": "w11-a", "text": "hi"},
		{"to": "*", "text": "hi"},
	} {
		if err := g1.Do("POST", "/api/v1/messages", body, &ignored); w11ErrCode(err) == "" {
			t.Fatalf("mis-addressed send accepted: %v", body)
		}
	}
	// Bulk payloads are refused.
	if err := g1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": "hi", "file": "transcript"}, &ignored); w11ErrCode(err) == "" {
		t.Fatalf("bulk file payload accepted")
	}

	// Anti-flood: the excess is a typed refusal.
	flooded := false
	for i := 0; i < 80 && !flooded; i++ {
		err := g1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": fmt.Sprintf("w11 flood %d", i)}, &ignored)
		if w11ErrCode(err) == "CORE-429" {
			flooded = true
		}
	}
	if !flooded {
		t.Fatalf("anti-flood cap never refused the excess")
	}
	// The cap is a true rolling 60/60 s: a refusal does not reset the
	// sender's window, and the refused excess stores no message row.
	msgAfterFlood := w11Count(t, inst, "messages")
	if err := g1.Do("POST", "/api/v1/messages", map[string]any{"to": uid2, "text": "w11 after flood"}, &ignored); w11ErrCode(err) != "CORE-429" {
		t.Fatalf("a same-sender send right after the refusal was not CORE-429: %v", err)
	}
	if got := w11Count(t, inst, "messages"); got != msgAfterFlood {
		t.Fatalf("a refused excess stored a messages row: %d -> %d", msgAfterFlood, got)
	}

	// CLI: send/list/show round-trip through the real binary as a DIFFERENT
	// sender: the flood arm above deliberately exhausts key1's window.
	out, code := w11PfAs(t, inst, key3, "--json", "msg", "send", "--to", uid2, "--text", "w11 via cli")
	if code != 0 {
		t.Fatalf("pf msg send exit %d: %s", code, out)
	}
	if _, code = w11PfAs(t, inst, key3, "--json", "msg", "list"); code != 0 {
		t.Fatalf("pf msg list exit %d", code)
	}
	// analyze is a registered execution-plane verb (no daemon here, so it
	// may fail to connect — but it must not be an unknown command).
	analyzeOut, _ := w11PfAs(t, inst, key3, "msg", "analyze", "msg_none")
	if strings.Contains(analyzeOut, "unknown command") {
		t.Fatalf("pf msg analyze is not registered: %s", analyzeOut)
	}
}

// w11MsgRows lists a conversation's messages and returns the raw rows.
func w11MsgRows(t *testing.T, c *aihubclient.Client, convID, query string) []map[string]any {
	t.Helper()
	var page map[string]any
	if err := c.Do("GET", "/api/v1/conversations/"+convID+"/messages"+query, nil, &page); err != nil {
		t.Fatalf("list messages%s: %v", query, err)
	}
	raw, _ := page["messages"].([]any)
	if raw == nil {
		raw, _ = page["items"].([]any)
	}
	var out []map[string]any
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// w11SeqOf returns the seq of the message whose text matches.
func w11SeqOf(rows []map[string]any, text string) int64 {
	for _, m := range rows {
		if m["text"] == text {
			if f, ok := m["seq"].(float64); ok {
				return int64(f)
			}
		}
	}
	return 0
}
