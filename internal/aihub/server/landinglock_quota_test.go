package server

// carries: internal/aihub/server/*, internal/aihub/core/*, internal/common/api/lease.go

// landinglock_quota_test.go: the lease face's anti-replay quota isolation
// (polyforge#245 AC3, decided Q12). Five waiting tickets and one holder:
// each ticket polls in its own (machine, lane, ticket) bucket, so a
// waiting ticket that spends its whole poll quota cannot refuse the
// holder's renew (its own lane and bucket) or another ticket's poll.
// Exactly the 512th distinct signature of a bucket is accepted; the 513th
// is CORE-429.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/machinesign"
)

func TestLeaseQuotaIsolation(t *testing.T) {
	e := newSrvEnv(t)
	const key = "github.com/o/app\nmain"
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-lease", pub, priv)

	wis := make([]string, 6)
	for i := range wis {
		wis[i] = createExecWIOverHTTP(t, e, fmt.Sprintf("quota ticket %d", i))
		leaseClaim(t, e, wis[i], "m-lease", fmt.Sprintf("%032d", i), e.key)
	}
	now := time.Now().Unix()

	// 1 holder + 5 waiting tickets, all on one machine.
	code, holder, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, now,
		leaseAcquireBody(key, "r-hold", wis[0]))
	if code != http.StatusOK || holder.Status != "granted" || holder.Token == "" {
		t.Fatalf("holder = %d %+v %s", code, holder, raw)
	}
	for i := 1; i <= 5; i++ {
		c, g, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, now+250+int64(i),
			leaseAcquireBody(key, fmt.Sprintf("r-%d", i), wis[i]))
		if c != http.StatusOK || g.Status != "queued" {
			t.Fatalf("waiter %d = %d %+v %s", i, c, g, raw)
		}
	}

	// Fill waiting ticket 1's own poll bucket: its setup poll is one live
	// entry, and 511 more distinct signatures bring it to the quota.
	for i := 0; i < 511; i++ {
		c, _, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv,
			now-300+int64(i), leaseAcquireBody(key, "r-1", wis[1]))
		if c != http.StatusOK {
			t.Fatalf("poll %d of the bucket = %d %s", i+1, c, raw)
		}
	}
	// That bucket now holds exactly its quota; the next signature is over
	// it. The quota is the shared 512-entry one (machinesign.ReplayQuota).
	if machinesign.ReplayQuota != 512 {
		t.Fatalf("the quota fixture assumes the shared 512-entry bucket, got %d", machinesign.ReplayQuota)
	}
	if c, _, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv,
		now+211, leaseAcquireBody(key, "r-1", wis[1])); c != http.StatusTooManyRequests {
		t.Fatalf("513th signature of one ticket's bucket = %d %s, want CORE-429", c, raw)
	}
	// The holder's renew rides the manage lane and its own ticket bucket.
	if c, g, raw := leasePost(t, e, leaseKindPath+"/renew", e.key, "m-lease", priv, 0,
		map[string]any{"key": key, "request_id": "r-hold", "work_item": wis[0], "token": holder.Token}); c != http.StatusOK || g.Status != "granted" {
		t.Fatalf("holder renew after a waiter's bucket filled = %d %+v %s", c, g, raw)
	}
	// Another waiting ticket's poll is its own bucket, unaffected.
	if c, g, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, now+211,
		leaseAcquireBody(key, "r-2", wis[2])); c != http.StatusOK || g.Status != "queued" {
		t.Fatalf("another ticket's poll after a full bucket = %d %+v %s", c, g, raw)
	}
	// A waiter's withdrawal is its own call and still works from a full
	// poll bucket (release rides the manage lane).
	if c, g, raw := leasePost(t, e, leaseKindPath+"/release", e.key, "m-lease", priv, 0,
		map[string]any{"key": key, "request_id": "r-1", "work_item": wis[1]}); c != http.StatusOK || g.Status != "released" {
		t.Fatalf("withdraw from a full poll bucket = %d %+v %s", c, g, raw)
	}
}
