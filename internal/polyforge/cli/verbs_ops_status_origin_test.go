package cli

import (
	"testing"
)

// polyforge#208: a chat-kind origin with an empty chat id (the pre-#206
// compound-command submits that lost PF_CHAT_ID) renders as `chat:none`;
// the old bare `chat` shape is deleted outright — no dual rendering. The
// sibling rows pin the `:none` suffix to kind chat: an `auto` origin
// (also an empty id) stays `auto`, and a chat with an id stays
// `chat:<full id>`. The whole table is golden-compared, so the ORIGIN
// cell is asserted per row as an exact cell — a bare `chat` cannot pass.
func TestOpsStatusRendersChatNoneForChatlessChatOrigin(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("GET", "/status",
		`{"machine_id":"m1","auto":false,"workspaces":["/srv/pf-ws"],"shutting_down":false,`+
			`"jobs":[`+
			`{"work_item_id":"wi_chatless","status":"running","origin":{"kind":"chat"}},`+
			`{"work_item_id":"wi_auto","status":"running","origin":{"kind":"auto"}},`+
			`{"work_item_id":"wi_chat","status":"running","origin":{"kind":"chat","chat_id":"chat_z"}}`+
			`],"queue":{"pending":1,"running":2,"max_concurrent":2}}`)

	// Full verb drive: `pf ops status` (human table mode) against the
	// same socket body — raw JSON → decode → renderStatus.
	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "ops", "status")
	if code != 0 {
		t.Fatalf("ops status: code=%d stderr=%s", code, stderr)
	}
	golden(t, "OpsStatusOriginCells", stdout,
		`daemon: m1  auto: off  max: 2  queue: 1 pending / 2 running
workspaces: /srv/pf-ws

WORK ITEM    STATUS   SESSION  SPAWN  AGENT PID  QUESTION  STEP-AGE  LAST-WORK  ORIGIN
-----------  -------  -------  -----  ---------  --------  --------  ---------  -----------
wi_chatless  running  -        -      -          -         -         -          chat:none
wi_auto      running  -        -      -          -         -         -          auto
wi_chat      running  -        -      -          -         -         -          chat:chat_z
`)
}
