package daemon

// drain.go: a connection change drains the Cell before it switches.
//
// Identity is stable (spec "An existing profile MUST NOT change its AIHub or
// user identity in place"): a reload whose normalized server differs from
// the Cell's effective one is refused with the new-profile route; an api_key
// change is verified first — the new key must answer for the same Hub user
// (one heartbeat with it); a different user is refused and an unverifiable
// one is not treated as the same user (nothing changes, the reason is
// reported). A verified same-identity rotation becomes the Cell's pending
// change: from then on new submits and automatic claims are refused
// DAEMON-409 while queries, answers, resume and cancel of existing work go
// on; nothing is killed. When the Cell is safe-idle — no job record of any
// state (paused, needs_input and retained sessions hold it) and no queued
// submit — the Cell switches by itself on its dispatch tick (no second
// reload): the hub client takes the new key in place.
//
// The last effective credential and the pending one are persisted in the
// Cell's effective-connection.json (0600, credential-grade, never echoed):
// a Host restart starts the Cell on its effective key, restores its work,
// and keeps draining toward the pending one — the newer file value cannot
// bypass the idle gate.
//
// An api_key removed from the file is decided, never shadowed by the
// persisted copy: a reload reports it on GET /profiles and keeps the
// running Cell on its effective key until the Host restarts; the next
// assembly deletes the record and the Cell holds no credential — it reads
// degraded and refuses AIHub business naming the missing key, until a key
// of the same Hub user is set (taken at once: nothing on the Hub to drain).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/version"
)

// effectiveFile persists a Cell's effective and pending credential.
const effectiveFile = "effective-connection.json"

// effectiveConnection is the persisted connection record.
type effectiveConnection struct {
	// APIKey is the credential the Cell's runtime uses.
	APIKey string `json:"api_key"`
	// PendingAPIKey is a verified same-identity rotation waiting for the
	// Cell to go idle ("" = none).
	PendingAPIKey string `json:"pending_api_key,omitempty"`
}

// readEffective answers the persisted record of dir (ok=false: none).
func readEffective(dir string) (effectiveConnection, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, effectiveFile))
	if err != nil {
		return effectiveConnection{}, false
	}
	var e effectiveConnection
	if json.Unmarshal(raw, &e) != nil || e.APIKey == "" {
		return effectiveConnection{}, false
	}
	return e, true
}

// writeEffective persists the record (0600: it holds credentials).
func writeEffective(dir string, e effectiveConnection) error {
	raw, _ := json.Marshal(e)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, effectiveFile), append(raw, '\n'), 0o600)
}

// pendingChange answers the Cell's pending connection change ("" = none).
func (d *Daemon) pendingChange() string {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	if d.pendingKey == "" {
		return ""
	}
	return "api_key rotation (same identity) waits for the Cell to go idle"
}

// drainRefusal answers the one refusal of new work while a change drains:
// a submit, an automatic claim and a task spawn. Nothing refused is
// queued. Callers that admit on a nil answer hold drainGate.
func (d *Daemon) drainRefusal() error {
	if p := d.pendingChange(); p != "" {
		return &api.Error{Code: api.ErrDaemonConflict, Detail: "this Cell is draining for a pending connection rotation (" + p + "): new submits, automatic claims and task spawns are refused and nothing is queued — retry once the Cell switched; queries, answers, resume and cancel of existing work go on"}
	}
	return nil
}

