package daemon

// identity.go: the daemon identity model (v2-converge §4).
//
//   - daemon id: <data-dir>/daemon-id holds THE id once written. The
//     first start resolves it (--machine-id, default m-<hostname>) and
//     records it; a later flag value that disagrees refuses to start
//     rather than silently switching identity (aihub refuses to register
//     a new id from a daemon key, so the string must never drift).
//   - chat ids: <data-dir>/chats.json maps (harness, harness-session) to
//     a chat id so a resumed conversation keeps the same chat. A chat id
//     is a notification label, never a credential.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

const (
	daemonIDFile = "daemon-id"
	chatsFile    = "chats.json"
)

// ResolveDaemonID returns the daemon id for dataDir. requested is the
// already-resolved flag/env/default value; explicit reports whether the
// operator supplied it (flag or env) rather than the hostname default.
func ResolveDaemonID(dataDir, requested string, explicit bool) (string, error) {
	path := filepath.Join(dataDir, daemonIDFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		stored := strings.TrimSpace(string(raw))
		if stored != "" {
			if explicit && requested != stored {
				return "", fmt.Errorf("daemon id mismatch: %s records %q but the flag/env asks for %q; refusing to switch identity (remove the flag/env, or delete %s only if you really mean a new daemon)", path, stored, requested, path)
			}
			return stored, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if requested == "" {
		return "", fmt.Errorf("daemon id is empty")
	}
	if err := atomicfile.Write(path, []byte(requested+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("record daemon id: %w", err)
	}
	return requested, nil
}

// chatStore persists the (harness, harness-session) -> chat id mapping.
type chatStore struct {
	mu   sync.Mutex
	path string
}

type chatsDoc struct {
	Version int               `json:"version"`
	Chats   map[string]string `json:"chats"` // "<harness>:<session>" -> chat id
}

// chatRecord is the POST /chat/init answer (the CLI's wire type).
type chatRecord = cli.ChatRecord

func newChatID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "chat_" + hex.EncodeToString(b), nil
}

// Init returns the chat id for the harness session, minting it on first use.
func (s *chatStore) Init(harness, session string) (chatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := chatsDoc{Version: 1, Chats: map[string]string{}}
	raw, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &doc); err != nil {
			return chatRecord{}, fmt.Errorf("%s unparsable: %w", chatsFile, err)
		}
		if doc.Chats == nil {
			doc.Chats = map[string]string{}
		}
	case !os.IsNotExist(err):
		return chatRecord{}, err
	}
	key := harness + ":" + session
	rec := chatRecord{Harness: harness, HarnessSession: session}
	if id, ok := doc.Chats[key]; ok {
		rec.ChatID = id
		return rec, nil
	}
	id, err := newChatID()
	if err != nil {
		return chatRecord{}, err
	}
	doc.Chats[key] = id
	blob, err := json.MarshalIndent(&doc, "", "  ")
	if err != nil {
		return chatRecord{}, err
	}
	if err := atomicfile.Write(s.path, blob, 0o600); err != nil {
		return chatRecord{}, err
	}
	rec.ChatID, rec.Created = id, true
	return rec, nil
}

// chatInitRoute serves POST /chat/init {"harness","harness_session"}.
func (d *Daemon) chatInitRoute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness        string `json:"harness"`
		HarnessSession string `json:"harness_session"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Harness != "claude" && body.Harness != "pi" {
		writeErr(w, api.ErrDaemonBadInput, "harness must be claude or pi")
		return
	}
	if strings.TrimSpace(body.HarnessSession) == "" {
		writeErr(w, api.ErrDaemonBadInput, "harness_session is required")
		return
	}
	rec, err := d.chats.Init(body.Harness, body.HarnessSession)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// reqOrigin is the optional "origin" a submit-family body carries: the
// caller's chat id (PF_CHAT_ID) and owner (PF_USER_ID / config user) —
// the CLI's wire type.
type reqOrigin cli.SubmitOrigin

// origin maps the request to the recorded origin: every interactive
// submit is a chat origin — the ChatID labels the harness chat when
// one is present, and an empty ChatID marks a chat-less manual submit.
// A nil receiver is the plain chat-less shape.
func (o *reqOrigin) origin() runtime.Origin {
	if o == nil {
		return runtime.Origin{Kind: runtime.OriginChat}
	}
	return runtime.Origin{Kind: runtime.OriginChat, ChatID: o.ChatID, Owner: o.Owner}
}
