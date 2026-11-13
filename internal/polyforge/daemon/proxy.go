package daemon

// proxy.go: the data-plane proxy, `ANY /aihub/{path...}` — every AIHub
// business call the CLI makes (file, queue, cancel, recall, remember,
// memory, msg, users, api-keys, machines, projects, backup) reaches the Hub
// through the routed Cell's effective connection: method, path, query and
// body forwarded, status and body answered unchanged — save a work-item
// create on a lane declaring `exclusive: true`, forwarded exclusive
// (exclusive.go). The forwarded path is
// the request's ESCAPED path, byte for byte: decoding it here and letting the
// client re-parse it turned an escaped reserved byte ("%23", "%3F", "%2F",
// "%25") into URL syntax, so a "<project>#<seq>" work-item reference
// truncated at the "#" on the wire. The Hub is the one place
// that decodes a path segment. No queue slot, no cache, no retry (a
// side-effecting request is never resent), and the CLI never dials a Hub
// itself. Only /api/v1/ paths are forwarded: the Hub's web console (/web/)
// and git mount (/git/) are not business faces.
//
// Reach: a human session only. A step or task agent's request is refused
// DAEMON-403 (agents hold no AIHub access; their memory faces are the
// /step/ and /task/ routes).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// hubForwarder is the raw relay a Cell's hub client provides
// (*aihubclient.Client.Forward).
type hubForwarder interface {
	Forward(ctx context.Context, method, pathQuery string, body []byte, contentType string) (int, string, []byte, error)
}

// proxyPrefix is the only Hub path family the proxy forwards.
const proxyPrefix = "/api/v1/"

// proxyRoute serves ANY /aihub/{path...}.
func (h *Host) proxyRoute(w http.ResponseWriter, r *http.Request) {
	if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason+" (agents hold no AIHub access)")
		return
	}
	profile := r.Header.Get(protocol.ProfileHeader)
	if profile == "" {
		profile = cellcfg.DefaultProfile
	}
	d, ae := h.routeTo(profile)
	if ae != nil {
		writeJSON(w, ae.HTTPStatus(), ae)
		return
	}
	d.serveProxy(w, r, profile)
}

// serveProxy forwards one business request through this Cell's connection.
func (d *Daemon) serveProxy(w http.ResponseWriter, r *http.Request, profile string) {
	// EscapedPath, not PathValue: the wildcard has already been decoded once,
	// and Forward parses what it is handed as a URL again. Only the /aihub
	// mount prefix is dropped; every byte of the business path stays escaped.
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/aihub")
	if !strings.HasPrefix(path, proxyPrefix) {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("the data-plane proxy forwards %s* paths only (got %s)", proxyPrefix, path))
		return
	}
	if why := d.identityConflict(); why != "" {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("Cell %q refuses AIHub business: %s", profile, why))
		return
	}
	if why := d.missingCredential(); why != "" {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("Cell %q refuses AIHub business: %s", profile, why))
		return
	}
	fwd, ok := d.hub.(hubForwarder)
	if !ok {
		writeErr(w, api.ErrDaemonInternal, "this Cell's hub client cannot relay business requests")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, api.ErrDaemonBadInput, "read body: "+err.Error())
		return
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	body = d.exclusiveCreate(r.Method, path, body)
	status, ct, data, err := fwd.Forward(r.Context(), r.Method, path, body, r.Header.Get("Content-Type"))
	if err != nil {
		d.noteHubFailure(err)
		outcome := "nothing was retried"
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			outcome = "the outcome of this side-effecting request is unknown and it was not resent — check the Hub before repeating it"
		}
		writeErr(w, api.ErrDaemonHubUnavailable, fmt.Sprintf("Cell %q: its AIHub %s is unreachable: %v — %s; other Cells are unaffected", profile, d.cfg.Server, err, outcome))
		return
	}
	if status == http.StatusUnauthorized {
		// the Hub refuses this Cell's credential: the Cell reads degraded
		// (its claims stop) — the answer itself is relayed unchanged
		d.noteHubFailure(fmt.Errorf("the AIHub refused this Cell's credential (HTTP 401)"))
	} else {
		d.noteHubOK()
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
