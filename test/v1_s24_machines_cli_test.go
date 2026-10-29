package test

// v1_s24_machines_cli_test.go: the `pf machines list` e2e scenario — the
// --json face pinned as the RAW server-body passthrough (ListMachinesRaw
// → cmdMachines's ctx.finish(raw, …) house rule), the human render
// pinned on its salient cells only (the orDash "-" RUNTIME placeholder,
// the STALE/alive STATE cell, the stale-threshold header line — never a
// byte-golden of the table), the stale→fresh transition forced by a
// scenario-side backdate of last_heartbeat_at in the running aihub's
// on-disk aihub.db (grill Q2 owner ruling: the
// internal/aihub/core/machines_test.go UPDATE pattern, then ONE real
// wire heartbeat to flip it fresh — no sleeps, no daemon heartbeat
// loop, no -machines-stale-after override: the production-default
// 5-minute window applies), and the admin gate pinned by one non-admin
// refusal through the real CLI.
//
// Authored fresh for polyforge#68: the ticket #23 s24 deliverable never
// reached origin main — no test/v1_s24* file existed at HEAD 1000723 —
// so the s24 numbering continuity is kept by authoring under this name.
//
// Direct-to-aihub like s23: one newV1Install (the REAL aihub binary
// with only --data-dir/--listen), the REAL pf CLI, no daemon involved.

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// s24MachineKey answers a fresh Ed25519 identity and its base64 wire
// spelling: since a heartbeat must be signed by the machine's
// bound key, so the scenario installs an identity instead of relying on the
// unsigned transition.
func s24MachineKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("machine key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

// TestV1S24MachinesListCLI walks the machines liveness face end to end:
// register by one real wire heartbeat, pin the --json raw passthrough
// and the human render's salient cells, force stale by backdate and
// flip fresh with exactly one further heartbeat (both faces), and pin
// the admin gate with one non-admin refusal.
func TestV1S24MachinesListCLI(t *testing.T) {
	inst := newV1Install(t)

	// Registration baseline: ONE real signed wire heartbeat with NO
	// runtime_version (omitempty ⇒ the key is absent from the body ⇒
	// nothing stored), under the bootstrap-admin credential (the
	// authorizeMachineRegistration gate admits the global admin).
	pub, priv := s24MachineKey(t)
	inst.client.SetMachineIdentity("m-s24", priv)
	res, err := inst.client.Heartbeat(aihubclient.MachineHeartbeat{
		MachineID: "m-s24",
		PublicKey: pub,
		LiveWIIDs: []string{},
	})
	if err != nil {
		t.Fatalf("register heartbeat: %v", err)
	}
	if !res.Registered || res.MachineID != "m-s24" {
		t.Fatalf("first heartbeat did not register m-s24: %+v", res)
	}

	// The typed client sees exactly the one machine.
	var list aihubclient.MachinesList
	rawList, err := inst.client.ListMachinesRaw()
	if err != nil {
		t.Fatalf("list machines: %v", err)
	}
	if err := json.Unmarshal(rawList, &list); err != nil {
		t.Fatalf("list machines decode: %v", err)
	}
	if list.Count != 1 || len(list.Machines) != 1 || list.Machines[0].ID != "m-s24" {
		t.Fatalf("machines baseline wrong: count %d, machines %+v", list.Count, list.Machines)
	}

	// The real pf CLI lists it too (text face; the harness enforces
	// exit 0 — non-empty output carrying the machine row).
	out := inst.pf(t, "machines", "list")
	if strings.TrimSpace(out) == "" {
		t.Fatalf("machines list: empty output")
	}
	if !strings.Contains(out, "m-s24") {
		t.Fatalf("machines list lost the machine row:\n%s", out)
	}

	// --json is the RAW passthrough: the CLI stdout decodes to the same
	// machine row a direct wire read of GET /api/v1/machines returns —
	// only heartbeat_age_seconds may advance between the two reads, so
	// it is deliberately not compared.
	jout := inst.pf(t, "--json", "machines", "list")
	wire, err := inst.client.ListMachinesRaw()
	if err != nil {
		t.Fatalf("wire read of GET /api/v1/machines: %v", err)
	}
	var fromCLI, fromWire aihubclient.MachinesList
	if err := json.Unmarshal([]byte(jout), &fromCLI); err != nil {
		t.Fatalf("--json output undecodable: %v (%s)", err, jout)
	}
	if err := json.Unmarshal(wire, &fromWire); err != nil {
		t.Fatalf("wire body undecodable: %v (%s)", err, wire)
	}
	if fromCLI.Count != 1 || fromWire.Count != 1 {
		t.Fatalf("count wrong: cli %d, wire %d (%s)", fromCLI.Count, fromWire.Count, jout)
	}
	if fromCLI.StaleAfterSeconds != 300 || fromWire.StaleAfterSeconds != 300 {
		t.Fatalf("stale_after_seconds wrong: cli %d, wire %d (production-default 5m window)",
			fromCLI.StaleAfterSeconds, fromWire.StaleAfterSeconds)
	}
	if len(fromCLI.Machines) != 1 || len(fromWire.Machines) != 1 {
		t.Fatalf("machine rows wrong: cli %d, wire %d", len(fromCLI.Machines), len(fromWire.Machines))
	}
	mCLI, mWire := fromCLI.Machines[0], fromWire.Machines[0]
	if mCLI.ID != "m-s24" || mWire.ID != "m-s24" {
		t.Fatalf("machine id wrong: cli %q, wire %q", mCLI.ID, mWire.ID)
	}
	if mCLI.Mode != mWire.Mode || mCLI.LastHeartbeatAt != mWire.LastHeartbeatAt ||
		mCLI.Stale != mWire.Stale || !slices.Equal(mCLI.LiveWIIDs, mWire.LiveWIIDs) {
		t.Fatalf("--json passthrough drifted from the wire body: cli %+v, wire %+v", mCLI, mWire)
	}
	if mCLI.Stale {
		t.Fatalf("fresh machine reported stale: %+v", mCLI)
	}

	// An empty runtime_version is spelled key-ABSENCE on the wire
	// (omitempty), never "runtime_version": "" — pinned on the decoded
	// machine object AND on the raw bytes.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jout), &doc); err != nil {
		t.Fatalf("--json output is not a JSON object: %v (%s)", err, jout)
	}
	var rows []map[string]any
	if err := json.Unmarshal(doc["machines"], &rows); err != nil {
		t.Fatalf("machines rows undecodable: %v (%s)", err, jout)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly one machine object in the raw body: %s", jout)
	}
	if rv, present := rows[0]["runtime_version"]; present {
		t.Fatalf("runtime_version must be key-ABSENT, got %v (%s)", rv, jout)
	}
	if strings.Contains(jout, "runtime_version") {
		t.Fatalf("raw --json bytes carry a runtime_version key: %s", jout)
	}

	// The text face's salient cells — substring/exact-cell pins only,
	// never a byte-golden, no column-order or width assertions: the
	// header's stale-threshold line, the "-" RUNTIME placeholder
	// (orDash) and the alive STATE cell, all drawn by renderMachines.
	out = inst.pf(t, "machines", "list")
	if !strings.Contains(out, "daemons: 1 (stale threshold 300s)") {
		t.Fatalf("header line wrong:\n%s", out)
	}
	cells := s24RowCells(t, out, "m-s24")
	if !slices.Contains(cells, "-") {
		t.Fatalf("RUNTIME placeholder: no exact \"-\" cell in the row (cells %v):\n%s", cells, out)
	}
	if !slices.Contains(cells, "alive") {
		t.Fatalf("STATE cell: no exact \"alive\" cell in the row (cells %v):\n%s", cells, out)
	}

	// --- stale, forced deterministically (grill Q2 ruling): backdate
	// last_heartbeat_at ~1h in the running aihub's on-disk aihub.db —
	// no sleeps, no daemon loop; the production-default 5-minute
	// window makes that unambiguously stale, in BOTH faces.
	s24Backdate(t, inst, "m-s24", time.Hour)

	jout = inst.pf(t, "--json", "machines", "list")
	staleRow := s24Machine(t, jout, "m-s24")
	if !staleRow.Stale {
		t.Fatalf("backdated machine not stale in --json: %+v (%s)", staleRow, jout)
	}
	out = inst.pf(t, "machines", "list")
	cells = s24RowCells(t, out, "m-s24")
	if !slices.Contains(cells, "STALE") {
		t.Fatalf("backdated machine not STALE in the text row (cells %v):\n%s", cells, out)
	}
	if slices.Contains(cells, "alive") {
		t.Fatalf("stale text row still claims alive (cells %v):\n%s", cells, out)
	}
	staleStamp := s24StampCell(t, cells) // the backdated LAST HEARTBEAT

	// --- fresh again: exactly ONE further real wire heartbeat flips the
	// row (the refresh UPDATE: the server re-stamps last_heartbeat_at,
	// runtime_version COALESCE keeps the stored NULL) — no sleeps, no
	// window override. Observed in BOTH faces.
	if _, err := inst.client.Heartbeat(aihubclient.MachineHeartbeat{
		MachineID: "m-s24",
		LiveWIIDs: []string{},
	}); err != nil {
		t.Fatalf("refresh heartbeat: %v", err)
	}

	jout = inst.pf(t, "--json", "machines", "list")
	freshRow := s24Machine(t, jout, "m-s24")
	if freshRow.Stale {
		t.Fatalf("refreshed machine still stale in --json: %+v (%s)", freshRow, jout)
	}
	if freshRow.LastHeartbeatAt == staleRow.LastHeartbeatAt {
		t.Fatalf("refresh did not move last_heartbeat_at: %q", freshRow.LastHeartbeatAt)
	}
	out = inst.pf(t, "machines", "list")
	cells = s24RowCells(t, out, "m-s24")
	if !slices.Contains(cells, "alive") {
		t.Fatalf("refreshed machine not alive in the text row (cells %v):\n%s", cells, out)
	}
	if slices.Contains(cells, "STALE") {
		t.Fatalf("fresh text row still claims STALE (cells %v):\n%s", cells, out)
	}
	freshStamp := s24StampCell(t, cells)
	if freshStamp == staleStamp {
		t.Fatalf("LAST HEARTBEAT cell did not move after the refresh heartbeat: %q", freshStamp)
	}
	stamped, err := time.Parse(time.RFC3339, freshStamp)
	if err != nil {
		t.Fatalf("refreshed LAST HEARTBEAT cell %q unparseable: %v", freshStamp, err)
	}
	if age := time.Since(stamped); age < -time.Minute || age > 2*time.Minute {
		t.Fatalf("refreshed LAST HEARTBEAT not ~now (server-stamped): %s (age %s)", freshStamp, age)
	}

	// --- the admin gate: one non-admin refusal through the real CLI.
	// s24MintKey mints a human writer user
	// + API key — non-admin by construction. This s24-local exec runs the
	// real pf with the same env recipe as inst.pf but that key: the
	// server's authAdmin gate (role != "admin" ⇒ 403) answers before
	// any row is rendered, surfacing as a CLI error with a non-zero
	// exit, and the combined output carries no machine rows.
	_, nonAdminKey := s24MintKey(t, inst, "s24-nonadmin")
	refused := s24PfAs(t, inst, nonAdminKey, "machines", "list")
	for _, leak := range []string{"m-s24", "alive", "STALE"} {
		if strings.Contains(refused, leak) {
			t.Fatalf("non-admin refusal leaked %q into its output:\n%s", leak, refused)
		}
	}
	// No machines row (hence no exact "-" RUNTIME cell) may render —
	// pinned per-cell, because the 403 error line itself carries "-"
	// characters (AUTH-403) that a raw substring check would trip on.
	for _, line := range strings.Split(refused, "\n") {
		for _, c := range strings.Split(line, "  ") {
			if c = strings.TrimSpace(c); c == "-" {
				t.Fatalf("non-admin refusal rendered a machine row cell:\n%s", refused)
			}
		}
	}
}

