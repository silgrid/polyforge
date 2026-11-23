package aihubclient

// landinglock_test.go: the daemon client half of the lease base
// (polyforge#245 Q6/Q9). The four calls sign their request with the
// machine identity (the same canonical string the server verifies) and
// decode the shared grant; the request journal is 0600, atomic, holds no
// token, and a fresh journal instance reads back the cycles a restarted
// daemon still owns.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
)

func TestLeaseClientRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const machine = "m-lease"
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		ts, _ := strconv.ParseInt(r.Header.Get(machinesign.HeaderTimestamp), 10, 64)
		if !machinesign.Verify(pub, time.Now(), r.Method, r.URL.Path, body, ts,
			r.Header.Get(machinesign.HeaderMachineID), r.Header.Get(machinesign.HeaderSignature), nil) {
			http.Error(w, "unsigned", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k-lease" {
			http.Error(w, "wrong bearer "+got, http.StatusUnauthorized)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		req := struct {
			Key       string `json:"key"`
			RequestID string `json:"request_id"`
			WorkItem  string `json:"work_item"`
			Token     string `json:"token"`
		}{}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/leases/requests/") {
			req.RequestID = strings.TrimPrefix(r.URL.Path, "/api/v1/leases/requests/")
		}
		g := api.LeaseGrant{Kind: "landing", Key: req.Key, RequestID: req.RequestID, Status: "granted"}
		switch {
		case strings.HasSuffix(r.URL.Path, "/acquire"):
			g.Token = "tok"
			g.ExpiresAt = "2026-11-23T12:05:00Z"
		case strings.HasSuffix(r.URL.Path, "/renew"):
			if req.Token != "tok" {
				http.Error(w, "renew token = "+req.Token, http.StatusBadRequest)
				return
			}
		case strings.HasSuffix(r.URL.Path, "/release"):
			g.Status = "released"
		}
		_ = json.NewEncoder(w).Encode(g)
	}))
	defer srv.Close()

	c := New(srv.URL, "k-lease")
	c.SetMachineIdentity(machine, priv)
	g, err := c.AcquireLease("landing", api.LeaseAcquire{Key: "k", RequestID: "r1", WorkItem: "alpha#1"})
	if err != nil || g.Token != "tok" || g.RequestID != "r1" {
		t.Fatalf("acquire = %+v, %v", g, err)
	}
	if g, err = c.RenewLease("landing", api.LeaseRenew{Key: "k", RequestID: "r1", WorkItem: "alpha#1", Token: g.Token}); err != nil || g.Status != "granted" {
		t.Fatalf("renew = %+v, %v", g, err)
	}
	if g, err = c.ReleaseLease("landing", api.LeaseRelease{Key: "k", RequestID: "r1", WorkItem: "alpha#1", Token: "tok"}); err != nil || g.Status != "released" {
		t.Fatalf("release = %+v, %v", g, err)
	}
	if _, err = c.LookupLease("r1", "alpha#1"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	want := []string{
		"POST /api/v1/leases/landing/acquire",
		"POST /api/v1/leases/landing/renew",
		"POST /api/v1/leases/landing/release",
		"GET /api/v1/leases/requests/r1?work_item=alpha%231",
	}
	if len(seen) != len(want) {
		t.Fatalf("requests = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestLeaseJournalRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "lease-journal.json")
	j := NewLeaseJournal(path)
	entries := []LeaseJournalEntry{
		{RequestID: "r1", Kind: "landing", Key: "github.com/o/app\nmain", WorkItem: "alpha#1", Cycle: "cycle-1"},
		{RequestID: "r2", Kind: "landing", Key: "github.com/o/other\nmain", WorkItem: "alpha#2", Cycle: "cycle-2"},
	}
	for _, e := range entries {
		if err := j.Add(e); err != nil {
			t.Fatalf("add %s: %v", e.RequestID, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("journal mode = %o, want 0600", mode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("the journal must never carry a token: %s", raw)
	}

	// A restarted daemon reads the same cycles back.
	restarted := NewLeaseJournal(path)
	got, err := restarted.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(got) != 2 || got[0] != entries[0] || got[1] != entries[1] {
		t.Fatalf("entries after restart = %+v, want %+v", got, entries)
	}
	// Adding the same request id replaces its entry (idempotent); removing
	// one keeps the other; removing an unknown id is a no-op.
	if err := restarted.Add(LeaseJournalEntry{RequestID: "r1", Kind: "landing", Key: "github.com/o/app\nmain", WorkItem: "alpha#1", Cycle: "cycle-1b"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if got, _ = restarted.Entries(); len(got) != 2 || got[0].Cycle != "cycle-1b" {
		t.Fatalf("re-add = %+v, want r1 updated in place", got)
	}
	if err := restarted.Remove("r1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ = restarted.Entries(); len(got) != 1 || got[0].RequestID != "r2" {
		t.Fatalf("after remove = %+v, want only r2", got)
	}
	if err := restarted.Remove("nope"); err != nil {
		t.Fatalf("remove unknown: %v", err)
	}
}
