package test

// w167_task_list_reporter_tail_acceptance_test.go: the real-binary e2e
// acceptance suite for — `pf --json task list` gains
// `reporter` (the spawn-time display-name snapshot, else `machine`) and
// `report_tail` (one bounded report line), while the text table and every
// existing row field stay unchanged.
//
// Authored BEFORE `code`, from the capsule's artifacts/spec.md only. Every
// bar boots the shipped aihub + a real `pf daemon` + the shipped fakepi and
// drives the real `pf` CLI and task report store, so the baseline reds are
// assertion-level (the two keys are absent from the list row), never build
// errors. The wire shape of the aihub include-caller request is deliberately
// not referenced: it is the plan's decision, and the owner-resolution
// scenario drives it only through `pf task spawn`.
//
// Scenario map (artifacts/e2e-scenarios.md carries GIVEN/WHEN/THEN):
//   S1  Caller id source: PF_USER_ID wins over cli.toml user_id; config is the fallback
//   S2  A human member of the task's project yields that member's display name
//   S3  No id / unresolved / another project's member / machine member / legacy row => machine
//   S4  reporter is a spawn-time snapshot: rename and aihub outage leave it unchanged
//   S5  aihub unavailable at spawn: spawn succeeds, reporter = machine
//   S6  Include-caller resolves the daemon-key owner; the default member face still excludes the caller
//   S7  report_tail equals a short last non-empty line verbatim
//   S8  An over-long last line is `…` plus its last 199 code points
//   S9  Running/empty reports give "", with no error fallback and no second truncation marker
//   S10 Markdown last line is plain text
//   S11 Text table is unchanged; the two new fields are JSON-only
//   S12 A 2 MiB report's row is at most 800 bytes larger than a one-line report's row
//   S13 report_tail is a verbatim substring of `pf task show`'s report; list keeps full report/prompt off
//   S14 The coupled route and verb docs state the fields, reach, trust and snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

const w167Yaml = "steps:\n  e2e:\n    task: { models: [pi:w167/m] }\n"

type w167Rig struct {
	inst *v1Install
	d    *v1Daemon
}

// w167Daemon boots a real daemon on inst, optionally with a dedicated
// aihub key (the daemon-key owner the include-caller request resolves).
func w167Daemon(t *testing.T, inst *v1Install, apiKey string) *w167Rig {
	t.Helper()
	var opts []daemonOpt
	if apiKey != "" {
		opts = append(opts, optAPIKey(apiKey))
	}
	d := newV1Daemon(t, inst, opts...)
	w33Yaml(t, d.workspace, w167Yaml)
	d.start()
	return &w167Rig{inst: inst, d: d}
}

func w167Start(t *testing.T) *w167Rig {
	t.Helper()
	return w167Daemon(t, newV1Install(t), "")
}