// s24RowCells returns the non-empty cells of the machines row line that
// carries id: printTable joins the padded cells with two spaces, so the
// row line splits on "  " into cells — but a column whose pad+separator
// total is odd leaves one stray space glued to the next cell (e.g. the
// LAST HEARTBEAT timestamp when the HEARTBEAT AGE cell is "0s"), so
// tokens are trimmed before use; all-padding tokens collapse to empty
// and are dropped. The salient cells survive verbatim — including the
// exact "-" placeholder, which a raw substring check could never pin
// because the LAST HEARTBEAT timestamp also contains "-" characters.
func s24RowCells(t *testing.T, out, id string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, id) {
			continue
		}
		var cells []string
		for _, c := range strings.Split(line, "  ") {
			if c = strings.TrimSpace(c); c != "" {
				cells = append(cells, c)
			}
		}
		return cells
	}
	t.Fatalf("no %q row line in output:\n%s", id, out)
	return nil
}

// s24PfAs runs the REAL pf CLI against the install with the same env
// recipe as the harness pf helpers, but PF_API_KEY swapped to the given
// non-admin credential (those helpers hardcode PF_API_KEY=inst.adminKey,
// so the refusal arm needs this s24-local exec). Non-zero exit is
// REQUIRED — the server's authAdmin 403 surfaces as a CLI error — and
// the combined stdout+stderr is returned for the no-rows pin.
func s24PfAs(t *testing.T, inst *v1Install, apiKey string, args ...string) string {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url,
		"PF_API_KEY="+apiKey,
		"PF_USER_ID=usr_root",
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		return out.String() // the expected non-zero exit
	}
	t.Fatalf("non-admin pf %v unexpectedly succeeded (output %q)", args, out.String())
	return ""
}

