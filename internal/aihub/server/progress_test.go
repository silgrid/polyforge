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
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

	// A caller with no project read is concealed: the
	// same missing-work-item answer an unknown reference gets, before
	// anything is enqueued.
	code, body := signedPost(t, e, ok.Plain, "m-outsider", priv,
		"/api/v1/work-items/"+wi+"/progress-query", api.ProgressQuery{WorkItem: wi})
	missing, mbody := signedPost(t, e, ok.Plain, "m-outsider", priv,
		"/api/v1/work-items/wi_missing/progress-query", api.ProgressQuery{WorkItem: "wi_missing"})
	if code != http.StatusNotFound || errCodeOf(body) != api.ErrCoreNotFound || code != missing ||
		strings.ReplaceAll(string(body), wi, "<ref>") != strings.ReplaceAll(string(mbody), "wi_missing", "<ref>") {
		t.Fatalf("a caller with no project read must get the missing-work-item answer before routing: %d %s vs %d %s", code, body, missing, mbody)
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

// relayHolder registers machineID as a fresh holder that advertises the
// relay capabilities, and answers every query addressed to it straight
// from the registry (no signed answer, so the holder's own lanes stay
// out of the admission test). It answers the count of queries it served.
func relayHolder(t *testing.T, e *srvEnv, machineID string) func() int {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	capability := api.FullCapability()
	if _, err := e.admin.HeartbeatAuthorized(core.MachineHeartbeat{
		MachineID: machineID, PublicKey: pubB64, Launcher: "pi-web", Role: "runner",
		Capability: &capability,
	}, core.HeartbeatProof{CallerID: e.adminID, Signed: true, KeyB64: pubB64}, nil); err != nil {
		t.Fatalf("register holder %s: %v", machineID, err)
	}
	done := make(chan struct{})
	served := make(chan int, 1)
	go func() {
		n := 0
		for {
			wake := e.srv.pending.waitCh()
			for _, q := range e.srv.pending.take(machineID) {
				if p := e.srv.pending.get(q.ID); p != nil {
					e.srv.pending.remove(q.ID)
					p.deliver(&queryResult{Body: &api.QueryAnswerBody{HolderMachine: machineID}})
					n++
				}
			}
			select {
			case <-done:
				served <- n
				return
			case <-wake:
			}
		}
	}()
	var once sync.Once
	stop := func() int {
		once.Do(func() { close(done) })
		return <-served
	}
	t.Cleanup(func() { once.Do(func() { close(done) }) })
	return stop
}

// TestRelayQueriesAreBoundedAtEnqueue pins A2 over HTTP with
// the shipped quotas: one member's flood of relayed queries is refused at
// enqueue with CORE-429 naming the per-member bound once it reaches
// relayMemberQuota, the refused query never reaches the holder, another
// member is still served, and the holder as a whole stops at
// relayHolderQuota whoever asks.
func TestRelayQueriesAreBoundedAtEnqueue(t *testing.T) {
	e := newSrvEnv(t)
	stop := relayHolder(t, e, "m-relay-holder")
	wi := newRunningWI(t, e, "m-relay-holder")
	path := "/api/v1/work-items/" + wi + "/progress-query"
	type requester struct {
		key, machine string
		priv         ed25519.PrivateKey
	}
	member := func(name string) requester {
		userID, key := memberUser(t, e, name, "viewer")
		priv, _ := signedMachineFor(t, e, "m-"+name, userID)
		return requester{key: key, machine: "m-" + name, priv: priv}
	}
	// A distinct (ignored) nonce per query keeps signatures sent within
	// one second distinct, as a daemon's 2 s cadence does.
	nonce := 0
	ask := func(r requester) (int, []byte) {
		nonce++
		return signedPost(t, e, r.key, r.machine, r.priv, path, map[string]any{"work_item": wi, "nonce": nonce})
	}
	refusedNamed := func(who string, code int, body []byte, bound string) {
		t.Helper()
		var ae api.Error
		_ = json.Unmarshal(body, &ae)
		if code != http.StatusTooManyRequests || ae.Code != api.ErrCoreTooMany ||
			!strings.Contains(ae.Detail, "relay refused") || !strings.Contains(ae.Detail, bound) {
			t.Fatalf("%s: %d %s, want 429 %s naming %q", who, code, body, api.ErrCoreTooMany, bound)
		}
	}

	a := member("relay-a")
	for i := 0; i < relayMemberQuota; i++ {
		if code, body := ask(a); code != http.StatusOK {
			t.Fatalf("member A query %d = %d %s, want 200", i+1, code, body)
		}
	}
	code, body := ask(a)
	refusedNamed("member A over its quota", code, body, fmt.Sprintf("at most %d per member per holder", relayMemberQuota))

	b := member("relay-b")
	for i := 0; i < relayHolderQuota-relayMemberQuota; i++ {
		if code, body := ask(b); code != http.StatusOK {
			t.Fatalf("member B query %d (holder budget left) = %d %s, want 200", i+1, code, body)
		}
	}
	code, body = ask(b)
	refusedNamed("member B at the holder's quota", code, body, fmt.Sprintf("at most %d per holder", relayHolderQuota))
	code, body = ask(member("relay-c"))
	refusedNamed("a fresh member at the holder's quota", code, body, fmt.Sprintf("at most %d per holder", relayHolderQuota))

	if served := stop(); served != relayHolderQuota {
		t.Fatalf("the holder served %d queries, want exactly the %d admitted (a refused query reached it)", served, relayHolderQuota)
	}
	e.srv.pending.mu.Lock()
	pending := len(e.srv.pending.items)
	e.srv.pending.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a refused query was enqueued: %d pending", pending)
	}
}