// checkConnectionChange applies the identity rules to a reload's new file
// config: the outcome to report, or a refusal. A verified rotation is
// recorded pending (persisted) and switched by the dispatch tick.
func (d *Daemon) checkConnectionChange(newServer, newKey string) (string, error) {
	cur, cerr := cellcfg.NormalizeServer(d.cfg.Server)
	if cerr != nil {
		return "", nil // a Daemon assembled without a server (the unhosted test seam) has no identity to keep
	}
	next, err := cellcfg.NormalizeServer(newServer)
	if err != nil {
		return "", &api.Error{Code: api.ErrDaemonBadInput, Detail: "config reload: " + err.Error()}
	}
	if next != cur {
		return "", &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(
			"config reload: the server changed from %s to %s — a Cell never changes its AIHub in place; create a new profile for the other AIHub (this Cell keeps serving %s; nothing was applied)", cur, next, cur)}
	}
	d.reloadMu.Lock()
	effective, pending := d.effectiveKey, d.pendingKey
	d.reloadMu.Unlock()
	switch {
	case newKey == "" && effective != "":
		// decided before the equality checks: with no rotation pending the
		// empty file key equals the empty pending one
		return d.recordKeyRemoval(effective)
	case newKey == effective && pending != "":
		// the file is back on the effective key: the rotation toward a key
		// the file no longer names is dropped
		if err := writeEffective(d.cfg.DataDir, effectiveConnection{APIKey: effective}); err != nil {
			return "", &api.Error{Code: api.ErrDaemonInternal, Detail: "config reload: persist the dropped rotation: " + err.Error()}
		}
		d.reloadMu.Lock()
		d.pendingKey, d.connNotice = "", ""
		d.reloadMu.Unlock()
		return "", nil
	case newKey == effective || newKey == pending:
		d.setConnNotice("") // the file agrees with the Cell again: an earlier notice is stale
		return "", nil
	}
	user, verr := d.verifyKeyUser(newServer, newKey)
	_, recorded := d.heartbeatSnapshot()
	if id, ok := readIdentity(d.cfg.DataDir); ok && id.User != "" {
		recorded = id.User
	}
	switch {
	case verr != nil:
		d.setConnNotice("the file's api_key is not adopted: its identity could not be verified (" + verr.Error() + ")")
		return "", &api.Error{Code: api.ErrDaemonConflict, Detail: "config reload: the new api_key's identity could not be verified (" + verr.Error() + ") — an unverified key is never taken for the same user; nothing was applied, retry once the Hub answers"}
	case recorded != "" && user != recorded:
		d.setConnNotice(fmt.Sprintf("the file's api_key is not adopted: it belongs to another Hub user (%s, the Cell's is %s) — create a new profile for that user", user, recorded))
		return "", &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("config reload: the new api_key belongs to another Hub user (%s, the Cell's is %s) — a Cell never changes its user in place; create a new profile for that user (nothing was applied)", user, recorded)}
	}
	if effective == "" {
		// a Cell holding no credential has nothing in flight on the Hub to
		// drain: the verified key is taken at once
		if err := writeEffective(d.cfg.DataDir, effectiveConnection{APIKey: newKey}); err != nil {
			return "", &api.Error{Code: api.ErrDaemonInternal, Detail: "config reload: persist the connection: " + err.Error()}
		}
		if c, ok := d.hub.(*aihubclient.Client); ok {
			c.SetAPIKey(newKey)
		}
		d.reloadMu.Lock()
		d.effectiveKey, d.pendingKey, d.connNotice = newKey, "", ""
		d.reloadMu.Unlock()
		return keyAppliedOutcome, nil
	}
	d.reloadMu.Lock()
	d.pendingKey, d.connNotice = newKey, ""
	rec := effectiveConnection{APIKey: d.effectiveKey, PendingAPIKey: newKey}
	d.reloadMu.Unlock()
	if err := writeEffective(d.cfg.DataDir, rec); err != nil {
		return "", &api.Error{Code: api.ErrDaemonInternal, Detail: "config reload: persist the pending change: " + err.Error()}
	}
	return "pending: same-identity api_key rotation drains — new submits wait, the Cell switches by itself once idle", nil
}

// keyAppliedOutcome is the reload outcome of a verified key taken by a
// Cell that held no credential.
const keyAppliedOutcome = "applied: the Cell held no api_key; the verified key (same Hub user) is its credential now"

// keyRemovedOutcome is the reload outcome of an api_key removed from the
// Cell's file.
const keyRemovedOutcome = "pending restart: the file has no api_key — the Cell keeps its effective credential until the Host restarts; from then on it holds none and refuses AIHub business naming the missing key (set a key of the same Hub user to keep it serving)"

// recordKeyRemoval decides an api_key removed from the Cell's file: it is
// never shadowed by the persisted copy. The running Cell keeps its
// effective credential (removing it under in-flight work would break that
// work) and reports the removal on GET /profiles; a pending rotation —
// a key the file no longer names — is dropped; the next Host start
// assembles the Cell with no credential (cellConfig deletes the record).
func (d *Daemon) recordKeyRemoval(effective string) (string, error) {
	if err := writeEffective(d.cfg.DataDir, effectiveConnection{APIKey: effective}); err != nil {
		return "", &api.Error{Code: api.ErrDaemonInternal, Detail: "config reload: persist the dropped rotation: " + err.Error()}
	}
	d.reloadMu.Lock()
	d.pendingKey = ""
	d.connNotice = "the file has no api_key: the Cell runs on its effective credential until the Host restarts, then holds none and refuses AIHub business — set a key with `pf config set api_key` (stdin)"
	d.reloadMu.Unlock()
	return keyRemovedOutcome, nil
}

// missingCredential answers why a Cell holding no api_key refuses AIHub
// business ("" = it holds one). A Daemon assembled without a server (the
// unhosted test seam) has no connection to hold a credential for.
func (d *Daemon) missingCredential() string {
	if _, err := cellcfg.NormalizeServer(d.cfg.Server); err != nil {
		return ""
	}
	d.reloadMu.Lock()
	key := d.effectiveKey
	d.reloadMu.Unlock()
	if key != "" {
		return ""
	}
	return fmt.Sprintf("api_key is not set in %s — this Cell holds no AIHub credential; set one with `pf config set api_key` (stdin) and run `pf config reload`", d.cfg.ConfigPath)
}

