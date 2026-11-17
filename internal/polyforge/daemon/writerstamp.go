package daemon

// writerstamp.go: the daemon's writer attribution of a session memory
// create (polyforge#107's columns, polyforge#119's stamp). The one writer
// of a session memory is the daemon that relays it: POST /api/v1/memories
// through the data-plane proxy goes to the Hub with writer_kind "session"
// and writer_details {machine, cell} of the ROUTED Cell, whatever the
// request body carried — a caller cannot self-report a writer. Unlike the
// exclusive create, which relays a non-object body as sent, a stamping
// failure refuses (DAEMON-400, the owner's Q6 ruling): there is no path
// where a body too broken to stamp still writes a memory without one.
//
// Reach: the proxy's (a human session only; agents are refused
// DAEMON-403 upstream in proxyRoute).

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
)

// memoryCreateBizPath is the decoded business path of the memory create
// the proxy stamps — the DECODED path, not the escaped one: the Hub is
// the one place that decodes a path segment ("%61" and "a" name the
// same route to it), so a percent-encoded alias must not dodge the
// stamp. The query never rides r.URL.Path and never turns the stamp off.
const memoryCreateBizPath = proxyPrefix + "memories"

// stampSessionWriter answers the body a memory create forwards: the
// daemon's session writer identity stamped over the request's (any
// writer_kind / writer_details the caller carried are overridden whole),
// every other field relayed byte for byte (json.RawMessage keeps each
// value — numbers included — verbatim). Any other method or path answers
// (nil, nil): nothing is stamped and the body relays as sent. A create
// whose body is not a complete JSON object answers DAEMON-400 and is
// never forwarded.
func (d *Daemon) stampSessionWriter(method, bizPath, profile string, body []byte) ([]byte, *api.Error) {
	if method != http.MethodPost || bizPath != memoryCreateBizPath {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: fmt.Sprintf(
			"a memory write's body must be a complete JSON object for the daemon to stamp its writer (got %.60q)", body)}
	}
	details, err := json.Marshal(map[string]string{"machine": d.cfg.MachineID, "cell": profile})
	if err != nil {
		return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "the session writer stamp could not be built: " + err.Error()}
	}
	fields["writer_kind"] = json.RawMessage(`"session"`)
	fields["writer_details"] = details
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "the session writer stamp could not be built: " + err.Error()}
	}
	return out, nil
}
