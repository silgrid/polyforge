package daemon

// tasknotices.go: the delivery of a task's terminal summary to its origin
// chat (workbench#53, carried from #40), riding the resident watcher's fetch-then-ack model
// rather than a new push path.
//
//	GET  /chats/{id}/task-notices        {"notices":[{task_id, text}]}
//	POST /chats/{id}/task-notices/ack    {"ids":[task_id...]}
//
// Reachability: the UDS face only. The GET is a read any local peer may
// make (like GET /status); the ack is a POST behind the write guard, so a
// step or task process tree is refused DAEMON-403. The summary never goes
// to aihub or memory.

import (
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func (d *Daemon) taskNoticesRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	out := protocol.TaskNotices{Notices: []protocol.TaskNotice{}}
	for _, n := range d.tasks.Notices(id) {
		out.Notices = append(out.Notices, protocol.TaskNotice{TaskID: n.TaskID, Text: n.Text})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) taskNoticesAckRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	var req protocol.TaskNoticeAck
	if !decodeBody(w, r, &req) {
		return
	}
	if err := d.tasks.AckNotices(id, req.IDs); err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
