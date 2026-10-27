package runtime

// taskproc.go: is a process part of a one-shot task agent's tree?
// (workbench#53, the re-established #40)
//
// A task process is spawned by the daemon with PF_TASK_ID and PF_DAEMON_ID
// and never with PF_WI_ID (task/dispatch.go taskEnv). Its env carries no
// aihub key and it must never drive the daemon, so the daemon's UDS write
// guard refuses a task peer exactly like a step peer — with the task's OWN
// markers, because a task is not a step of "work item task_…": reusing the
// step markers would also hand it the step-write allowlist. HOME is
// inherited, so cli.toml (and its api_key) stays readable: the guard is an
// accident guard, not a boundary.
//
// The membership walk is the one step agents use (markedProcessOrigin in
// stepproc.go): the process itself, an ancestor, or the leader of its
// process group.

import "strings"

// TaskProcessOrigin reports whether pid belongs to a task process tree of
// the daemon whose machine id is daemonID, and the PF_TASK_ID of the task
// it belongs to.
func TaskProcessOrigin(pid int, daemonID string) (taskID string, ok bool) {
	return markedProcessOrigin(pid, daemonID, taskMarkers)
}

// taskMarkers reads the process's environment via the proc seam: a task
// process of this daemon carries PF_DAEMON_ID=<daemonID> and a non-empty
// PF_TASK_ID. PF_WI_ID is deliberately not consulted — a task is not a
// step, and a step is not a task.
func taskMarkers(pid int, daemonID string) (string, bool) {
	envs, err := procEnviron(pid)
	if err != nil {
		return "", false
	}
	var id string
	var ours bool
	for _, kv := range envs {
		switch {
		case strings.HasPrefix(kv, "PF_TASK_ID="):
			id = strings.TrimPrefix(kv, "PF_TASK_ID=")
		case kv == "PF_DAEMON_ID="+daemonID:
			ours = true
		}
	}
	return id, ours && id != ""
}
