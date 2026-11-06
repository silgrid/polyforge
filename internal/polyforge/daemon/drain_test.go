package daemon

// drain_test.go: the connection-change rules of drain.go — a server change
// and a different credential user are refused, an unverifiable key is not
// taken for the same user, a same-identity rotation drains (submits refused
// DAEMON-409) and switches by itself once the Cell is idle, and the
// persisted record restores effective + pending after a restart.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// keyUserHub answers the machine heartbeat with the Hub user each key
// belongs to; an unknown key is refused 401.
func keyUserHub(t *testing.T, users map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok || r.URL.Path != "/api/v1/machines/heartbeat" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"AUTH-401","message":"unknown key"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.MachineHeartbeatResult{MachineID: "m-test", OperatorID: user})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestConnectionChangeIdentityRulesAndDrain(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1", "k-other": "usr_2"})
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k-old" })
	<-td.ready // the machine key is loaded (the reload route is served only after it)
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := td.checkConnectionChange("http://127.0.0.1:9", "k-old"); err == nil || !strings.Contains(err.Error(), "create a new profile") {
		t.Fatalf("a server change must be refused naming the new-profile route; got %v", err)
	}
	if _, err := td.checkConnectionChange(hub.URL, "k-other"); err == nil || !strings.Contains(err.Error(), "another Hub user") {
		t.Fatalf("a different user's key must be refused; got %v", err)
	}
	if _, err := td.checkConnectionChange(hub.URL, "k-unknown"); err == nil || !strings.Contains(err.Error(), "could not be verified") {
		t.Fatalf("an unverifiable key must not pass as the same user; got %v", err)
	}
	if td.pendingChange() != "" {
		t.Fatalf("a refused change left a pending record")
	}
	if out, err := td.checkConnectionChange(hub.URL, "k-old"); err != nil || out != "" {
		t.Fatalf("an unchanged key is no change: %q %v", out, err)
	}

	// Hold the Cell busy (one queued submit) so the drain cannot finish.
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{}
	td.q.mu.Unlock()
	out, err := td.checkConnectionChange(hub.URL, "k-new")
	if err != nil || !strings.Contains(out, "pending") {
		t.Fatalf("a same-identity rotation must go pending: %q %v", out, err)
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-old" || rec.PendingAPIKey != "k-new" {
		t.Fatalf("the pending record was not persisted: %+v %v", rec, ok)
	}
	var ae *api.Error
	if e := td.drainRefusal(); e == nil {
		t.Fatal("a draining Cell must refuse new submits")
	} else if ae = e.(*api.Error); ae.Code != api.ErrDaemonConflict {
		t.Fatalf("drain refusal code = %s, want DAEMON-409", ae.Code)
	}
	td.maybeSwitchConnection() // the dispatch tick's own call, while busy
	if td.pendingChange() == "" {
		t.Fatal("the Cell switched while it still held work")
	}

	// Idle: the dispatch tick switches by itself, no second reload.
	td.q.mu.Lock()
	delete(td.q.inflight, "wi_busy")
	td.q.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for td.pendingChange() != "" {
		if time.Now().After(deadline) {
			t.Fatal("the idle Cell never switched to the rotated key")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-new" || rec.PendingAPIKey != "" {
		t.Fatalf("the switched record = %+v %v", rec, ok)
	}
	if td.drainRefusal() != nil {
		t.Fatal("submits still refused after the switch")
	}
}

func TestCellConfigRestoresEffectiveAndPendingKey(t *testing.T) {
	dir := t.TempDir()
	if err := writeEffective(dir, effectiveConnection{APIKey: "k-eff", PendingAPIKey: "k-pend"}); err != nil {
		t.Fatal(err)
	}
	rec, ok := readEffective(dir)
	if !ok || rec.APIKey != "k-eff" || rec.PendingAPIKey != "k-pend" {
		t.Fatalf("round trip = %+v %v", rec, ok)
	}
	if _, ok := readEffective(t.TempDir()); ok {
		t.Fatal("an absent record must read as none")
	}
}

// TestAssemblyHoldsTheRecordedIdentity: every assembly (Host start, add,
// repair all go through cellConfig) checks the file's normalized server
// against the Cell's identity.json — another server refuses the Cell with
// the new-profile route; the first start persists the effective credential
// and a later differing file key is carried as a pending change, never
// started on.
func TestAssemblyHoldsTheRecordedIdentity(t *testing.T) {
	root := t.TempDir()
	in := func(server, key string) cellBuildInput {
		return cellBuildInput{profile: "alpha", hostRoot: root, file: cli.Config{Server: server, APIKey: key, Mapping: &mapping.Plan{}}}
	}
	cfg, err := cellConfig(Config{}, in("http://hub-a:1", "k-old"))
	if err != nil || cfg.APIKey != "k-old" || cfg.FileAPIKey != "" {
		t.Fatalf("first start: key %q file %q %v", cfg.APIKey, cfg.FileAPIKey, err)
	}
	if rec, ok := readEffective(cfg.DataDir); !ok || rec.APIKey != "k-old" {
		t.Fatalf("the first start must persist the effective credential: %+v %v", rec, ok)
	}
	if err := writeIdentity(cfg.DataDir, cellIdentity{Server: "http://hub-a:1", User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	// a hand edit (or `pf config set server`) to another AIHub: refused at the next start
	if _, err := cellConfig(Config{}, in("http://hub-b:1", "k-old")); err == nil || !strings.Contains(err.Error(), "new profile") {
		t.Fatalf("another server must be refused at assembly naming the new-profile route: %v", err)
	}
	if _, err := cellConfig(Config{}, in("http://HUB-A:1/", "k-old")); err != nil {
		t.Fatalf("the same server spelled differently is the same identity: %v", err)
	}
	// another key in the file: the Cell starts on its effective credential
	again, err := cellConfig(Config{}, in("http://hub-a:1", "k-other"))
	if err != nil || again.APIKey != "k-old" || again.FileAPIKey != "k-other" {
		t.Fatalf("restart with another file key: key %q file %q %v", again.APIKey, again.FileAPIKey, err)
	}
	if rec, _ := readEffective(cfg.DataDir); rec.APIKey != "k-old" {
		t.Fatalf("a differing file key must not overwrite the effective record: %+v", rec)
	}
}

// TestAdoptFileKeyChecksTheUser: a file credential the Cell did not start
// on goes through the identity rules — another Hub user's key is not
// adopted (the Cell reports why and keeps its credential), the same user's
// key becomes the pending rotation.
func TestAdoptFileKeyChecksTheUser(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1", "k-other": "usr_2"})
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey, c.FileAPIKey = hub.URL, "k-old", "k-other" })
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{} // keep the Cell busy: a pending change stays pending
	td.q.mu.Unlock()
	td.adoptFileKey()
	if td.pendingChange() != "" {
		t.Fatal("another user's file key must not become pending")
	}
	if n := td.connectionNotice(); !strings.Contains(n, "another Hub user") {
		t.Fatalf("the Cell must report the not-adopted key: %q", n)
	}
	if row := td.profileInfo("alpha", ""); !strings.Contains(row.Reason, "another Hub user") {
		t.Fatalf("GET /profiles must show the notice: %+v", row)
	}
	td.cfg.FileAPIKey = "k-new"
	td.adoptFileKey()
	if td.pendingChange() == "" || td.connectionNotice() != "" {
		t.Fatalf("the same user's file key must become the pending rotation (notice %q)", td.connectionNotice())
	}
	td.q.mu.Lock()
	delete(td.q.inflight, "wi_busy")
	td.q.mu.Unlock()
}

// TestHeartbeatForAnotherUserIsolatesTheCell: a heartbeat answered for a
// Hub user other than the recorded one never overwrites the identity; the
// Cell reads degraded (claims stop) and the data-plane proxy refuses
// business on it.
func TestHeartbeatForAnotherUserIsolatesTheCell(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-other": "usr_2"})
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k-other"; c.Hub = aihubclient.New(hub.URL, "k-other") })
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	if err := td.sendHeartbeat(); err == nil || !strings.Contains(err.Error(), "never changes its user") {
		t.Fatalf("a heartbeat for another user must be refused: %v", err)
	}
	if id, _ := readIdentity(td.cfg.DataDir); id.User != "usr_1" {
		t.Fatalf("the recorded user was overwritten: %+v", id)
	}
	if td.degradedReason() == "" {
		t.Fatal("the Cell must read degraded")
	}
	td.noteHubOK() // a later success does not clear an identity mismatch
	if td.identityConflict() == "" || td.degradedReason() == "" {
		t.Fatal("the identity mismatch must be sticky")
	}
	req := httptest.NewRequest(http.MethodGet, "/aihub/api/v1/projects", nil)
	req.SetPathValue("path", "api/v1/projects")
	rec := httptest.NewRecorder()
	td.serveProxy(rec, req, "default")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "refuses AIHub business") {
		t.Fatalf("the proxy must refuse business on a mismatched Cell: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRemovedAPIKeyIsNeverShadowed: an api_key removed from the Cell's file
// is decided, never shadowed by the persisted effective copy. A reload
// reports the removal on GET /profiles (pending restart, not a misleading
// "could not be verified") and drops a pending rotation; the next assembly
// deletes the record and starts the Cell with no credential — it reads
// degraded naming the missing key, claims nothing and the proxy refuses
// business; a key of the same Hub user is then taken at once, another
// user's is refused.
func TestRemovedAPIKeyIsNeverShadowed(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1", "k-other": "usr_2"})

	// reload: the running Cell keeps its effective key and reports the removal
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k-old" })
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{} // busy: the pending rotation cannot switch by itself
	td.q.mu.Unlock()
	// Establish busy before introducing a pending rotation. Starting idle
	// with PendingAPIKey set lets the dispatch tick switch before setup.
	if _, err := td.checkConnectionChange(hub.URL, "k-new"); err != nil || td.pendingChange() == "" {
		t.Fatalf("queue the same-user rotation while busy: %v", err)
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-old" || rec.PendingAPIKey != "k-new" {
		t.Fatalf("the pre-removal record must contain the held rotation: %+v %v", rec, ok)
	}
	out, err := td.checkConnectionChange(hub.URL, "")
	if err != nil || out != keyRemovedOutcome {
		t.Fatalf("a removed key must be decided as a pending restart: %q %v", out, err)
	}
	if td.pendingChange() != "" {
		t.Fatal("a rotation toward a key the file no longer names must be dropped")
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-old" || rec.PendingAPIKey != "" {
		t.Fatalf("the record after the removal = %+v %v", rec, ok)
	}
	if row := td.profileInfo("default", ""); !strings.Contains(row.Reason, "the file has no api_key") {
		t.Fatalf("GET /profiles must report the removed key: %+v", row)
	}
	td.q.mu.Lock()
	delete(td.q.inflight, "wi_busy")
	td.q.mu.Unlock()

	// restart: the record is deleted, the Cell holds no credential
	root := t.TempDir()
	in := func(key string) cellBuildInput {
		return cellBuildInput{profile: "default", hostRoot: root, configPath: "/etc/cli.toml", file: cli.Config{Server: hub.URL, APIKey: key, Mapping: &mapping.Plan{}}}
	}
	first, err := cellConfig(Config{}, in("k-old"))
	if err != nil || first.APIKey != "k-old" {
		t.Fatalf("first start: %q %v", first.APIKey, err)
	}
	if err := writeIdentity(first.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	unset, err := cellConfig(Config{}, in(""))
	if err != nil || unset.APIKey != "" || unset.FileAPIKey != "" {
		t.Fatalf("a restart with no file key must not start on the persisted copy: key %q file %q %v", unset.APIKey, unset.FileAPIKey, err)
	}
	if _, ok := readEffective(first.DataDir); ok {
		t.Fatal("the effective record must be deleted once the file has no api_key")
	}

	bare := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey, c.ConfigPath = hub.URL, "", "/etc/cli.toml" })
	<-bare.ready
	if err := writeIdentity(bare.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	row := bare.profileInfo("default", "")
	if row.State != "degraded" || !strings.Contains(row.Reason, "api_key is not set in /etc/cli.toml") {
		t.Fatalf("a Cell with no credential must read degraded naming the missing key: %+v", row)
	}
	if !strings.Contains(bare.degradedReason(), "api_key is not set") {
		t.Fatal("a Cell with no credential must stop its automatic claims")
	}
	req := httptest.NewRequest(http.MethodGet, "/aihub/api/v1/projects", nil)
	req.SetPathValue("path", "api/v1/projects")
	rec := httptest.NewRecorder()
	bare.serveProxy(rec, req, "default")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "api_key is not set") {
		t.Fatalf("the proxy must refuse business naming the missing key: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := bare.checkConnectionChange(hub.URL, "k-other"); err == nil || !strings.Contains(err.Error(), "another Hub user") {
		t.Fatalf("another user's key must still be refused: %v", err)
	}
	out, err = bare.checkConnectionChange(hub.URL, "k-new")
	if err != nil || out != keyAppliedOutcome {
		t.Fatalf("a same-user key must be taken at once by a Cell holding none: %q %v", out, err)
	}
	if bare.missingCredential() != "" || bare.pendingChange() != "" {
		t.Fatalf("the Cell must hold the key now (missing %q, pending %q)", bare.missingCredential(), bare.pendingChange())
	}
	if rec, ok := readEffective(bare.cfg.DataDir); !ok || rec.APIKey != "k-new" {
		t.Fatalf("the taken key must be persisted: %+v %v", rec, ok)
	}
}

// TestPlainUnsetAndRevertedRotationAreDecided: the plain unset (no rotation
// pending — the empty file key then equals the empty pending one) is
// reported, never a silent no-op; a file reverted to the effective key
// drops a pending rotation; a repeated reload of the unset stays reported.
func TestPlainUnsetAndRevertedRotationAreDecided(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1"})
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k-old" })
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := td.checkConnectionChange(hub.URL, "")
		if err != nil || out != keyRemovedOutcome {
			t.Fatalf("reload %d of a plain unset must be decided as a pending restart: %q %v", i, out, err)
		}
		if row := td.profileInfo("default", ""); !strings.Contains(row.Reason, "the file has no api_key") {
			t.Fatalf("reload %d: GET /profiles must report the removed key: %+v", i, row)
		}
	}
	// the key back in the file clears the notice
	if out, err := td.checkConnectionChange(hub.URL, "k-old"); err != nil || out != "" || td.connectionNotice() != "" {
		t.Fatalf("the restored key: %q %v notice %q", out, err, td.connectionNotice())
	}
	// a rotation, then the file reverted to the effective key: the rotation is dropped
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{}
	td.q.mu.Unlock()
	if out, err := td.checkConnectionChange(hub.URL, "k-new"); err != nil || !strings.Contains(out, "pending") {
		t.Fatalf("rotation: %q %v", out, err)
	}
	if out, err := td.checkConnectionChange(hub.URL, "k-old"); err != nil || out != "" {
		t.Fatalf("revert: %q %v", out, err)
	}
	if td.pendingChange() != "" {
		t.Fatal("a rotation toward a key the file no longer names must be dropped")
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-old" || rec.PendingAPIKey != "" {
		t.Fatalf("the record after the revert = %+v %v", rec, ok)
	}
	td.q.mu.Lock()
	delete(td.q.inflight, "wi_busy")
	td.q.mu.Unlock()
}

// TestDrainSwitchWaitsForAnInFlightReload: the dispatch tick's switch never
// interleaves with a reload deciding the connection (review s20 N1) — while
// a reload holds the serializer an idle Cell's tick does not switch, so a
// revert to the effective key cannot be overtaken by the rotation it drops.
func TestDrainSwitchWaitsForAnInFlightReload(t *testing.T) {
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1"})
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k-old" })
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	td.reloadApplyMu.Lock() // a reload is in flight (before the rotation is recorded the tick cannot switch it)
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{}
	td.q.mu.Unlock()
	if out, err := td.checkConnectionChange(hub.URL, "k-new"); err != nil || !strings.Contains(out, "pending") {
		td.reloadApplyMu.Unlock()
		t.Fatalf("rotation: %q %v", out, err)
	}
	td.q.mu.Lock()
	delete(td.q.inflight, "wi_busy") // the Cell turns idle inside the reload's window
	td.q.mu.Unlock()
	td.maybeSwitchConnection()
	if td.pendingChange() == "" {
		td.reloadApplyMu.Unlock()
		t.Fatal("the tick switched while a reload held the serializer")
	}
	// the in-flight reload reverts the file to the effective key
	_, err := td.checkConnectionChange(hub.URL, "k-old")
	td.reloadApplyMu.Unlock()
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	td.maybeSwitchConnection()
	if td.pendingChange() != "" {
		t.Fatal("the reverted rotation is still pending")
	}
	if rec, ok := readEffective(td.cfg.DataDir); !ok || rec.APIKey != "k-old" || rec.PendingAPIKey != "" {
		t.Fatalf("the Cell must stay on the key its file names: %+v %v", rec, ok)
	}
}