// w167Config writes the isolated CLI config, optionally with user_id.
func w167Config(t *testing.T, userID string) string {
	t.Helper()
	body := "# w167 isolated config\n"
	if userID != "" {
		body += "user_id = " + fmt.Sprintf("%q", userID) + "\n"
	}
	path := filepath.Join(t.TempDir(), "w167-cli.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// w167Pf runs the real pf CLI against this rig's daemon, with the given
// config and environment overrides; no implicit caller id is inherited.
func (r *w167Rig) pf(t *testing.T, cfg string, extraEnv []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(r.inst.pfPath, append([]string{"--config", cfg}, args...)...)
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "PF_SERVER="), strings.HasPrefix(kv, "PF_API_KEY="),
			strings.HasPrefix(kv, "PF_USER_ID="), strings.HasPrefix(kv, "PF_DAEMON_SOCKET="),
			strings.HasPrefix(kv, "PF_CHAT_ID="), strings.HasPrefix(kv, "PF_WI_ID="),
			strings.HasPrefix(kv, "PF_TASK_ID="), strings.HasPrefix(kv, "PF_DAEMON_ID="):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PF_SERVER="+r.inst.url, "PF_API_KEY="+r.inst.adminKey,
		"PF_DAEMON_SOCKET="+r.d.sock)
	env = append(env, extraEnv...)
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// w167Spawn issues one real spawn, waits for its terminal state (so the
// two-slot limit never trips between scenarios' spawns) and answers its id.
func (r *w167Rig) w167Spawn(t *testing.T, cfg string, extraEnv []string, prompt string) string {
	t.Helper()
	out, code := r.pf(t, cfg, extraEnv, "task", "spawn", "--project", v1Project, prompt)
	if code != 0 {
		t.Fatalf("pf task spawn: exit %d: %s", code, out)
	}
	id := w40TaskIDRe.FindString(out)
	if id == "" {
		t.Fatalf("pf task spawn printed no task_<16hex> id: %q", out)
	}
	w167WaitTerminal(t, r, id)
	return id
}

func w167RowsRaw(t *testing.T, r *w167Rig) string {
	t.Helper()
	return r.d.pf(t, "task", "list", "--json")
}

func w167WaitTerminal(t *testing.T, r *w167Rig, id string) map[string]any {
	t.Helper()
	var row map[string]any
	waitUntil(t, 20*time.Second, "task "+id+" never reached a terminal state", func() bool {
		raw := w167RowsRaw(t, r)
		if !strings.Contains(raw, id) {
			return false
		}
		row = w123TaskRow(t, raw, id)
		state, _ := row["state"].(string)
		return state != "" && state != string(task.StateRunning)
	})
	return row
}

func w167String(t *testing.T, row map[string]any, field string) string {
	t.Helper()
	v, ok := row[field].(string)
	if !ok {
		t.Fatalf("list row carries no %s string: %v", field, row)
	}
	return v
}

// w167SetMembers replaces the project's member list.
func w167SetMembers(t *testing.T, inst *v1Install, project string, ids ...string) {
	t.Helper()
	members := make([]aihubclient.ProjectMember, 0, len(ids))
	for _, id := range ids {
		members = append(members, aihubclient.ProjectMember{UserID: id, Role: "maintainer"})
	}
	if _, err := inst.client.PatchProject(project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
		t.Fatalf("set %s members %v: %v", project, ids, err)
	}
}

func w167CreateUser(t *testing.T, inst *v1Install, name, typ string) string {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: name + "@w167.invalid", UserType: typ, Role: "writer",
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == "" {
		t.Fatalf("user answer for %s: %v (%s)", name, err, raw)
	}
	return user.ID
}

// S1: the spawn request takes the caller id from PF_USER_ID first and falls
// back to cli.toml's user_id.
func TestW167CallerIDSource(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	bob, _ := w11MintKey(t, inst, "w167-bob")
	w167SetMembers(t, inst, v1Project, alice, bob)
	r := w167Daemon(t, inst, aliceKey)

	// cli.toml says alice; PF_USER_ID says bob — bob must win.
	id := r.w167Spawn(t, w167Config(t, alice), []string{"PF_USER_ID=" + bob}, "w167 env wins")
	row := w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-bob" {
		t.Fatalf("PF_USER_ID wins: reporter = %q, want w167-bob", got)
	}

	// No PF_USER_ID: cli.toml's user_id is the source.
	id = r.w167Spawn(t, w167Config(t, bob), nil, "w167 config fallback")
	row = w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-bob" {
		t.Fatalf("config fallback: reporter = %q, want w167-bob", got)
	}
	fmt.Println("PASS W167-S1")
}

// S2: a task spawned with a human member of the task's project reports that
// member's display name.
func TestW167HumanMemberReporter(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	carol, _ := w11MintKey(t, inst, "w167-carol")
	w167SetMembers(t, inst, v1Project, alice, carol)
	r := w167Daemon(t, inst, aliceKey)

	id := r.w167Spawn(t, w167Config(t, ""), []string{"PF_USER_ID=" + carol}, "w167 human reporter")
	row := w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-carol" {
		t.Fatalf("human member reporter = %q, want w167-carol", got)
	}
	fmt.Println("PASS W167-S2")
}

