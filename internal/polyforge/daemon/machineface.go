package daemon

// machineface.go: the daemon's cross-machine face state.
// The heartbeat negotiates the role; the query long-poll runs for every
// registered daemon, the message long-poll only for an interactive
// daemon whose operator has a second live machine. A heartbeat response
// without machine_face_version disables the whole face (old hub, no
// compatibility branch).

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// MachineFace is the signed cross-machine client half the daemon uses
// (the production *aihubclient.Client; Config.MachineFace swaps it in
// tests).
type MachineFace interface {
	Inbox(faces string, wait time.Duration) (*api.MachineInbox, error)
	AnswerQuery(qid string, body api.QueryAnswerBody) error
	ProgressQuery(wi, artifactPath string) (*api.QueryAnswerBody, error)
	DeliverMessage(conversationID string, seq int64) error
	SetMachineIdentity(machineID string, key ed25519.PrivateKey)
}

// roleFromLauncher maps the invoking environment to the desired role
// (claude/pi-web are interactive sessions; anything else is a runner).
func roleFromLauncher(launcher string) string {
	switch launcher {
	case api.LauncherClaude, api.LauncherPiWeb:
		return api.MachineRoleInteractive
	default:
		return api.MachineRoleRunner
	}
}

// machineFaceState is guarded by Daemon.faceMu.
type machineFaceState struct {
	available   bool
	version     int
	role        string
	desiredRole string
	// roleChosen: an operator chose the role explicitly (POST
	// /machine/role without if_unset); persisted in <data-dir>/
	// machine-role.json so it survives a restart. The session-start
	// adapters' if_unset request then leaves it alone.
	roleChosen     bool
	forceNext      bool
	multiMachine   bool
	superseded     bool
	supersededBy   string
	refused        string
	unavailableLog bool
	queryStarted   bool
	messageStarted bool
}

func (d *Daemon) faceClient() MachineFace {
	if d.cfg.MachineFace != nil {
		return d.cfg.MachineFace
	}
	if c, ok := d.hub.(*aihubclient.Client); ok {
		return c
	}
	return nil
}

func (d *Daemon) faceAvailable() bool {
	d.mfaceMu.Lock()
	defer d.mfaceMu.Unlock()
	return d.face.available
}

func (d *Daemon) faceRole() string {
	d.mfaceMu.Lock()
	defer d.mfaceMu.Unlock()
	return d.face.role
}

// faceHeartbeat projects the identity and desired role onto the heartbeat.
func (d *Daemon) faceHeartbeat() (string, *api.CapabilityCard, bool) {
	d.mfaceMu.Lock()
	defer d.mfaceMu.Unlock()
	role := d.face.desiredRole
	if role == "" {
		role = api.MachineRoleRunner
	}
	cap := api.FullCapability()
	force := d.face.forceNext
	d.face.forceNext = false
	return role, &cap, force
}

// applyHeartbeat consumes the server's coordination answer and starts the
// faces that became available. The lock guards only the state mutation:
// log lines are computed under it and written after Unlock (the daemon's
// stderr is its log file, and no disk I/O runs under a lock).
func (d *Daemon) applyHeartbeat(res *aihubclient.MachineHeartbeatResult) {
	if res == nil {
		return
	}
	var unavailable, supersededLine string
	d.mfaceMu.Lock()
	startQueries, startMessages := false, false
	if res.MachineFaceVersion == 0 {
		d.face.available = false
		if !d.face.unavailableLog {
			d.face.unavailableLog = true
			unavailable = "daemon: cross-machine face unavailable (the aihub heartbeat carries no machine_face_version); local execution continues"
		}
		d.mfaceMu.Unlock()
		if unavailable != "" {
			fmt.Fprintln(os.Stderr, unavailable)
		}
		return
	}
	d.face.available = true
	d.face.version = res.MachineFaceVersion
	if res.Role != "" {
		d.face.role = res.Role
	}
	wasSuperseded := d.face.superseded
	d.face.multiMachine = res.MultiMachine
	d.face.superseded = res.Superseded
	d.face.supersededBy = res.SupersededBy
	d.face.refused = res.InteractiveRefused
	if res.Superseded && d.face.role != api.MachineRoleRunner {
		d.face.role = api.MachineRoleRunner
	}
	if res.Superseded && !wasSuperseded {
		supersededLine = fmt.Sprintf("daemon: interactive role superseded by machine %s; query face and local execution continue, message face stops", res.SupersededBy)
	}
	if !d.face.queryStarted {
		d.face.queryStarted = true
		startQueries = true
	}
	if d.face.role == api.MachineRoleInteractive && d.face.multiMachine && !d.face.messageStarted {
		d.face.messageStarted = true
		startMessages = true
	}
	d.mfaceMu.Unlock()
	if supersededLine != "" {
		fmt.Fprintln(os.Stderr, supersededLine)
	}
	if startQueries {
		go d.queryLoop(d.runCtx)
	}
	if startMessages {
		go d.messageLoop(d.runCtx)
	}
}

