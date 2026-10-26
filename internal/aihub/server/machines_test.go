package server

// machines_test.go: the M3 machines liveness read face over real HTTP —
// admin-only auth, the stale projection against the configured threshold,
// and the cannot-judge case (unparseable stamp reported, not guessed).

import (
	"encoding/json"
	"testing"
	"time"
)

type machinesResp struct {
	Machines []struct {
		ID                  string   `json:"id"`
		UserID              string   `json:"user_id"`
		RuntimeVersion      string   `json:"runtime_version"`
		Mode                string   `json:"mode"`
		LiveWIIDs           []string `json:"live_wi_ids"`
		LastHeartbeatAt     string   `json:"last_heartbeat_at"`
		HeartbeatAgeSeconds *int64   `json:"heartbeat_age_seconds"`
		Stale               bool     `json:"stale"`
	} `json:"machines"`
	StaleAfterSeconds int64 `json:"stale_after_seconds"`
	Count             int   `json:"count"`
}

func TestMachinesFaceOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	e.srv.StaleAfter = 2 * time.Second // test-sized window (production default 5m)

	// Two machines: one stale (backdated stamp), one fresh.
	e.do(t, "POST", "/api/v1/machines/heartbeat", e.key,
		map[string]any{"machine_id": "m-stale", "mode": "manual",
			"live_wi_ids": []string{"p#7"}, "runtime_version": "v1.5"}, nil)
	e.do(t, "POST", "/api/v1/machines/heartbeat", e.key,
		map[string]any{"machine_id": "m-fresh", "mode": "auto"}, nil)
	backdated := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := e.db.Exec(`UPDATE machines SET last_heartbeat_at=? WHERE id='m-stale'`, backdated); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	var out machinesResp
	code, _ := e.do(t, "GET", "/api/v1/machines", e.key, nil, &out)
	if code != 200 {
		t.Fatalf("GET machines: %d", code)
	}
	if out.Count != 2 || len(out.Machines) != 2 || out.StaleAfterSeconds != 2 {
		t.Fatalf("listing shape: %+v", out)
	}
	for i := range out.Machines {
		m := &out.Machines[i]
		switch m.ID {
		case "m-stale":
			if !m.Stale {
				t.Fatalf("m-stale must be stale: %+v", m)
			}
			if m.Mode != "manual" || m.RuntimeVersion != "v1.5" || len(m.LiveWIIDs) != 1 || m.LiveWIIDs[0] != "p#7" {
				t.Fatalf("m-stale projection: %+v", m)
			}
			if m.HeartbeatAgeSeconds == nil || *m.HeartbeatAgeSeconds < 3500 {
				t.Fatalf("m-stale age: %+v", m.HeartbeatAgeSeconds)
			}
			if _, err := time.Parse(time.RFC3339, m.LastHeartbeatAt); err != nil {
				t.Fatalf("m-stale stamp: %v", err)
			}
		case "m-fresh":
			if m.Stale {
				t.Fatalf("m-fresh must not be stale: %+v", m)
			}
			if m.Mode != "auto" {
				t.Fatalf("m-fresh mode: %+v", m)
			}
			if m.HeartbeatAgeSeconds == nil || *m.HeartbeatAgeSeconds > 60 {
				t.Fatalf("m-fresh age: %+v", m.HeartbeatAgeSeconds)
			}
		default:
			t.Fatalf("unexpected machine %q", m.ID)
		}
	}
}

func TestMachinesFaceAuth(t *testing.T) {
	e := newSrvEnv(t)
	// No key → 401.
	if code, _ := e.do(t, "GET", "/api/v1/machines", "", nil, nil); code != 401 {
		t.Fatalf("no key: %d", code)
	}
	// Writer (non-admin) → 403: the face exposes every machine's fleet view.
	u2, err := e.admin.CreateUser("wr", "wr@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	k2, err := e.admin.CreateAPIKey(u2.ID, "wr", "")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if code, _ := e.do(t, "GET", "/api/v1/machines", k2.Plain, nil, nil); code != 403 {
		t.Fatalf("writer key: %d", code)
	}
	// Admin → 200 with an empty (non-null) listing.
	if code, body := e.do(t, "GET", "/api/v1/machines", e.key, nil, nil); code != 200 {
		t.Fatalf("admin: %d %s", code, body)
	} else {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		if string(raw["machines"]) == "null" {
			t.Fatalf("machines must be [] not null: %s", body)
		}
	}
}

