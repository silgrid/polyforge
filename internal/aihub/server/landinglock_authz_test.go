package server

// carries: internal/aihub/server/*, internal/aihub/core/*, internal/common/api/lease.go

// landinglock_authz_test.go: the signed lease face's authorization
// matrix (polyforge#245 AC3, decided Q10). The four refusals the ticket
// names — an unrelated machine, a machine that lost the ticket's session
// (force-ended or reclaimed), and a step/task direct call (Bearer only) —
// plus expired and replayed signatures, and the split between withdrawing
// a queued request and releasing the holder.

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

const leaseKindPath = "/api/v1/leases/landing"

// leasePost performs one signed lease call and decodes the grant.
func leasePost(t *testing.T, e *srvEnv, path, key, machine string, priv ed25519.PrivateKey, ts int64, body any) (int, *api.LeaseGrant, []byte) {
	t.Helper()
	code, raw := e.doSigned(t, "POST", path, key, machine, priv, ts, body)
	var g api.LeaseGrant
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("decode grant: %v (%s)", err, raw)
		}
	}
	return code, &g, raw
}

// leaseClaim claims one ticket on one machine.
func leaseClaim(t *testing.T, e *srvEnv, wi, machine, secret, key string) {
	t.Helper()
	var out api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi+"/claim", key,
		map[string]any{"session_id": secret, "machine_id": machine}, &out); code != http.StatusOK {
		t.Fatalf("claim %s on %s: %d %s", wi, machine, code, raw)
	}
}

// leaseAcquireBody names one request.
func leaseAcquireBody(key, requestID, wi string) map[string]any {
	return map[string]any{"key": key, "request_id": requestID, "work_item": wi}
}

