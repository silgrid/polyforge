package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestChatInitPrintsIDAndSendsHarnessSession(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/chat/init", `{"chat_id":"chat_abc123","harness":"claude","harness_session":"s-1","created":true}`)
	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"chat", "init", "--harness", "claude", "--harness-session", "s-1")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "chat_abc123" {
		t.Errorf("stdout = %q, want just the chat id", stdout)
	}
	body := d.rec.last().Body
	if !strings.Contains(body, `"harness":"claude"`) || !strings.Contains(body, `"harness_session":"s-1"`) {
		t.Errorf("request body = %s", body)
	}
	code, stdout, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"--json", "chat", "init", "--harness", "claude", "--harness-session", "s-1")
	if code != 0 || !strings.Contains(stdout, `"chat_id"`) {
		t.Errorf("--json: code=%d stdout=%s", code, stdout)
	}
}

func TestChatInitRejectsBadHarness(t *testing.T) {
	code, _, stderr := runPf(t, map[string]string{}, "chat", "init", "--harness", "vim", "--harness-session", "x")
	if code == 0 || !strings.Contains(stderr, "--harness") {
		t.Errorf("code=%d stderr=%s", code, stderr)
	}
}

func TestSubmitBodyCarriesOrigin(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/claim", `{"accepted":true,"work_item_id":"wi_abc"}`)
	c := NewDaemonClient(d.sock)
	c.Origin = &SubmitOrigin{ChatID: "chat_z", Owner: "usr_1"}
	if _, err := c.ClaimWithModels("wi_abc", "", nil); err != nil {
		t.Fatal(err)
	}
	if b := d.rec.last().Body; !strings.Contains(b, `"origin":{"chat_id":"chat_z","owner":"usr_1"}`) {
		t.Errorf("body = %s", b)
	}
}

func TestConfigReadsChatIDFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PF_CHAT_ID", "chat_env")
	cfg, err := LoadConfig(writeTempConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatID != "chat_env" {
		t.Errorf("ChatID = %q", cfg.ChatID)
	}
}

func TestStatusRendersChatNoneForChatlessChatOrigin(t *testing.T) {
	// polyforge#208: a chat-kind origin with an empty chat id renders the
	// whole ORIGIN cell as `chat:none` (bare `chat` is deleted); `:none` is
	// pinned to kind chat, so an id-less `auto` stays `auto` and a chat with
	// an id stays `chat:<full id>`.
	var b strings.Builder
	renderStatus(&b, &StatusResponse{
		MachineID: "m-h", DisplayName: "laptop",
		Jobs: []StatusJob{
			{WorkItemID: "wi_chatless", Status: "running", Origin: &protocol.Origin{Kind: "chat"}},
			{WorkItemID: "wi_auto", Status: "running", Origin: &protocol.Origin{Kind: "auto"}},
			{WorkItemID: "wi_chat", Status: "running", Origin: &protocol.Origin{Kind: "chat", ChatID: "chat_z"}},
		},
	})
	originCells := map[string]string{}
	for _, line := range strings.Split(b.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && strings.HasPrefix(fields[0], "wi_") {
			originCells[fields[0]] = fields[len(fields)-1]
		}
	}
	for wi, want := range map[string]string{
		"wi_chatless": "chat:none",
		"wi_auto":     "auto",
		"wi_chat":     "chat:chat_z",
	} {
		if got := originCells[wi]; got != want {
			t.Errorf("ORIGIN cell for %s = %q, want %q\n%s", wi, got, want, b.String())
		}
	}
}

func TestStatusRendersDaemonNameAndOrigin(t *testing.T) {
	var b strings.Builder
	renderStatus(&b, &StatusResponse{
		MachineID: "m-h", DisplayName: "laptop",
		Jobs: []StatusJob{{WorkItemID: "wi_1", Status: "running", Origin: &protocol.Origin{Kind: "chat", ChatID: "chat_z"}}},
	})
	for _, want := range []string{"daemon: m-h (laptop)", "ORIGIN", "chat:chat_z"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}
}

