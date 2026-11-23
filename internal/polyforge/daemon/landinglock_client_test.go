package daemon

// carries: internal/polyforge/aihubclient/*, internal/aihub/server/*, internal/aihub/core/*

// landinglock_client_test.go: independent daemon clients against one real
// AIHub server (polyforge#245 AC1/AC2's cross-client half, the decided Q1 B
// placement). One scenario releases and lets the waiter poll; the other
// starts every client on one barrier, so AC1's "simultaneous acquire"
// half is pinned: exactly one holder, the rest queued with distinct
// positions and no error. The repository key is the server's normalized
// landing key (已定 Q2, review r1#2).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/aihub/server"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

const (
	landingRepo   = "git@github.com:o/app.git"
	landingBranch = "main"
)

// newLandingHub runs a real AIHub server with one admin, project alpha
// declaring one repository, one signed daemon client per machine and one
// claimed ticket per client.
func newLandingHub(t *testing.T, machines []string) ([]*aihubclient.Client, []string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	admin := core.NewAdminManager(db)
	u, err := admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("admin user: %v", err)
	}
	issued, err := admin.CreateAPIKey(u.ID, "lease", "")
	if err != nil {
		t.Fatalf("admin key: %v", err)
	}
	if _, err := admin.CreateProject(core.CreateProjectRequest{Name: "alpha"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	if _, err := db.Exec(`UPDATE projects SET repos=?1 WHERE name='alpha'`,
		`[{"name":"app","url":"git@github.com:o/app.git"}]`); err != nil {
		t.Fatalf("declare project repos: %v", err)
	}
	mgr, err := git.NewManager(dataDir, core.NewOwnerManager(db))
	if err != nil {
		t.Fatalf("git manager: %v", err)
	}
	srv := httptest.NewServer(server.New(db, dataDir, mgr))
	t.Cleanup(srv.Close)

	clients := make([]*aihubclient.Client, len(machines))
	wis := make([]string, len(machines))
	sessions := core.NewSessionManager(db)
	wisMgr := core.NewWIManager(db)
	for i, machine := range machines {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO machines(id,public_key,operator_id,last_heartbeat_at,role)
			VALUES (?1,?2,?3,?4,'runner')`, machine,
			base64.StdEncoding.EncodeToString(pub), u.ID,
			time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seed machine %s: %v", machine, err)
		}
		clients[i] = aihubclient.New(srv.URL, issued.Plain)
		clients[i].SetMachineIdentity(machine, priv)
		bundle := "demo-bundle"
		wi, err := wisMgr.Create(core.CreateRequest{Project: "alpha", Goal: fmt.Sprintf("lease ticket %d", i),
			Reporter: u.ID, BundleName: &bundle})
		if err != nil {
			t.Fatalf("create WI %d: %v", i, err)
		}
		wis[i] = wi.ID
		if _, err := sessions.Claim(wi.ID, api.SessionClaimRequest{
			SessionID: fmt.Sprintf("%032d", i), MachineID: machine}, u.ID); err != nil {
			t.Fatalf("claim WI %d on %s: %v", i, machine, err)
		}
	}
	return clients, wis
}

func TestLandingLeaseTwoClients(t *testing.T) {
	clients, wis := newLandingHub(t, []string{"m-a", "m-b"})
	held, err := clients[0].AcquireLease("landing", api.LeaseAcquire{
		Repo: landingRepo, Branch: landingBranch, RequestID: "r-a", WorkItem: wis[0]})
	if err != nil || held.Status != "granted" || held.Token == "" {
		t.Fatalf("A acquire = %+v, %v", held, err)
	}
	queued, err := clients[1].AcquireLease("landing", api.LeaseAcquire{
		Repo: landingRepo, Branch: landingBranch, RequestID: "r-b", WorkItem: wis[1]})
	if err != nil || queued.Status != "queued" || queued.Position != 1 {
		t.Fatalf("B acquire = %+v, %v, want queued 1", queued, err)
	}
	// The queue grants on the waiter's own poll, never on the release.
	if _, err := clients[0].ReleaseLease("landing", api.LeaseRelease{
		Key: held.Key, RequestID: "r-a", WorkItem: wis[0], Token: held.Token}); err != nil {
		t.Fatalf("A release: %v", err)
	}
	granted, err := clients[1].AcquireLease("landing", api.LeaseAcquire{
		Repo: landingRepo, Branch: landingBranch, RequestID: "r-b", WorkItem: wis[1]})
	if err != nil || granted.Status != "granted" || granted.Token == "" || granted.Token == held.Token {
		t.Fatalf("B poll = %+v, %v, want granted with a fresh token", granted, err)
	}
	if got, err := clients[1].LookupLease("r-b", wis[1]); err != nil || got.Status != "held" || got.Token != "" {
		t.Fatalf("B lookup = %+v, %v, want held with no token", got, err)
	}
	if _, err := clients[0].LookupLease("r-b", wis[0]); err == nil {
		t.Fatal("another machine looked up B's grant")
	}
}

// TestLandingLeaseConcurrentAcquire pins AC1's simultaneous half across
// independent daemon clients: every client starts on one barrier for the
// same repository and branch. Exactly one is granted, every other is
// queued, the positions are distinct 1..n-1 and no call errors — a
// read-then-write split would produce an error (the primary key refuses
// the second holder row) instead of a queue entry.
func TestLandingLeaseConcurrentAcquire(t *testing.T) {
	machines := []string{"m-0", "m-1", "m-2", "m-3"}
	clients, wis := newLandingHub(t, machines)
	const n = 4
	start := make(chan struct{})
	grants := make([]*api.LeaseGrant, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			grants[i], errs[i] = clients[i].AcquireLease("landing", api.LeaseAcquire{
				Repo: landingRepo, Branch: landingBranch,
				RequestID: fmt.Sprintf("c-%d", i), WorkItem: wis[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	granted, queued := 0, 0
	positions := map[int]bool{}
	for i, g := range grants {
		if errs[i] != nil {
			t.Fatalf("client %d errored: %v", i, errs[i])
		}
		switch g.Status {
		case "granted":
			granted++
			if g.Token == "" {
				t.Fatalf("client %d granted without a token", i)
			}
		case "queued":
			queued++
			positions[g.Position] = true
		default:
			t.Fatalf("client %d = %+v, want granted or queued", i, g)
		}
	}
	if granted != 1 || queued != n-1 {
		t.Fatalf("grants=%d queues=%d, want exactly 1 granted and %d queued", granted, queued, n-1)
	}
	for p := 1; p < n; p++ {
		if !positions[p] {
			t.Fatalf("queue positions = %v, want the distinct set 1..%d", positions, n-1)
		}
	}
}
