package cli

import (
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