// withRootsConfig writes a scratch cli.toml mapping the given workspace
// roots and answers the XDG_CONFIG_HOME env entry pointing a runPf call
// at it (the CLI resolves the session cwd against its OWN
// mapping before the daemon records the tag).
func withRootsConfig(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("server = \"http://gmi14.invalid:1\"\n\n")
	for i, root := range roots {
		fmt.Fprintf(&b, "[workspace.%q]\nprojects = [\"gmi14p%d\"]\n\n", root, i)
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

// TestChatInitSendsResolvedWorkspace — `pf chat init` resolves
// the session cwd against its own [workspace] mapping (longest prefix)
// and sends the resolved root in the POST body; an unmapped cwd sends
// "". The stdout contract stays the bare chat id.
func TestChatInitSendsResolvedWorkspace(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/chat/init", `{"chat_id":"chat_ws1","harness":"pi","harness_session":"s-ws","created":true}`)
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	deep := filepath.Join(root, "deep")
	for _, p := range []string{filepath.Join(deep, "capsules"), filepath.Join(dir, "elsewhere")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := withRootsConfig(t, root, deep)
	env["PF_DAEMON_SOCKET"] = d.sock
	t.Chdir(filepath.Join(deep, "capsules"))
	code, stdout, stderr := runPf(t, env, "chat", "init", "--harness", "pi", "--harness-session", "s-ws")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "chat_ws1" {
		t.Errorf("stdout = %q, want the bare chat id", stdout)
	}
	if body := d.rec.last().Body; !strings.Contains(body, `"workspace":"`+deep+`"`) {
		t.Errorf("a cwd under both roots must send the LONGEST prefix: %s", body)
	}
	// an unmapped cwd sends "" (the chat is minted untagged)
	t.Chdir(filepath.Join(dir, "elsewhere"))
	if _, _, stderr := runPf(t, env, "chat", "init", "--harness", "pi", "--harness-session", "s-ws"); stderr != "" {
		t.Fatalf("unmapped init: %s", stderr)
	}
	if body := d.rec.last().Body; !strings.Contains(body, `"workspace":""`) {
		t.Errorf("an unmapped cwd must send an empty workspace: %s", body)
	}
}

// TestWatchHarnessSessionResolvesWorkspace — the --harness-session
// resolution mints chats through the same init path, so it carries the
// same resolved workspace tag (one fact, one place). XDG_RUNTIME_DIR is
// isolated so the session→chat cache in watcher.RuntimeDir() cannot
// short-circuit the init: the assertion rides the POST /chat/init body,
// and a cache hit (a prior run's leftover, e.g. the drift check's shared
// testenv runtime dir) answers without it (verbs_watch_test.go pattern).
func TestWatchHarnessSessionResolvesWorkspace(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/chat/init", `{"chat_id":"chat_ws2","harness":"claude","harness_session":"s-hx","created":true}`)
	d.answer("GET", "/chats/chat_ws2/subscriptions", `{"chat_id":"chat_ws2","subscriptions":[]}`)
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	if err := os.MkdirAll(filepath.Join(root, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := withRootsConfig(t, root)
	env["PF_DAEMON_SOCKET"] = d.sock
	env["XDG_RUNTIME_DIR"] = t.TempDir()
	t.Chdir(filepath.Join(root, "repos"))
	code, _, stderr := runPf(t, env, "watch", "list", "--harness", "claude", "--harness-session", "s-hx")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var sent string
	for _, req := range d.rec.all() {
		if req.Path == "/chat/init" {
			sent = req.Body
		}
	}
	if !strings.Contains(sent, `"workspace":"`+root+`"`) {
		t.Errorf("the harness-session resolution must send the resolved workspace: %s", sent)
	}
}