// s24Machine decodes the --json machines body (the raw passthrough) and
// returns id's row.
func s24Machine(t *testing.T, jout, id string) aihubclient.MachineStatus {
	t.Helper()
	var list aihubclient.MachinesList
	if err := json.Unmarshal([]byte(jout), &list); err != nil {
		t.Fatalf("machines --json undecodable: %v (%s)", err, jout)
	}
	for _, m := range list.Machines {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no %q machine row in the --json body: %s", id, jout)
	return aihubclient.MachineStatus{}
}

// s24StampCell returns the RFC3339 timestamp cell of a machines row
// (the LAST HEARTBEAT value), located by parseability — not by column
// position.
func s24StampCell(t *testing.T, cells []string) string {
	t.Helper()
	for _, c := range cells {
		if _, err := time.Parse(time.RFC3339, c); err == nil {
			return c
		}
	}
	t.Fatalf("no RFC3339 timestamp cell in %v", cells)
	return ""
}

// s24Backdate forces the machine's staleness the grill-Q2 way: the
// internal/aihub/core/machines_test.go UPDATE pattern, adapted to a
// scenario-side write-open of the RUNNING aihub's on-disk aihub.db (the
// s15 on-disk-access precedent; the server runs WAL with
// busy_timeout(5000), so the scenario DSN carries the same pragma) —
// age ~1h is far beyond the production-default 5-minute stale window.
// No sleeps, no daemon heartbeat loop.
func s24Backdate(t *testing.T, inst *v1Install, machineID string, age time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite",
		"file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db for the backdate: %v", err)
	}
	defer db.Close()
	stamp := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id=?`, stamp, machineID); err != nil {
		t.Fatalf("backdate %s: %v", machineID, err)
	}
}

// s24MintKey creates one user plus an API key for the auth matrix and
// returns (user id, key plaintext).
func s24MintKey(t *testing.T, inst *v1Install, name string) (string, string) {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name,
		Email:       name + "@s24.invalid", // core requires email for human users
		UserType:    "human",
		Role:        "writer",
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
