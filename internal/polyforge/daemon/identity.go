package daemon

// identity.go: the daemon identity model (v2-converge §4).
//
//   - daemon id: <data-dir>/daemon-id holds THE id once written. The
//     first start resolves it (--machine-id, default m-<hostname>) and
//     records it; a later flag value that disagrees refuses to start
//     rather than silently switching identity (aihub refuses to register
//     a new id from a daemon key, so the string must never drift).
//   - the machine role request (POST /machine/role, `pf ops role`): the
//     operator's interactive / runner move the heartbeat negotiates — the
//     role that used to ride `pf chat init` before the chat identity was
//     retired.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

const daemonIDFile = "daemon-id"

// ResolveDaemonID returns the daemon id for dataDir. requested is the
// already-resolved flag/env/default value; explicit reports whether the
// operator supplied it (flag or env) rather than the hostname default.
func ResolveDaemonID(dataDir, requested string, explicit bool) (string, error) {
	path := filepath.Join(dataDir, daemonIDFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		stored := strings.TrimSpace(string(raw))
		if stored != "" {
			if explicit && requested != stored {
				return "", fmt.Errorf("daemon id mismatch: %s records %q but the flag/env asks for %q; refusing to switch identity (remove the flag/env, or delete %s only if you really mean a new daemon)", path, stored, requested, path)
			}
			return stored, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if requested == "" {
		return "", fmt.Errorf("daemon id is empty")
	}
	if err := atomicfile.Write(path, []byte(requested+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("record daemon id: %w", err)
	}
	return requested, nil
}

// machineRoleRoute serves POST /machine/role {"role","force"}: the
// operator's explicit role move — interactive asks the heartbeat for the
// interactive role, force supersedes the machine that holds it. The
// heartbeat negotiates it; the answer is the request and whether it was
// applied (if_unset — the session-start adapters' request — leaves an
// operator's explicit choice alone).
// Reachability: the UDS face behind the write guard (a step or task
// process tree is refused DAEMON-403); it reaches aihub only through the
// next heartbeat.
func (d *Daemon) machineRoleRoute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role    string `json:"role"`
		Force   bool   `json:"force"`
		IfUnset bool   `json:"if_unset"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Role != api.MachineRoleInteractive && body.Role != api.MachineRoleRunner {
		writeErr(w, api.ErrDaemonBadInput, "role must be interactive or runner")
		return
	}
	if body.Force && body.Role != api.MachineRoleInteractive {
		writeErr(w, api.ErrDaemonBadInput, "--force takes the interactive role over; it does not apply to runner")
		return
	}
	if body.Force && body.IfUnset {
		writeErr(w, api.ErrDaemonBadInput, "--if-unset never takes the role over: drop --force or --if-unset")
		return
	}
	applied, err := d.requestDesiredRole(body.Role, body.Force, body.IfUnset)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": body.Role, "force": body.Force, "applied": applied, "profile": d.profile()})
}

// reqOrigin is the optional "origin" a submit-family body carries: the
// caller's owner (PF_USER_ID / config user) — the CLI's wire type.
type reqOrigin cli.SubmitOrigin

// origin maps the request to the recorded origin: every interactive
// submit is an origin of kind chat (an interactive submit; the kind's name
// predates). A nil receiver carries no owner.
func (o *reqOrigin) origin() runtime.Origin {
	if o == nil {
		return runtime.Origin{Kind: runtime.OriginChat}
	}
	return runtime.Origin{Kind: runtime.OriginChat, Owner: o.Owner}
}