// TestHeartbeatFirstRegistrationRequiresAdmin pins the M3 folded finding:
// first registration (a fresh machine_id creating a machine user) is a
// privileged act. A non-admin writer api key must be refused with 403 and
// must not create anything; the admin key performs the first
// registration; and refresh of an EXISTING machine row stays open to any
// authenticated key (the refresh path is not a registration).
func TestHeartbeatFirstRegistrationRequiresAdmin(t *testing.T) {
	e := newSrvEnv(t)

	// A plain human writer with a global (unscoped) api key — authenticated,
	// but not admin.
	wr, err := e.admin.CreateUser("wr", "wr-hb@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("writer user: %v", err)
	}
	kwr, err := e.admin.CreateAPIKey(wr.ID, "wr-hb", "")
	if err != nil {
		t.Fatalf("writer key: %v", err)
	}

	// First registration under a FRESH machine_id with the writer key must
	// be refused: minting a machine identity is not any-key territory.
	code, body := e.do(t, "POST", "/api/v1/machines/heartbeat", kwr.Plain,
		map[string]any{"machine_id": "m-writer-attempt", "runtime_version": "v1.5"}, nil)
	if code != 403 {
		t.Fatalf("writer first registration must be refused with 403, got %d: %s", code, body)
	}

	// The refusal must not have created anything: no machines row, no
	// machine user.
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id='m-writer-attempt'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused registration must not create a machines row: n=%d err=%v", n, err)
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM users WHERE kind='machine' AND display_name LIKE '%m-writer-attempt%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused registration must not create a machine user: n=%d err=%v", n, err)
	}

	// The admin key performs the first registration (the privileged act);
	// no api key is issued, in the response or in the store.
	type hbResp struct {
		Registered bool            `json:"registered"`
		MachineID  string          `json:"machine_id"`
		UserID     string          `json:"user_id"`
		APIKey     json.RawMessage `json:"api_key"`
	}
	var admin1 hbResp
	code, body = e.do(t, "POST", "/api/v1/machines/heartbeat", e.key,
		map[string]any{"machine_id": "m-writer-attempt"}, &admin1)
	if code != 200 || !admin1.Registered || admin1.APIKey != nil {
		t.Fatalf("admin first registration: code=%d body=%s", code, body)
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=?1`, admin1.UserID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("registration must issue no api key: n=%d err=%v", n, err)
	}

	// Refresh of the existing row stays open to the writer key: no new
	// registration, same identity.
	var wrRefresh hbResp
	code, body = e.do(t, "POST", "/api/v1/machines/heartbeat", kwr.Plain,
		map[string]any{"machine_id": "m-writer-attempt"}, &wrRefresh)
	if code != 200 || wrRefresh.Registered || wrRefresh.APIKey != nil || wrRefresh.UserID != admin1.UserID {
		t.Fatalf("writer refresh of existing machine must stay open: code=%d body=%s", code, body)
	}
}

func TestMachinesFaceUnparseableStamp(t *testing.T) {
	e := newSrvEnv(t)
	if _, body := e.do(t, "POST", "/api/v1/machines/heartbeat", e.key,
		map[string]any{"machine_id": "m-weird"}, nil); body == nil {
	}
	if _, err := e.db.Exec(`UPDATE machines SET last_heartbeat_at='not-a-time' WHERE id='m-weird'`); err != nil {
		t.Fatalf("corrupt stamp: %v", err)
	}
	var out machinesResp
	if code, _ := e.do(t, "GET", "/api/v1/machines", e.key, nil, &out); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if len(out.Machines) != 1 {
		t.Fatalf("listing: %+v", out)
	}
	m := out.Machines[0]
	// Cannot-judge is not dead: no stale claim, no invented age.
	if m.Stale || m.HeartbeatAgeSeconds != nil {
		t.Fatalf("unparseable stamp must report stale=false, age omitted: %+v", m)
	}
}