// S3: everything that does not resolve to a human member of THIS task's
// project reads `machine`: no id, an unresolved id, another project's
// member, a machine member, and a pre-field (legacy) record.
func TestW167ReporterMachineFallbacks(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	machineID := w167CreateUser(t, inst, "w167-machine", "machine")
	dana := w167CreateUser(t, inst, "w167-dana", "human")
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "w167other"}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	w167SetMembers(t, inst, "w167other", dana)
	w167SetMembers(t, inst, v1Project, alice, machineID)
	r := w167Daemon(t, inst, aliceKey)

	for _, tc := range []struct {
		name string
		cfg  string
		env  []string
	}{
		{"no id", w167Config(t, ""), nil},
		{"unresolved id", w167Config(t, ""), []string{"PF_USER_ID=usr_w167_missing"}},
		{"other project member", w167Config(t, ""), []string{"PF_USER_ID=" + dana}},
		{"machine member", w167Config(t, ""), []string{"PF_USER_ID=" + machineID}},
	} {
		id := r.w167Spawn(t, tc.cfg, tc.env, "w167 fallback "+tc.name)
		row := w123TaskRow(t, w167RowsRaw(t, r), id)
		if got := w167String(t, row, "reporter"); got != "machine" {
			t.Fatalf("%s: reporter = %q, want machine", tc.name, got)
		}
	}

	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000001", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 legacy", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "legacy report",
	})
	legacy := w123TaskRow(t, w167RowsRaw(t, r), "task_1670000000000001")
	if got := w167String(t, legacy, "reporter"); got != "machine" {
		t.Fatalf("legacy record: reporter = %q, want machine", got)
	}
	fmt.Println("PASS W167-S3")
}

// S4: reporter is a spawn-time snapshot. Renaming the member and taking
// aihub down leave the existing row unchanged, and list still answers.
func TestW167ReporterSnapshotSurvivesRenameAndOutage(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	w167SetMembers(t, inst, v1Project, alice)
	r := w167Daemon(t, inst, aliceKey)

	id := r.w167Spawn(t, w167Config(t, ""), []string{"PF_USER_ID=" + alice}, "w167 snapshot")
	row := w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-alice" {
		t.Fatalf("spawn reporter = %q, want w167-alice", got)
	}

	if _, err := inst.client.PatchUser(alice, aihubclient.UserPatchRequest{DisplayName: "w167-alicia"}); err != nil {
		t.Fatalf("rename alice: %v", err)
	}
	row = w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-alice" {
		t.Fatalf("after rename reporter = %q, want the spawn-time snapshot w167-alice", got)
	}

	inst.stopAihub()
	row = w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-alice" {
		t.Fatalf("with aihub down reporter = %q, want w167-alice", got)
	}
	fmt.Println("PASS W167-S4")
}

// S5: an aihub outage at spawn does not fail the spawn; reporter = machine.
func TestW167SpawnSucceedsWithAihubDown(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	w167SetMembers(t, inst, v1Project, alice)
	r := w167Daemon(t, inst, aliceKey)
	inst.stopAihub()

	id := r.w167Spawn(t, w167Config(t, ""), []string{"PF_USER_ID=" + alice}, "w167 aihub down")
	row := w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "machine" {
		t.Fatalf("aihub down at spawn: reporter = %q, want machine", got)
	}
	fmt.Println("PASS W167-S5")
}

// S6: the include-caller request resolves the daemon-key owner to their own
// display name, while the member face's default still excludes the caller.
func TestW167IncludeCallerResolvesDaemonOwner(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w167-alice")
	bob, _ := w11MintKey(t, inst, "w167-bob")
	w167SetMembers(t, inst, v1Project, alice, bob)
	r := w167Daemon(t, inst, aliceKey)

	id := r.w167Spawn(t, w167Config(t, ""), []string{"PF_USER_ID=" + alice}, "w167 owner")
	row := w123TaskRow(t, w167RowsRaw(t, r), id)
	if got := w167String(t, row, "reporter"); got != "w167-alice" {
		t.Fatalf("daemon-key owner reporter = %q, want w167-alice", got)
	}

	// The default (no include-caller) face still excludes the caller.
	contacts, err := aihubclient.New(inst.url, aliceKey).ProjectContacts(v1Project)
	if err != nil {
		t.Fatalf("default member face: %v", err)
	}
	for _, contact := range contacts {
		if contact.ID == alice {
			t.Fatalf("the default member face returned the caller %s: %v", alice, contacts)
		}
	}
	foundBob := false
	for _, contact := range contacts {
		if contact.ID == bob {
			foundBob = true
		}
	}
	if !foundBob {
		t.Fatalf("the default member face lost the co-member bob: %v", contacts)
	}
	fmt.Println("PASS W167-S6")
}

