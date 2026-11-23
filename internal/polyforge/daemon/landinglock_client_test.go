package daemon

// carries: internal/polyforge/aihubclient/*, internal/aihub/server/*, internal/aihub/core/*

// landinglock_client_test.go: two independent daemon clients against one
// real AIHub server (polyforge#245 AC1/AC2's cross-client half, the
// decided Q1 B placement): same key, one holder and one queue; the
// queued client is granted only on its own poll after the holder
// releases, with a fresh token; lookup answers the holder and refuses
// another machine.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/aihub/server"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

func TestLandingLeaseTwoClients(t *testing.T) {
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

	machines := []string{"m-a", "m-b"}
	privs := make([]ed25519.PrivateKey, len(machines))
	for i, machine := range machines {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		privs[i] = priv
		if _, err := db.Exec(`INSERT INTO machines(id,public_key,operator_id,last_heartbeat_at,role)
			VALUES (?1,?2,?3,?4,'runner')`, machine,
			base64.StdEncoding.EncodeToString(pub), u.ID,
			time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seed machine %s: %v", machine, err)
		}
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
		clients[i] = aihubclient.New(srv.URL, issued.Plain)
		clients[i].SetMachineIdentity(machine, privs[i])
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

	const key = "github.com/o/app\nmain"
	held, err := clients[0].AcquireLease("landing", api.LeaseAcquire{Key: key, RequestID: "r-a", WorkItem: wis[0]})
	if err != nil || held.Status != "granted" || held.Token == "" {
		t.Fatalf("A acquire = %+v, %v", held, err)
	}
	queued, err := clients[1].AcquireLease("landing", api.LeaseAcquire{Key: key, RequestID: "r-b", WorkItem: wis[1]})
	if err != nil || queued.Status != "queued" || queued.Position != 1 {
		t.Fatalf("B acquire = %+v, %v, want queued 1", queued, err)
	}
	// The queue grants on the waiter's own poll, never on the release.
	if _, err := clients[0].ReleaseLease("landing", api.LeaseRelease{Key: key, RequestID: "r-a", WorkItem: wis[0], Token: held.Token}); err != nil {
		t.Fatalf("A release: %v", err)
	}
	granted, err := clients[1].AcquireLease("landing", api.LeaseAcquire{Key: key, RequestID: "r-b", WorkItem: wis[1]})
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