// setConnNotice records why the file's credential was not adopted.
func (d *Daemon) setConnNotice(why string) {
	d.reloadMu.Lock()
	d.connNotice = why
	d.reloadMu.Unlock()
}

// connectionNotice answers the not-adopted file credential notice ("" =
// none).
func (d *Daemon) connectionNotice() string {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	return d.connNotice
}

// adoptFileKey runs the identity check on a file credential that differs
// from the effective one the Cell started on (Host start, add, repair):
// verified for the same Hub user it becomes the pending rotation and
// drains; otherwise it is not used and the Cell reports why. Called once
// after the Cell started, outside every lock but the reload serializer
// (the check is one bounded heartbeat).
func (d *Daemon) adoptFileKey() {
	key := d.cfg.FileAPIKey
	if key == "" {
		return // the file agrees with the effective key (an empty file key never reaches here: cellConfig)
	}
	d.reloadApplyMu.Lock()
	defer d.reloadApplyMu.Unlock()
	outcome, err := d.checkConnectionChange(d.cfg.Server, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: Cell %s keeps its effective api_key: %v\n", d.profile(), err)
		return
	}
	if outcome != "" {
		fmt.Fprintf(os.Stderr, "daemon: Cell %s: %s\n", d.profile(), outcome)
	}
}

// verifyKeyUser answers the Hub user a credential answers for: one
// heartbeat of this Cell's machine identity with that key (the same call
// the Cell makes every cadence).
func (d *Daemon) verifyKeyUser(server, key string) (string, error) {
	c := aihubclient.New(server, key)
	if d.machineKey != nil {
		c.SetMachineIdentity(d.cfg.MachineID, d.machineKey)
	}
	res, err := c.Heartbeat(aihubclient.MachineHeartbeat{
		MachineID: d.cfg.MachineID, RuntimeVersion: version.Version,
		Mode: d.modes.HeartbeatMode(), LiveWIIDs: liveWIIDs(d.rt.List()), PublicKey: d.publicKeyB64, Launcher: d.cfg.Launcher,
	})
	if err != nil {
		return "", err
	}
	return res.OperatorID, nil
}

// safeIdle reports whether the Cell holds nothing: no job record of any
// state (paused, needs_input and retained sessions included), no queued
// or in-flight submit (one queue snapshot), no admission still persisting
// its queue entry (an admitting reservation) and no admitted one-off task.
// The caller holds drainGate, so no admission passes its pending check
// meanwhile. The reservation is read before the queue: an entry that lands
// after the snapshot was reserved before it.
func (d *Daemon) safeIdle() bool {
	return d.admitting.Load() == 0 && len(d.rt.List()) == 0 && d.q.Idle() && d.tasksHeld.Load() == 0
}

// maybeSwitchConnection switches a pending rotation once the Cell is idle
// (the dispatch tick calls it). It runs under the reload serializer, so a
// reload deciding effective/pending (a revert drops the rotation) and the
// switch never interleave; while a reload holds it the tick skips and the
// next tick retries. The idle decision is taken under drainGate: every
// admission that passed its pending check has landed or holds its
// admitting reservation (and is seen), and
// every later one finds the rotation still pending and is refused until
// the switch below clears it — so no new work can slip in after the
// decision. The record is persisted (outside the gate) before the hub
// client takes the new key in place (a crash in between restarts on the
// new key, which an idle Cell may take).
func (d *Daemon) maybeSwitchConnection() {
	if !d.reloadApplyMu.TryLock() {
		return // a reload is deciding the connection: the next tick retries
	}
	defer d.reloadApplyMu.Unlock()
	d.reloadMu.Lock()
	pending := d.pendingKey
	d.reloadMu.Unlock()
	if pending == "" {
		return
	}
	d.drainGate.Lock()
	idle := d.safeIdle()
	d.drainGate.Unlock()
	if !idle {
		return
	}
	if err := writeEffective(d.cfg.DataDir, effectiveConnection{APIKey: pending}); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: persist the switched connection (the next tick retries): %v\n", err)
		return
	}
	if c, ok := d.hub.(*aihubclient.Client); ok {
		c.SetAPIKey(pending)
	}
	d.reloadMu.Lock()
	d.effectiveKey, d.pendingKey = pending, ""
	d.reloadMu.Unlock()
	fmt.Fprintf(os.Stderr, "daemon: Cell %s switched to its rotated api_key (it was idle)\n", d.profile())
}