// S7: a short last non-empty line is report_tail verbatim (trimmed).
func TestW167ReportTailShortLine(t *testing.T) {
	r := w167Start(t)
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000007", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 short tail", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "first line\n  the last short line  \n\n",
	})
	row := w123TaskRow(t, w167RowsRaw(t, r), "task_1670000000000007")
	if got := w167String(t, row, "report_tail"); got != "the last short line" {
		t.Fatalf("report_tail = %q, want %q", got, "the last short line")
	}
	fmt.Println("PASS W167-S7")
}

// S8: an over-long last line is `…` plus its last 199 code points.
func TestW167ReportTailLongLine(t *testing.T) {
	r := w167Start(t)
	line := strings.Repeat("字", 210) + "TAIL-" + strings.Repeat("尾", 80)
	runes := []rune(line)
	want := "…" + string(runes[len(runes)-199:])
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000008", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 long tail", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "header\n" + line,
	})
	row := w123TaskRow(t, w167RowsRaw(t, r), "task_1670000000000008")
	if got := w167String(t, row, "report_tail"); got != want {
		t.Fatalf("report_tail = %q (%d runes), want %q (%d runes)",
			got, len([]rune(got)), want, len([]rune(want)))
	}
	fmt.Println("PASS W167-S8")
}

// S9: running and empty reports give "", with no error fallback and no extra
// truncation marker.
func TestW167ReportTailEmptyAndNoFallback(t *testing.T) {
	r := w167Start(t)
	for _, tk := range []task.Task{
		{ID: "task_1670000000000009", State: task.StateRunning,
			Spec: task.Spec{Project: v1Project, Prompt: "w167 running", Candidates: []string{"pi:w167/m"},
				MaxDuration: time.Minute, MaxTokens: 1000}, Report: "running body"},
		{ID: "task_1670000000000010", State: task.StateCompleted,
			Spec: task.Spec{Project: v1Project, Prompt: "w167 empty", Candidates: []string{"pi:w167/m"},
				MaxDuration: time.Minute, MaxTokens: 1000}},
		{ID: "task_1670000000000011", State: task.StateFailed, Error: "w167-boom",
			Spec: task.Spec{Project: v1Project, Prompt: "w167 error", Candidates: []string{"pi:w167/m"},
				MaxDuration: time.Minute, MaxTokens: 1000}},
		{ID: "task_1670000000000012", State: task.StateCapped, Truncated: true,
			Spec: task.Spec{Project: v1Project, Prompt: "w167 truncated", Candidates: []string{"pi:w167/m"},
				MaxDuration: time.Minute, MaxTokens: 1000}},
	} {
		w123SeedTask(t, r.d, tk)
	}
	raw := w167RowsRaw(t, r)
	for _, id := range []string{
		"task_1670000000000009", "task_1670000000000010",
		"task_1670000000000011", "task_1670000000000012",
	} {
		row := w123TaskRow(t, raw, id)
		got := w167String(t, row, "report_tail")
		if got != "" {
			t.Fatalf("%s report_tail = %q, want empty (no error fallback, no extra marker)", id, got)
		}
		if strings.Contains(got, "truncated") || strings.Contains(got, "w167-boom") {
			t.Fatalf("%s report_tail carries a fallback/marker: %q", id, got)
		}
	}
	fmt.Println("PASS W167-S9")
}

// S10: a markdown last line is returned as plain text, unchanged.
func TestW167ReportTailMarkdownLine(t *testing.T) {
	r := w167Start(t)
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000013", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 markdown", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "intro\n```json\n",
	})
	row := w123TaskRow(t, w167RowsRaw(t, r), "task_1670000000000013")
	if got := w167String(t, row, "report_tail"); got != "```json" {
		t.Fatalf("markdown report_tail = %q, want %q", got, "```json")
	}
	fmt.Println("PASS W167-S10")
}

// S11: the text table is unchanged; the two new fields are JSON-only.
func TestW167TextTableUnchangedFieldsJSONOnly(t *testing.T) {
	r := w167Start(t)
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000014", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 table", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "table report line",
	})
	text := r.d.pf(t, "task", "list")
	header := strings.Fields(strings.Split(strings.TrimSpace(text), "\n")[0])
	if len(header) != 4 || header[0] != "ID" || header[1] != "PROJECT" || header[2] != "STATE" || header[3] != "AGE" {
		t.Fatalf("text table header = %v, want ID PROJECT STATE AGE (unchanged)", header)
	}
	if strings.Contains(text, "reporter") || strings.Contains(text, "report_tail") {
		t.Fatalf("text table carries a new JSON field: %q", text)
	}
	row := w123TaskRow(t, w167RowsRaw(t, r), "task_1670000000000014")
	w167String(t, row, "reporter")
	w167String(t, row, "report_tail")
	fmt.Println("PASS W167-S11")
}

