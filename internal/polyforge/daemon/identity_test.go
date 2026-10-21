package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestResolveDaemonID(t *testing.T) {
	dir := t.TempDir()
	id, err := ResolveDaemonID(dir, "m-host", false)
	if err != nil || id != "m-host" {
		t.Fatalf("first start: %q %v", id, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "daemon-id")); strings.TrimSpace(string(raw)) != "m-host" {
		t.Fatalf("daemon-id file = %q", raw)
	}
	// the stored id wins over a different hostname default
	if id, err = ResolveDaemonID(dir, "m-renamed", false); err != nil || id != "m-host" {
		t.Fatalf("stored must win: %q %v", id, err)
	}
	// an explicit matching value is fine
	if id, err = ResolveDaemonID(dir, "m-host", true); err != nil || id != "m-host" {
		t.Fatalf("explicit match: %q %v", id, err)
	}
	// an explicit differing value refuses
	if _, err = ResolveDaemonID(dir, "m-other", true); err == nil || !strings.Contains(err.Error(), "refusing to switch identity") {
		t.Fatalf("explicit mismatch must refuse, got %v", err)
	}
}

func TestChatInitStableAcrossStoresAndSessions(t *testing.T) {
	dir := t.TempDir()
	s := &chatStore{path: filepath.Join(dir, chatsFile)}
	a, err := s.Init("claude", "sess-1")
	if err != nil || !a.Created || !strings.HasPrefix(a.ChatID, "chat_") {
		t.Fatalf("first: %+v %v", a, err)
	}
	// a new store over the same file (daemon restart) resumes the same chat
	b, _ := (&chatStore{path: s.path}).Init("claude", "sess-1")
	if b.ChatID != a.ChatID || b.Created {
		t.Fatalf("resume: %+v vs %+v", b, a)
	}
	if c, _ := s.Init("pi", "sess-1"); c.ChatID == a.ChatID {
		t.Fatal("same session id under another harness must be another chat")
	}
	if c, _ := s.Init("claude", "sess-2"); c.ChatID == a.ChatID {
		t.Fatal("another session must be another chat")
	}
}

func TestChatInitRoute(t *testing.T) {
	d := &Daemon{chats: &chatStore{path: filepath.Join(t.TempDir(), chatsFile)}}
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		d.chatInitRoute(w, httptest.NewRequest("POST", "/chat/init", strings.NewReader(body)))
		return w
	}
	if w := call(`{"harness":"claude","harness_session":"s"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"chat_id":"chat_`) {
		t.Fatalf("ok: %d %s", w.Code, w.Body)
	}
	if w := call(`{"harness":"emacs","harness_session":"s"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad harness: %d", w.Code)
	}
	if w := call(`{"harness":"pi"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing session: %d", w.Code)
	}
}

func TestReqOriginKinds(t *testing.T) {
	var none *reqOrigin
	if o := none.origin(); o.Kind != runtime.OriginChat || o.ChatID != "" {
		t.Errorf("nil (chat-less manual): %+v", o)
	}
	if o := (&reqOrigin{ChatID: "chat_x", Owner: "u"}).origin(); o.Kind != runtime.OriginChat || o.ChatID != "chat_x" || o.Owner != "u" {
		t.Errorf("chat: %+v", o)
	}
	if o := (&reqOrigin{Owner: "u"}).origin(); o.Kind != runtime.OriginChat || o.ChatID != "" || o.Owner != "u" {
		t.Errorf("chat-less manual: %+v", o)
	}
}
