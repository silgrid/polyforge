package server

// progress_test.go: the signed caller -> aihub query doors. Project read
// is authorized BEFORE anything is enqueued, every holder-resolution
// branch answers its typed code, and nothing is persisted.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
)

func signedMachineFor(t *testing.T, e *srvEnv, machineID, operatorID string) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if _, err := e.admin.HeartbeatAuthorized(core.MachineHeartbeat{
		MachineID: machineID, PublicKey: pubB64,
		Launcher: "pi-web", Role: "runner",
	}, core.HeartbeatProof{CallerID: operatorID, Signed: true, KeyB64: pubB64}, nil); err != nil {
		t.Fatalf("register %s: %v", machineID, err)
	}
	return priv, operatorID
}

func signedPost(t *testing.T, e *srvEnv, key, machineID string, priv ed25519.PrivateKey, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Unix()
	req, err := http.NewRequest("POST", e.ts.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set(machinesign.HeaderMachineID, machineID)
	req.Header.Set(machinesign.HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(machinesign.HeaderSignature, machinesign.Sign(priv, "POST", path, raw, ts, machineID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func newRunningWI(t *testing.T, e *srvEnv, holder string) string {
	t.Helper()
	var created map[string]any
	code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": "remote query"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create WI: %d %s", code, body)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create WI answer: %s", body)
	}
	if _, err := e.db.Exec(`UPDATE work_items SET status=?1, session_machine=?2 WHERE id=?3`,
		api.StatusRunning, holder, id); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	return id
}

func errCodeOf(body []byte) string {
	var ae api.Error
	_ = json.Unmarshal(body, &ae)
	return ae.Code
}

func TestProgressQueryAuthorizesBeforeRouting(t *testing.T) {
	e := newSrvEnv(t)
	outsider, err := e.admin.CreateUser("outsider", "outsider@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := e.admin.CreateAPIKey(outsider.ID, "outsider", "")
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := signedMachineFor(t, e, "m-outsider", outsider.ID)
	wi := newRunningWI(t, e, "m-outsider")

	code, body := signedPost(t, e, ok.Plain, "m-outsider", priv,
		"/api/v1/work-items/"+wi+"/progress-query", api.ProgressQuery{WorkItem: wi})
	if code != http.StatusForbidden || errCodeOf(body) != api.ErrAuthForbidden {
		t.Fatalf("a caller with no project read must be AUTH-403 before routing: %d %s", code, body)
	}
	e.srv.pending.mu.Lock()
	pending := len(e.srv.pending.items)
	e.srv.pending.mu.Unlock()
	if pending != 0 {
		t.Fatalf("an unauthorized query was enqueued: %d pending", pending)
	}
}

func TestProgressQueryTypedHolderBranches(t *testing.T) {
	e := newSrvEnv(t)
	e.srv.StaleAfter = 2 * time.Second
	priv, _ := signedMachineFor(t, e, "m-holder", e.adminID)

	// CORE-404: unknown work item.
	code, body := signedPost(t, e, e.key, "m-holder", priv,
		"/api/v1/work-items/wi_missing/progress-query", api.ProgressQuery{WorkItem: "wi_missing"})
	if code != http.StatusNotFound || errCodeOf(body) != api.ErrCoreNotFound {
		t.Fatalf("unknown WI: %d %s", code, body)
	}

	// CORE-504: stale holder names the machine and its age.
	stale := newRunningWI(t, e, "m-holder")
	if _, err := e.db.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id='m-holder'`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	code, body = signedPost(t, e, e.key, "m-holder", priv,
		"/api/v1/work-items/"+stale+"/progress-query", api.ProgressQuery{WorkItem: stale})
	if code != http.StatusGatewayTimeout || errCodeOf(body) != api.ErrCoreStaleHolder {
		t.Fatalf("stale holder: %d %s", code, body)
	}
	var staleErr api.Error
	_ = json.Unmarshal(body, &staleErr)
	if !bytes.Contains([]byte(staleErr.Detail), []byte("m-holder")) || !bytes.Contains([]byte(staleErr.Detail), []byte("s ago")) {
		t.Fatalf("stale detail must name the machine and its age: %s", staleErr.Detail)
	}

	// A fresh heartbeat un-stales it; CORE-410: the holder row is gone.
	if _, err := e.admin.HeartbeatAuthorized(core.MachineHeartbeat{MachineID: "m-holder"},
		core.HeartbeatProof{CallerID: e.adminID, Signed: true}, nil); err != nil {
		t.Fatal(err)
	}
	dereg := newRunningWI(t, e, "m-gone")
	code, body = signedPost(t, e, e.key, "m-holder", priv,
		"/api/v1/work-items/"+dereg+"/progress-query", api.ProgressQuery{WorkItem: dereg})
	if code != http.StatusGone || errCodeOf(body) != api.ErrCoreGone {
		t.Fatalf("deregistered holder: %d %s", code, body)
	}

	// CORE-501: a fresh holder without the progress-query capability.
	nocapKey := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	if _, err := e.admin.HeartbeatAuthorized(core.MachineHeartbeat{
		MachineID: "m-nocap", PublicKey: nocapKey,
	}, core.HeartbeatProof{CallerID: e.adminID, Signed: true, KeyB64: nocapKey}, nil); err != nil {
		t.Fatal(err)
	}
	nocap := newRunningWI(t, e, "m-nocap")
	code, body = signedPost(t, e, e.key, "m-holder", priv,
		"/api/v1/work-items/"+nocap+"/progress-query", api.ProgressQuery{WorkItem: nocap})
	if code != http.StatusNotImplemented || errCodeOf(body) != api.ErrCoreUnsupported {
		t.Fatalf("uncapable holder: %d %s", code, body)
	}

	// Artifact queries walk the same gate.
	code, body = signedPost(t, e, e.key, "m-holder", priv,
		"/api/v1/work-items/wi_missing/artifact-query", api.ProgressQuery{WorkItem: "wi_missing", ArtifactPath: "spec.md"})
	if code != http.StatusNotFound || errCodeOf(body) != api.ErrCoreNotFound {
		t.Fatalf("unknown artifact WI: %d %s", code, body)
	}

	e.srv.pending.mu.Lock()
	pending := len(e.srv.pending.items)
	e.srv.pending.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a refused query was enqueued: %d pending", pending)
	}
}
