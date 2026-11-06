package cli

import (
	"testing"
)

// The ORIGIN cell is the origin kind alone: no chat id is
// recorded or rendered any more — an interactive submit is `chat`, an
// auto-claim `auto`. The whole table is golden-compared.
func TestOpsStatusRendersTheOriginKind(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("GET", "/status",
		`{"machine_id":"m1","auto":false,"workspaces":["/srv/pf-ws"],"shutting_down":false,`+
			`"jobs":[`+
			`{"work_item_id":"wi_manual","status":"running","origin":{"kind":"chat","owner":"usr_1"}},`+
			`{"work_item_id":"wi_auto","status":"running","origin":{"kind":"auto"}}`+
			`],"queue":{"pending":1,"running":2,"max_concurrent":2}}`)

	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "ops", "status")
	if code != 0 {
		t.Fatalf("ops status: code=%d stderr=%s", code, stderr)
	}
	golden(t, "OpsStatusOriginCells", stdout,
		`daemon: m1  auto: off  max: 2  queue: 1 pending / 2 running
model health: no model-health summary from this daemon
workspaces: /srv/pf-ws

WORK ITEM  STATUS   SESSION  SPAWN  AGENT PID  QUESTION  STEP-AGE  LAST-WORK  ORIGIN
---------  -------  -------  -----  ---------  --------  --------  ---------  ------
wi_manual  running  -        -      -          -         -         -          chat
wi_auto    running  -        -      -          -         -         -          auto
`)
}