// S12: the list row stays bounded by report size: a 2 MiB report's row is at
// most 800 bytes larger than a one-line report's row.
func TestW167LargeReportRowBounded(t *testing.T) {
	r := w167Start(t)
	big := strings.Repeat("x", 2<<20)
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000015", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 big", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: big,
	})
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000016", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: "w167 small", Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "one line",
	})
	var doc struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(w167RowsRaw(t, r)), &doc); err != nil {
		t.Fatalf("task list --json: %v", err)
	}
	rows := map[string]map[string]any{}
	for _, raw := range doc.Tasks {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("row: %v", err)
		}
		if id, _ := row["id"].(string); id != "" {
			rows[id] = row
		}
	}
	bigRow, smallRow := rows["task_1670000000000015"], rows["task_1670000000000016"]
	if bigRow == nil || smallRow == nil {
		t.Fatalf("the bounded-row pin misses a seeded task: %v", rows)
	}
	w167String(t, bigRow, "report_tail")
	w167String(t, smallRow, "report_tail")
	bigJSON, _ := json.Marshal(bigRow)
	smallJSON, _ := json.Marshal(smallRow)
	if diff := len(bigJSON) - len(smallJSON); diff > 800 {
		t.Fatalf("2 MiB report row is %d bytes larger than the one-line row, want <= 800", diff)
	}
	fmt.Println("PASS W167-S12")
}

// S13: report_tail is a verbatim substring of `pf task show`'s report (no
// masking), and list still keeps the full report and prompt off.
func TestW167ReportTailSameTextAsShow(t *testing.T) {
	r := w167Start(t)
	const prompt = "w167 prompt\nPROMPT-SECRET body"
	first := "token: w167-SECRET-TOKEN"
	last := "last line AKIAW167SECRET"
	w123SeedTask(t, r.d, task.Task{
		ID: "task_1670000000000017", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: prompt, Candidates: []string{"pi:w167/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: first + "\n" + last,
	})
	raw := w167RowsRaw(t, r)
	row := w123TaskRow(t, raw, "task_1670000000000017")
	tail := w167String(t, row, "report_tail")
	if tail != last {
		t.Fatalf("credential-looking last line = %q, want it unmasked as %q", tail, last)
	}
	var shown struct {
		Report string `json:"report"`
	}
	if err := json.Unmarshal([]byte(r.d.pf(t, "task", "show", "task_1670000000000017", "--json")), &shown); err != nil {
		t.Fatalf("task show --json: %v", err)
	}
	if !strings.Contains(shown.Report, tail) {
		t.Fatalf("report_tail %q is not a substring of the shown report %q", tail, shown.Report)
	}
	for _, leak := range []string{prompt, first} {
		if strings.Contains(raw, leak) {
			t.Fatalf("task list leaks %q: %s", leak, raw)
		}
	}
	fmt.Println("PASS W167-S13")
}

// S14: the coupled docs state the two fields, the any-local-peer reach, and
// reporter's self-asserted snapshot trust.
func TestW167CoupledDocsStateFieldsAndTrust(t *testing.T) {
	routes := strings.ToLower(w103Doc(t, "docs/reference/uds-routes.md"))
	verb := strings.ToLower(w103Doc(t, "skills/polyforge/verbs/task.md"))
	routeLine := ""
	for _, line := range strings.Split(routes, "\n") {
		if strings.Contains(line, "/task/list") {
			routeLine = line
			break
		}
	}
	if routeLine == "" {
		t.Fatalf("docs/reference/uds-routes.md has no /task/list row")
	}
	for _, want := range []string{"reporter", "report_tail", "any local peer"} {
		if !strings.Contains(routeLine, want) {
			t.Fatalf("the /task/list route row does not state %q: %s", want, routeLine)
		}
	}
	for _, want := range []string{"reporter", "report_tail", "self-asserted", "snapshot", "pf task show"} {
		if !strings.Contains(verb, want) {
			t.Fatalf("skills/polyforge/verbs/task.md does not state %q", want)
		}
	}
	fmt.Println("PASS W167-S14")
}
