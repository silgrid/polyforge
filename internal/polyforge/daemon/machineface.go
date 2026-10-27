package daemon

// machineface.go: the daemon's cross-machine face state (workbench#11).
// The heartbeat negotiates the role; the query long-poll runs for every
// registered daemon, the message long-poll only for an interactive
// daemon whose operator has a second live machine. A heartbeat response
// without machine_face_version disables the whole face (old hub, no
// compatibility branch).

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// MachineFace is the signed cross-machine client half the daemon uses
// (the production *aihubclient.Client; Config.MachineFace swaps it in
// tests).
type MachineFace interface {
	Inbox(faces string, wait time.Duration) (*api.MachineInbox, error)
	AnswerQuery(qid string, body api.QueryAnswerBody) error
	ProgressQuery(wi, artifactPath string) (*api.QueryAnswerBody, error)
	AckMessage(conversationID string, seq int64) error
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
	available      bool
	version        int
	role           string
	desiredRole    string
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

// requestDesiredRole records an operator move (`pf chat init --force`).
func (d *Daemon) requestDesiredRole(role string, force bool) error {
	if role != api.MachineRoleInteractive && role != api.MachineRoleRunner {
		return fmt.Errorf("role must be interactive or runner")
	}
	d.mfaceMu.Lock()
	d.face.desiredRole = role
	if force {
		d.face.forceNext = true
	}
	d.mfaceMu.Unlock()
	return nil
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
