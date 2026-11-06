package daemon

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// TestHubFailureDegradesOnlyItsCellAndStopsItsClaims: a Hub refusing a
// Cell's credential degrades that Cell (relayed unchanged, the reason on
// /profiles), a degraded Cell's automatic claims stop, the other Cell
// stays serving, and a successful round trip clears the mark.
func TestHubFailureDegradesOnlyItsCellAndStopsItsClaims(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	refuse := true
	var hits atomic.Int64
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if refuse {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"AUTH-401"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer hub.Close()
	alpha := th.cell("alpha").d
	alpha.hub = aihubclient.New(hub.URL, "revoked")
	call := func() int {
		req, _ := http.NewRequest("GET", "http://daemon/aihub/api/v1/projects", nil)
		req.Header.Set("X-PF-Profile", "alpha")
		resp, err := th.client("").HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}
	if code := call(); code != http.StatusUnauthorized {
		t.Fatalf("the Hub's refusal must be relayed unchanged: %d", code)
	}
	if why := alpha.degradedReason(); !strings.Contains(why, "credential") {
		t.Fatalf("a refused credential must degrade the Cell: %q", why)
	}
	if th.cell("default").d.degradedReason() != "" {
		t.Fatal("another Cell was degraded")
	}
	resp, err := th.client("").Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Profiles {
		if p.Profile == "alpha" && (p.State != "degraded" || p.Reason == "") {
			t.Fatalf("alpha must read degraded with its reason: %+v", p)
		}
		if p.Profile == "default" && p.State == "degraded" {
			t.Fatalf("default must not read degraded: %+v", p)
		}
	}
	refuse = false
	if code := call(); code != http.StatusOK || alpha.degradedReason() != "" {
		t.Fatalf("a successful round trip must clear the mark: %d %q", code, alpha.degradedReason())
	}
	alpha.noteHubFailure(errors.New("dial: refused"))
	if err := alpha.modes.SetAuto(true); err != nil {
		t.Fatal(err)
	}
	before := hits.Load()
	alpha.fetchAutoWave() // auto on, but degraded: no ready-queue fetch at all
	if hits.Load() != before || alpha.q.LenPending() != 0 {
		t.Fatalf("a degraded Cell pulled automatic work (%d Hub calls)", hits.Load()-before)
	}
}

// TestOnlyConnectionFailuresDegrade: an unreachable Hub (transport error,
// 5xx) or a refused credential (401) degrades the Cell; a typed refusal of
// one request (a machine-binding 403, a 409) does not.
func TestOnlyConnectionFailuresDegrade(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{errors.New("dial tcp: connection refused"), true},
		{&api.Error{Code: api.ErrAuthUnauthorized, Detail: "bad key"}, true},
		{&api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "503"}, true},
		{&api.Error{Code: api.ErrAuthForbidden, Detail: "machine bound to another key"}, false},
		{&api.Error{Code: api.ErrDaemonConflict, Detail: "conflict"}, false},
	} {
		if got := hubFailure(c.err); got != c.want {
			t.Errorf("hubFailure(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