// TestLeaseAuthz pins the signed lease face's authorization.
func TestLeaseAuthz(t *testing.T) {
	e := newSrvEnv(t)
	const key = "github.com/o/app\nmain"

	pubLease, privLease := machineKey(t)
	e.registerSigned(t, e.key, "m-lease", pubLease, privLease)
	pubOther, privOther := machineKey(t)
	_, otherKey := memberUser(t, e, "lease-other", "writer")
	e.registerSigned(t, otherKey, "m-other", pubOther, privOther)

	wiA := createExecWIOverHTTP(t, e, "lease holder A")
	leaseClaim(t, e, wiA, "m-lease", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", e.key)

	// ① the machine's operator, the project write and the current session
	// all hold: the acquire is accepted and granted.
	code, held, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease, 0,
		leaseAcquireBody(key, "r-hold", wiA))
	if code != http.StatusOK || held.Status != "granted" || held.Token == "" {
		t.Fatalf("authorized acquire: %d %s", code, raw)
	}

	// ④ step/task direct: a Bearer without the machine signature never
	// reaches the route.
	if code, _ := e.do(t, "POST", leaseKindPath+"/acquire", e.key, leaseAcquireBody(key, "r-bare", wiA), nil); code != http.StatusUnauthorized {
		t.Fatalf("Bearer-only acquire = %d, want AUTH-401", code)
	}

	// Expired signature and a verbatim replay are AUTH-401.
	if code, _, _ := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease,
		time.Now().Unix()-301, leaseAcquireBody(key, "r-expired", wiA)); code != http.StatusUnauthorized {
		t.Fatalf("expired signature = %d, want AUTH-401", code)
	}
	replayTS := time.Now().Unix()
	first, _, _ := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease,
		replayTS, leaseAcquireBody(key, "r-replay", wiA))
	if first != http.StatusOK {
		t.Fatalf("replay fixture = %d", first)
	}
	if code, _, _ := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease,
		replayTS, leaseAcquireBody(key, "r-replay", wiA)); code != http.StatusUnauthorized {
		t.Fatalf("replayed signature = %d, want AUTH-401", code)
	}
	// The replay's own first call queued a request; withdraw it so the
	// withdrawal case below starts from an empty queue.
	if code, _, raw := leasePost(t, e, leaseKindPath+"/release", e.key, "m-lease", privLease, 0,
		map[string]any{"key": key, "request_id": "r-replay", "work_item": wiA}); code != http.StatusOK {
		t.Fatalf("withdraw the replay request: %d %s", code, raw)
	}

	// ② an unrelated machine: a different operator cannot speak for the
	// holder's machine, and a machine whose operator is a project writer
	// still does not hold the ticket's session.
	if code, _, _ := leasePost(t, e, leaseKindPath+"/acquire", otherKey, "m-lease", privLease, 0,
		leaseAcquireBody(key, "r-wrong-op", wiA)); code != http.StatusForbidden {
		t.Fatalf("foreign operator = %d, want AUTH-403", code)
	}
	if code, _, raw := leasePost(t, e, leaseKindPath+"/acquire", otherKey, "m-other", privOther, 0,
		leaseAcquireBody(key, "r-foreign", wiA)); code != http.StatusForbidden || string(raw) == "" {
		t.Fatalf("unrelated machine = %d %s, want AUTH-403", code, raw)
	} else {
		var envl api.Error
		_ = json.Unmarshal(raw, &envl)
		var extra struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(envl.Extra, &extra)
		if extra.Reason != "LEASE_SESSION_NOT_HELD" {
			t.Fatalf("unrelated machine reason = %q, want LEASE_SESSION_NOT_HELD (%s)", extra.Reason, raw)
		}
	}

	// Withdrawing a queued request needs no token; releasing the holder
	// does. One ticket's queue row and the holder are separate checks.
	wiB := createExecWIOverHTTP(t, e, "lease waiter B")
	leaseClaim(t, e, wiB, "m-lease", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", e.key)
	code, queued, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease, 0,
		leaseAcquireBody(key, "r-wait", wiB))
	if code != http.StatusOK || queued.Status != "queued" || queued.Position != 1 {
		t.Fatalf("waiter = %d %+v %s", code, queued, raw)
	}
	if code, rel, raw := leasePost(t, e, leaseKindPath+"/release", e.key, "m-lease", privLease, 0,
		map[string]any{"key": key, "request_id": "r-wait", "work_item": wiB}); code != http.StatusOK || rel.Status != "released" {
		t.Fatalf("withdraw = %d %+v %s", code, rel, raw)
	}
	if code, _, raw := leasePost(t, e, leaseKindPath+"/renew", e.key, "m-lease", privLease, 0,
		map[string]any{"key": key, "request_id": "r-hold", "work_item": wiA, "token": held.Token}); code != http.StatusOK {
		t.Fatalf("holder renew after a withdrawal = %d %s", code, raw)
	}
	if code, _, _ := leasePost(t, e, leaseKindPath+"/release", e.key, "m-lease", privLease, 0,
		map[string]any{"key": key, "request_id": "r-hold", "work_item": wiA, "token": "00"}); code != http.StatusForbidden {
		t.Fatalf("stale token release = %d, want AUTH-403", code)
	}

	// ③ lost execution: a force-ended session refuses the old machine.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wiA+"/force-end-session", e.key, map[string]any{"reason": "lease test"}, nil); code != http.StatusOK {
		t.Fatalf("force-end: %d %s", code, raw)
	}
	if code, _, _ := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease,
		time.Now().Unix()+120, leaseAcquireBody(key, "r-hold", wiA)); code != http.StatusForbidden {
		t.Fatalf("force-ended session = %d, want AUTH-403", code)
	}

	// ③ reclaimed by another machine: the old machine is refused, the new
	// holder proceeds; the old request id cannot release the holder.
	leaseClaim(t, e, wiA, "m-other", "cccccccccccccccccccccccccccccccc", otherKey)
	if code, _, _ := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", privLease, 0,
		leaseAcquireBody(key, "r-hold2", wiA)); code != http.StatusForbidden {
		t.Fatalf("reclaimed session (old machine) = %d, want AUTH-403", code)
	}
	if code, queued, raw := leasePost(t, e, leaseKindPath+"/acquire", otherKey, "m-other", privOther, 0,
		leaseAcquireBody(key, "r-new", wiA)); code != http.StatusOK {
		t.Fatalf("reclaimed session (new machine) = %d %s", code, raw)
	} else if queued.Status != "queued" && queued.Status != "granted" {
		t.Fatalf("new machine acquire = %+v, want queued or granted", queued)
	}
	if code, _, raw := leasePost(t, e, leaseKindPath+"/release", otherKey, "m-other", privOther, 0,
		map[string]any{"key": key, "request_id": "r-hold", "work_item": wiA}); code != http.StatusForbidden {
		t.Fatalf("release another cycle's holder by request id = %d %s, want AUTH-403", code, raw)
	}
}