// TestRelayBudgetWindowAndMemory pins the budget's clock (
// A2): a refused admission records nothing, every admission leaves after
// the window so both bounds recover by themselves, and a key whose
// admissions all expired is dropped by any later admit.
func TestRelayBudgetWindowAndMemory(t *testing.T) {
	b := newRelayBudget(3, 2, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		if err := b.admit(now, "u1", "h"); err != nil {
			t.Fatalf("u1 admission %d: %v", i+1, err)
		}
	}
	if err := b.admit(now, "u1", "h"); err == nil || !strings.Contains(err.Error(), "per member per holder") {
		t.Fatalf("u1 over its member quota = %v, want the member refusal", err)
	}
	if err := b.admit(now, "u1", "other-holder"); err != nil {
		t.Fatalf("the member quota is per holder: %v", err)
	}
	if err := b.admit(now, "u2", "h"); err != nil {
		t.Fatalf("u2 within the holder quota: %v", err)
	}
	err := b.admit(now.Add(10*time.Second), "u3", "h")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreTooMany || !strings.Contains(ae.Detail, "per holder") ||
		!strings.Contains(ae.Detail, "retry within 51 s") {
		t.Fatalf("u3 at the holder quota = %v, want CORE-429 per holder, retry within 51 s", err)
	}
	b.mu.Lock()
	held := len(b.byHolder["h"])
	b.mu.Unlock()
	if held != 3 {
		t.Fatalf("refused admissions were recorded: holder h holds %d, want 3", held)
	}
	later := now.Add(time.Minute + time.Second)
	if err := b.admit(later, "u1", "h"); err != nil {
		t.Fatalf("the budget did not recover after the window: %v", err)
	}
	b.mu.Lock()
	_, otherKept := b.byHolder["other-holder"]
	_, memberKept := b.byMember[relayMember{user: "u2", holder: "h"}]
	b.mu.Unlock()
	if otherKept || memberKept {
		t.Fatalf("fully expired keys survived a later admit: holder %v member %v", otherKept, memberKept)
	}
}

// TestRelayBudgetFitsTheHolderLanes pins the derivation of the shipped
// constants ( A2): relay-driven entries can fill neither the
// holder's answer lane nor, with the two loops' idle polls, its inbox
// lane, and no single member holds the whole holder budget.
func TestRelayBudgetFitsTheHolderLanes(t *testing.T) {
	if relayWindow < machinesign.ReplayTTL+progressQueryDeadline {
		t.Fatalf("relayWindow %s is shorter than the replay TTL plus the answer deadline", relayWindow)
	}
	idlePolls := 2 * int((relayWindow+inboxLongPoll-1)/inboxLongPoll)
	if relayHolderQuota > machinesign.ReplayQuota || relayHolderQuota+idlePolls > machinesign.ReplayQuota {
		t.Fatalf("relayHolderQuota %d (+%d idle polls) does not fit the %d-entry lane", relayHolderQuota, idlePolls, machinesign.ReplayQuota)
	}
	if relayMemberQuota >= relayHolderQuota {
		t.Fatalf("relayMemberQuota %d lets one member hold the whole holder budget %d", relayMemberQuota, relayHolderQuota)
	}
}
