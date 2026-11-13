package daemon

// exclusive.go: a lane's `exclusive: true` (workflow contract 5). The
// data-plane proxy forwards every request unchanged save one: a work-item
// create (`POST /api/v1/work-items`) whose bundle_name names a lane of the
// project's lane set declaring `exclusive: true` goes to aihub with
// `"exclusive": true`, so aihub refuses it CORE-409 OPEN_ON_BUNDLE while
// another work item of the project on the lane is not wrapped, failed or
// canceled — the check aihub runs inside the create transaction, so two
// racing creates never both commit. The daemon is the one place that knows
// the lane set a work item runs on; it reads the declaration and judges
// nothing else. A body that is no JSON object, names no bundle, or names a
// lane the set does not carry (or carries refused) is forwarded as sent:
// aihub answers it as it would any create, and a claim refuses a lane the
// set does not run (WFC-123).
//
// Reach: the proxy's (a human session only; a step's `pf step hub file`
// files through the daemon's own client, never through here).

import (
	"encoding/json"
	"net/http"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// workItemsCreatePath is the proxied path of a work-item create.
const workItemsCreatePath = proxyPrefix + "work-items"

// exclusiveCreate answers the body the proxy forwards for method and path
// (the escaped path with its query): body itself, except for a work-item
// create on an exclusive lane, which gets `"exclusive": true`.
func (d *Daemon) exclusiveCreate(method, path string, body []byte) []byte {
	if method != http.MethodPost || path != workItemsCreatePath {
		return body
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}
	var project, bundle string
	if json.Unmarshal(fields["project"], &project) != nil || json.Unmarshal(fields["bundle_name"], &bundle) != nil || project == "" || bundle == "" {
		return body
	}
	if !d.laneExclusive(project, bundle) {
		return body
	}
	fields["exclusive"] = json.RawMessage("true")
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return out
}

// laneExclusive answers whether the lane bundle of project's lane set (the
// built-in lanes and the user lanes of the workspace the project maps to)
// loads and declares `exclusive: true`.
func (d *Daemon) laneExclusive(project, bundle string) bool {
	if d.cfg.WorkflowsDir == "" {
		return false
	}
	ws, err := d.plan().WorkspaceFor(project)
	if err != nil {
		ws = ""
	}
	lane, _, err := workflow.ForWorkspace(d.cfg.WorkflowsDir, ws).Load(bundle)
	return err == nil && lane != nil && lane.Exclusive
}
