package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// Golden tests: exact-output assertions for the human table renderers.

func golden(t *testing.T, name string, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestGoldenWITable(t *testing.T) {
	bundle := "bundle-x"
	items := []WIView{
		{WorkItem: api.WorkItem{Slug: "pf-1", Status: "running", Priority: "high", BundleName: &bundle, Goal: "fix the thing"},
			SessionFingerprint: "ab12cd34ef56aa00"},
		{WorkItem: api.WorkItem{Slug: "pf-2", Status: "queued", Priority: "normal", Goal: "a goal that is definitely longer than sixty characters in total"}},
	}
	var b bytes.Buffer
	renderWITable(&b, items)
	golden(t, "WITable", b.String(),
		`SLUG  PROJECT  STATUS   SESSION           PRIORITY  BUNDLE    GOAL
----  -------  -------  ----------------  --------  --------  --------------------------------------------------------------
pf-1  -        running  ab12cd34ef56aa00  high      bundle-x  fix the thing
pf-2  -        queued   -                 normal    -         a goal that is definitely longer than sixty characters in t…
`)
}

func TestGoldenStatus(t *testing.T) {
	s := StatusResponse{
		MachineID:  "m1",
		Auto:       true,
		Workspaces: []string{"/srv/pf-ws"},
		Jobs: []StatusJob{
			{WorkItemID: "wi_abc", Slug: "polyforge#140", Status: "running", SpawnN: 3, SessionFingerprint: "ab12cd34ef56aa00", AgentPID: 42,
				StepAgeSec: int64Ptr(3725), LastWorkAgoSec: int64Ptr(7)},
			// D6: a job presented as needs_input shows its pending
			// question's id in the QUESTION column. A record without a
			// slug shows its id in WORK ITEM. A job with no session
			// fingerprint renders "-" in SESSION (and "-" in SPAWN while
			// no derivation was announced).
			{WorkItemID: "wi_ask", Status: "needs_input", AgentPID: 43,
				NeedsInput: true, QuestionID: "q_zz9"},
		},
		Queue: QueueStats{Pending: 1, Running: 2, MaxConcurrent: 2},
	}
	var b bytes.Buffer
	renderStatus(&b, &s)
	golden(t, "Status", b.String(),
		`daemon: m1  auto: on  max: 2  queue: 1 pending / 2 running
workspaces: /srv/pf-ws

WORK ITEM      STATUS       SESSION           SPAWN  AGENT PID  QUESTION  STEP-AGE  LAST-WORK
-------------  -----------  ----------------  -----  ---------  --------  --------  ---------
polyforge#140  running      ab12cd34ef56aa00  #3     42         -         62:05     00:07
wi_ask         needs_input  -                 -      43         q_zz9     -         -
`)
}

func TestGoldenWIDetail(t *testing.T) {
	milestone := "m1"
	labels := []string{"infra"}
	wi := WIView{WorkItem: api.WorkItem{
		ID: "wi_abc", Slug: "pf-42", ProjectName: "polyforge",
		Goal: "do the thing", Status: "queued", Priority: "high",
		Milestone: &milestone, Labels: labels, CreatedAt: "t0",
	}}
	var b bytes.Buffer
	renderWIDetail(&b, &wi)
	golden(t, "WIDetail", b.String(),
		`pf-42 (wi_abc)
  status:   queued (priority high)
  project:  polyforge
  goal:     do the thing
  milestone: m1
  labels:   infra
  bundle:   -
  created:  t0
`)

	// The v1.5 session audit fields render when the server body carries
	// them (additive decode): fingerprint, machine, owner, started-at,
	// clean_pause — the public handle only, never the secret.
	wi.SessionFingerprint = "ab12cd34ef56aa00"
	wi.SessionMachine = "m-b"
	wi.SessionStartedAt = "2026-10-01T00:00:00Z"
	wi.CleanPause = true
	b.Reset()
	renderWIDetail(&b, &wi)
	golden(t, "WIDetailSession", b.String(),
		`pf-42 (wi_abc)
  status:   queued (priority high)
  project:  polyforge
  goal:     do the thing
  milestone: m1
  labels:   infra
  bundle:   -
  session:  ab12cd34ef56aa00 (machine m-b) since 2026-10-01T00:00:00Z
  clean-pause: yes (operator pause; restart will not auto-resume)
  created:  t0
`)
}

// TestGoldenMachines pins the machines list human render: RUNTIME set
// (go1.99) and unset ("-"), LIVE WI as the bare count (2 and 0), the
// server-computed stale projection visible at the CLI layer (STALE vs
// alive), and heartbeat age rendered as Ns vs ? (nil).
func TestGoldenMachines(t *testing.T) {
	list := &aihubclient.MachinesList{
		Count:             2,
		StaleAfterSeconds: 120,
		Machines: []aihubclient.MachineStatus{
			{ID: "m_1", UserID: "usr_1", RuntimeVersion: "go1.99", Mode: "agent",
				LiveWIIDs: []string{"wi_a", "wi_b"}, LastHeartbeatAt: "t1",
				HeartbeatAgeSeconds: int64Ptr(42), Stale: false},
			{ID: "m_2", UserID: "usr_2", Mode: "grace",
				LiveWIIDs: []string{}, LastHeartbeatAt: "t2", Stale: true},
		},
	}
	var b bytes.Buffer
	renderMachines(&b, list)
	golden(t, "Machines", b.String(),
		`daemons: 2 (stale threshold 120s)
ID   MODE   RUNTIME  LIVE WI  HEARTBEAT AGE  LAST HEARTBEAT  STATE
---  -----  -------  -------  -------------  --------------  -----
m_1  agent  go1.99   2        42s            t1              alive
m_2  grace  -        0        ?              t2              STALE
`)

	// The empty-machines case: header line plus bare table.
	b.Reset()
	renderMachines(&b, &aihubclient.MachinesList{StaleAfterSeconds: 120})
	golden(t, "MachinesEmpty", b.String(),
		`daemons: 0 (stale threshold 120s)
ID  MODE  RUNTIME  LIVE WI  HEARTBEAT AGE  LAST HEARTBEAT  STATE
--  ----  -------  -------  -------------  --------------  -----
`)
}

func TestGoldenMemories(t *testing.T) {
	title := "a titled lesson"
	items := []memorySearchHit{
		{ID: "mem_1", Type: "experience.debug", ContentExcerpt: "first line\nsecond line", UpdatedAt: "t1", Tags: []string{"go"}},
		{ID: "mem_2", Type: "fact.note", Title: &title, ContentExcerpt: "body", UpdatedAt: "t2"},
		{ID: "mem_3", Type: "fact.note", UpdatedAt: "t3"},
	}
	var b bytes.Buffer
	renderMemories(&b, items)
	golden(t, "Memories", b.String(),
		`ID     TYPE              SUMMARY          UPDATED  TAGS
-----  ----------------  ---------------  -------  ----
mem_1  experience.debug  first line       t1       go
mem_2  fact.note         a titled lesson  t2       -
mem_3  fact.note         -                t3       -
`)
}

// int64Ptr is the STEP-AGE / LAST-WORK golden helper (a *int64 field).
func int64Ptr(v int64) *int64 { return &v }

// polyforge#176: the ops status face shows the candidate a dispatch runs
// on, effort included; the column only appears when some job has one.
func TestStatusShowsCandidateEffort(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, &StatusResponse{Jobs: []StatusJob{
		{WorkItemID: "wi_a", Status: "running", Candidate: "claude:claude-opus-5-5:high"},
		{WorkItemID: "wi_b", Status: "running"},
	}})
	out := b.String()
	if !strings.Contains(out, "CANDIDATE") || !strings.Contains(out, "claude:claude-opus-5-5:high") {
		t.Fatalf("status must show the in-flight candidate with its effort:\n%s", out)
	}
	b.Reset()
	renderStatus(&b, &StatusResponse{Jobs: []StatusJob{{WorkItemID: "wi_b", Status: "running"}}})
	if strings.Contains(b.String(), "CANDIDATE") {
		t.Fatalf("no candidate in flight: no column:\n%s", b.String())
	}
}
