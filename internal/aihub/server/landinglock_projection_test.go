package server

// carries: internal/aihub/server/*, internal/aihub/core/lease.go, internal/common/api/landing_lock.go

// landinglock_projection_test.go: the read-only landing-lock projection on
// the work-item read paths (polyforge#245 epic 3/3, Q11). One holder and
// five live waiters share one key; each waiting ticket sees its own 1-based
// position, the holder and other tickets never appear, and GET leaves the
// lease tables untouched.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func landingCounts(t *testing.T, e *srvEnv) (int, int, int) {
	t.Helper()
	var waiters, seqs, holders int
	if err := e.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(seq),0) FROM lease_waiters`).Scan(&waiters, &seqs); err != nil {
		t.Fatalf("count waiters: %v", err)
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&holders); err != nil {
		t.Fatalf("count holders: %v", err)
	}
	return waiters, seqs, holders
}

func TestLandingLockListGetReadOnly(t *testing.T) {
	e := newSrvEnv(t)
	const key = "github.com/o/app\ntrunk"
	now := time.Now().UnixNano()
	holderWI := createExecWIOverHTTP(t, e, "holder")
	if _, err := e.db.Exec(`INSERT INTO leases(
		kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns)
		VALUES ('landing',?1,'r-hold','m-holder',?2,'op','hash-holder',?3,?4)`,
		key, holderWI, now, now+int64(300*time.Second)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}
	waiting := make([]string, 0, 6)
	for i := 0; i < 5; i++ {
		wi := createExecWIOverHTTP(t, e, "waiter")
		waiting = append(waiting, wi)
		if _, err := e.db.Exec(`INSERT INTO lease_waiters(
			request_id,kind,key,machine_id,work_item,operator_id,requested_at_ns,last_poll_at_ns)
			VALUES (?1,'landing',?2,'m-w',?3,'op',?4,?5)`,
			"req-"+wi, key, wi, now+int64(i)*int64(time.Second), now); err != nil {
			t.Fatalf("seed waiter %d: %v", i, err)
		}
	}
	staleWI := createExecWIOverHTTP(t, e, "stale waiter")
	if _, err := e.db.Exec(`INSERT INTO lease_waiters(
		request_id,kind,key,machine_id,work_item,operator_id,requested_at_ns,last_poll_at_ns)
		VALUES (?1,'landing',?2,'m-w',?3,'op',?4,?5)`,
		"req-"+staleWI, key, staleWI, now-int64(time.Minute), now-int64(61*time.Second)); err != nil {
		t.Fatalf("seed stale waiter: %v", err)
	}

	beforeW, beforeS, beforeH := landingCounts(t, e)
	var list struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items?project=alpha", e.key, nil, &list); code != http.StatusOK {
		t.Fatalf("list: %d %s", code, raw)
	}
	if len(list.Items) != 7 {
		t.Fatalf("list returned %d items, want 7", len(list.Items))
	}
	positions := map[string]int{}
	for _, row := range list.Items {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			t.Fatalf("item id: %v", err)
		}
		raw := row["landing_lock"]
		if len(raw) == 0 {
			continue
		}
		var ll struct {
			State string `json:"state"`
			Keys  []struct {
				Repo     string `json:"repo"`
				Branch   string `json:"branch"`
				Position int    `json:"position"`
				Since    string `json:"since"`
			} `json:"keys"`
		}
		if err := json.Unmarshal(raw, &ll); err != nil {
			t.Fatalf("decode projection: %v", err)
		}
		if strings.Contains(string(raw), "hash-holder") || strings.Contains(string(raw), "m-holder") ||
			strings.Contains(string(raw), "req-") || strings.Contains(string(raw), holderWI) {
			t.Fatalf("projection leaks holder/request data: %s", raw)
		}
		if id == holderWI {
			if ll.State != "none" {
				t.Fatalf("the holder is not waiting: %s", raw)
			}
			continue
		}
		if id == staleWI {
			if ll.State != "none" || len(ll.Keys) != 0 {
				t.Fatalf("an expired waiter is not a position: %s", raw)
			}
			continue
		}
		if ll.State != "waiting" || len(ll.Keys) != 1 || ll.Keys[0].Repo != "github.com/o/app" || ll.Keys[0].Branch != "trunk" {
			t.Fatalf("%s projection = %s", id, raw)
		}
		positions[id] = ll.Keys[0].Position
	}
	for i, wi := range waiting {
		if got, want := positions[wi], i+1; got != want {
			t.Fatalf("%s position = %d, want %d (the holder is not a position)", wi, got, want)
		}
	}
	if len(positions) != len(waiting) {
		t.Fatalf("positions = %v, want the five waiters", positions)
	}

	var one map[string]json.RawMessage
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+waiting[2], e.key, nil, &one); code != http.StatusOK {
		t.Fatalf("get: %d %s", code, raw)
	}
	var ll struct {
		State string `json:"state"`
		Keys  []struct {
			Position int `json:"position"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(one["landing_lock"], &ll); err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if ll.State != "waiting" || len(ll.Keys) != 1 || ll.Keys[0].Position != 3 {
		t.Fatalf("get projection = %s", one["landing_lock"])
	}

	afterW, afterS, afterH := landingCounts(t, e)
	if afterW != beforeW || afterS != beforeS || afterH != beforeH {
		t.Fatalf("GET changed the lease store: before %d/%d/%d after %d/%d/%d",
			beforeW, beforeS, beforeH, afterW, afterS, afterH)
	}
}
