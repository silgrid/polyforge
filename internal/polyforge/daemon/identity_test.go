package daemon

import (
	"encoding/json"
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
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	aPlan := mappingFor(ws)
	subs, err := newSubStore(filepath.Join(dir, chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{chats: &chatStore{path: filepath.Join(dir, chatsFile)}, subs: subs, cfg: Config{Mapping: aPlan}}
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

// TestChatInitRecordsWorkspaceTag — the route records the
// chat's workspace tag on the chat-subs chatDoc (recorded even
// untagged), refreshes it on a re-init, refuses a workspace the
// daemon's own mapping does not carry, and leaves chats.json — the
// (harness, session)→id identity map — with its exact shape.
func TestChatInitRecordsWorkspaceTag(t *testing.T) {
	dir := t.TempDir()
	wsA, wsB := filepath.Join(dir, "wsA"), filepath.Join(dir, "wsB")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	aPlan := mappingFor(wsA, "projA")
	subs, err := newSubStore(filepath.Join(dir, chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{chats: &chatStore{path: filepath.Join(dir, chatsFile)}, subs: subs, cfg: Config{Mapping: aPlan}}
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		d.chatInitRoute(w, httptest.NewRequest("POST", "/chat/init", strings.NewReader(body)))
		return w
	}
	chat := func() string {
		t.Helper()
		w := call(`{"harness":"pi","harness_session":"gmi14","workspace":"` + wsA + `"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("chat init: %d %s", w.Code, w.Body)
		}
		var rec chatRecord
		if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil || rec.ChatID == "" {
			t.Fatalf("chat init answer: %s (%v)", w.Body, err)
		}
		return rec.ChatID
	}
	id := chat()
	if tag := subs.WorkspaceOf(id); tag != wsA {
		t.Fatalf("the tag must be recorded on the chat's chatDoc: %q", tag)
	}
	// a re-init from another cwd refreshes the tag
	w := call(`{"harness":"pi","harness_session":"gmi14","workspace":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("re-init untagged: %d %s", w.Code, w.Body)
	}
	if tag := subs.WorkspaceOf(id); tag != "" {
		t.Fatalf("re-init must refresh the tag from the current cwd: %q", tag)
	}
	// an untagged chat is a RECORDED chat (present in the tag store)
	subs.mu.Lock()
	_, recorded := subs.doc.Chats[id]
	subs.mu.Unlock()
	if !recorded {
		t.Fatal("an untagged chat must exist in the tag store")
	}
	// a workspace outside the daemon's mapping is a DAEMON-400 refusal
	w = call(`{"harness":"pi","harness_session":"gmi14","workspace":"` + wsB + `"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), wsB) || !strings.Contains(w.Body.String(), "DAEMON-400") {
		t.Fatalf("a config drift must refuse naming the workspace: %d %s", w.Code, w.Body)
	}
	// chats.json keeps its exact shape: (harness, session)→id only
	raw, err := os.ReadFile(filepath.Join(dir, chatsFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Chats map[string]string `json:"chats"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Chats) != 1 || doc.Chats["pi:gmi14"] != id {
		t.Fatalf("chats.json shape: %s (%v)", raw, err)
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
