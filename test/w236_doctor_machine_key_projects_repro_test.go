package test

// w236 (polyforge#236, root-cause lane, step "repro"): the doctor
// `aihub projects` + `auth` rows fail with the README flow's own machine
// key.
//
// The reported operator sequence (the README's single-machine flow,
// reproduced against the REAL binaries — aihub as a process on a free
// loopback port, the pf CLI as the real binary; no daemon needed, the
// reported rows do not touch it):
//
//   - the flow creates a machine identity — `pf users create
//     --display-name smoke-machine --user-type machine`, no --role —
//     whose global role is the CLI default writer, mints its API key
//     (the daemon's and the single-machine install's credential:
//     install.sh writes api_key = "<machine key>" into cli.toml) and
//     grants it the maintainer role on the project (the README's own
//     grant: the heartbeat's first-registration gate admits an admin or
//     a project maintainer);
//   - everything the flow itself does with that credential succeeds:
//     the machine registers (heartbeat) and the queue's project read —
//     the daemon's ready-queue fetch — answers 200 for it;
//   - and the flow's own health judgement — `pf doctor`, which the
//     README names for every step of the flow ("pf doctor at any
//     point") and install.sh runs at its end — fails exactly the two
//     rows that judge the aihub credential: `aihub projects`
//     ("project ... : AUTH-403: admin role required") and `auth`
//     ("aihub rejected the credential with a 401/403 answer — fix the
//     api key"). Observed on Linux (the report notes the failure is not
//     darwin-specific).
//
// The scenario asserts the contract the goal names: with the flow's own
// machine credential (global role writer, maintainer of the mapped
// project), doctor's `aihub projects` and `auth` rows must PASS. It
// fails today at exactly the reported point — both rows answer the
// AUTH-403 the report describes — and passes once the check's project
// read no longer requires the global admin role (a non-admin project
// read, or project-scoped auth on the endpoint).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// TestW236DoctorProjectsWithTheMachineKey is the mechanical repro: the
// README flow's credential shape against the real pf doctor's --json
// face. The two reported rows are the assertion; unrelated rows (the
// daemon socket row — no daemon in this scenario) are not.
func TestW236DoctorProjectsWithTheMachineKey(t *testing.T) {
	inst := newV1Install(t)

	// The flow's machine identity: `pf users create --display-name
	// smoke-machine --user-type machine` (no --role) mints a machine
	// user whose global role is the CLI default writer.
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: "smoke-machine",
		UserType:    "machine",
	})
	if err != nil {
		t.Fatalf("create the machine user: %v", err)
	}
	var user struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == "" {
		t.Fatalf("machine user answer: %v (%s)", err, raw)
	}
	if user.Role != "writer" {
		t.Fatalf("w236 premise: the flow's machine user must carry the CLI default writer role (no --role in the flow); got %q", user.Role)
	}

	// The machine key — the daemon's / the single-machine install's
	// credential (`api-keys create --user <id>`; plaintext shown once).
	raw, err = inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: user.ID})
	if err != nil {
		t.Fatalf("mint the machine key: %v", err)
	}
	var key struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || key.Plain == "" {
		t.Fatalf("machine key answer: %v (%s)", err, raw)
	}

	// The flow's own grant: the machine user becomes maintainer of the
	// project (`projects members add <project> --user <id> --role
	// maintainer`) — without it the daemon's heartbeat could never
	// register this machine.
	members := []aihubclient.ProjectMember{{UserID: user.ID, Role: "maintainer"}}
	if _, err := inst.client.PatchProject(v1Project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
		t.Fatalf("grant the machine user the maintainer role on %s: %v", v1Project, err)
	}

	// The credential is the flow's own working one: the queue's project
	// read (the daemon's ready-queue fetch) answers with the machine
	// key. If this ever fails, the setup — not the doctor row — is
	// broken, and the two rows below would fail for the wrong reason.
	machine := aihubclient.New(inst.url, key.Plain)
	if _, err := machine.ReadyQueueRaw(v1Project); err != nil {
		t.Fatalf("the machine key must read %s's ready queue (the daemon's own fetch): %v", v1Project, err)
	}

	// The single-machine install's config: the MACHINE key carries the
	// connection (install.sh writes api_key = "<machine key>"), the
	// [workspace] mapping names the project, and the socket points at a
	// scratch path — no daemon runs in this scenario, so that row
	// reports and is not asserted.
	home := t.TempDir()
	ws := filepath.Join(home, "polyforge-workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "cli.toml")
	body := "server = " + strconv.Quote(inst.url) + "\n" +
		"api_key = " + strconv.Quote(key.Plain) + "\n" +
		"daemon_socket = " + strconv.Quote(filepath.Join(t.TempDir(), "pf.sock")) + "\n\n" +
		"[workspace." + strconv.Quote(ws) + "]\n" +
		"projects = [" + strconv.Quote(v1Project) + "]\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// The flow's own health judgement: the real pf doctor, --json face,
	// on a clean machine env (no PF_* overrides — the config file's
	// machine key rules). The exit code is not the assertion (the
	// daemon socket row fails with no daemon); the two reported rows
	// are.
	var out, errb strings.Builder
	c := exec.Command(inst.pfPath, "--json", "--config", cfg, "doctor")
	c.Env = w218DoctorEnv(home, os.Getenv("PATH"))
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		t.Logf("pf --json doctor exited non-zero (expected: no daemon, rows report): %v\nstderr: %s", err, errb.String())
	}
	var rep struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("pf --json doctor must emit the checks document; observed:\n%s\nstderr: %s\nerror: %v", out.String(), errb.String(), err)
	}
	row := func(name string) (string, string) {
		for _, ch := range rep.Checks {
			if ch.Name == name {
				return ch.Status, ch.Detail
			}
		}
		return "", ""
	}

	// Control: the aihub answered — a 401/403 in the reported rows is an
	// auth answer, never an outage.
	if st, detail := row("server reach"); st != "pass" {
		t.Fatalf("w236 control: aihub must answer (server reach pass); got status %q, detail %q", st, detail)
	}

	// THE reported rows.
	st, detail := row("aihub projects")
	if st != "pass" {
		t.Fatalf("w236: the aihub projects row must pass with the flow's own machine key (writer role, maintainer of %s) — the project read must not require the global admin role; got status %q, detail %q", v1Project, st, detail)
	}
	st, detail = row("auth")
	if st != "pass" {
		t.Fatalf("w236: the auth row must pass with the flow's own machine key — the same reads prove the credential; got status %q, detail %q", st, detail)
	}
}