// faceStatus projects the face state for GET /status.
func (d *Daemon) faceStatus() map[string]any {
	d.mfaceMu.Lock()
	defer d.mfaceMu.Unlock()
	return map[string]any{
		"available":           d.face.available,
		"role":                d.face.role,
		"multi_machine":       d.face.multiMachine,
		"superseded":          d.face.superseded,
		"superseded_by":       d.face.supersededBy,
		"interactive_refused": d.face.refused,
	}
}

// requestDesiredRole records an operator move (`pf ops role`, POST
// /machine/role) and answers whether it was applied. ifUnset is the
// session-start adapters' request (`--if-unset`): applied only while no
// operator chose a role explicitly (persisted, so across restarts; an explicit
// `pf ops role runner` is never undone by a session start, compact or
// clear); an explicit move always applies and marks the role chosen.
func (d *Daemon) requestDesiredRole(role string, force, ifUnset bool) (bool, error) {
	if role != api.MachineRoleInteractive && role != api.MachineRoleRunner {
		return false, fmt.Errorf("role must be interactive or runner")
	}
	// one role move at a time: memory and the persisted file follow the
	// same order (the file write happens under roleMu, not the face lock)
	d.roleMu.Lock()
	defer d.roleMu.Unlock()
	d.mfaceMu.Lock()
	if ifUnset && d.face.roleChosen {
		d.mfaceMu.Unlock()
		return false, nil
	}
	d.face.desiredRole = role
	if !ifUnset {
		d.face.roleChosen = true
	}
	if force {
		d.face.forceNext = true
	}
	d.mfaceMu.Unlock()
	if ifUnset {
		return true, nil
	}
	// the explicit choice outlives the process (no I/O under the face lock)
	if err := writeChosenRole(d.cfg.DataDir, role); err != nil {
		return true, fmt.Errorf("the role is requested but could not be recorded (it lasts until the daemon restarts): %w", err)
	}
	return true, nil
}

// chosenRoleFile holds the operator's explicit role choice (pf ops role
// interactive|runner without --if-unset), so a restart keeps it.
const chosenRoleFile = "machine-role.json"

func writeChosenRole(dataDir, role string) error {
	blob, err := json.Marshal(map[string]string{"role": role})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dataDir, chosenRoleFile), blob, 0o600)
}

// readChosenRole answers the persisted explicit choice ("" = none, or an
// unreadable file: the launcher default then applies, as before any choice).
func readChosenRole(dataDir string) string {
	raw, err := os.ReadFile(filepath.Join(dataDir, chosenRoleFile))
	if err != nil {
		return ""
	}
	var doc struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(raw, &doc) != nil || (doc.Role != api.MachineRoleInteractive && doc.Role != api.MachineRoleRunner) {
		return ""
	}
	return doc.Role
}

// initialFace answers the face state a daemon starts with: the persisted
// explicit choice, else the launcher's default (not a choice).
func initialFace(cfg Config) machineFaceState {
	if role := readChosenRole(cfg.DataDir); role != "" {
		return machineFaceState{desiredRole: role, roleChosen: true}
	}
	return machineFaceState{desiredRole: roleFromLauncher(cfg.Launcher)}
}

// faceSleep is the loop's context-aware pause (never a bare sleep).
func faceSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
