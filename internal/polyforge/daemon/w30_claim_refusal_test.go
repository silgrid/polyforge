package daemon

// w30_claim_refusal_test.go — AC8 (polyforge#30), the daemon half: when
// aihub refuses a claim because the work item's project is deactivated
// (the typed CORE-403 PROJECT_DEACTIVATED the claim door answers), the
// auto-pulled entry is settled — never started, never requeued, no
// retry loop — and the refusal is recorded as the work item's
// submit_rejected event naming the typed error, so watchers and the
// decision feed surface it. This is the generic typed-refusal path; the
// pin keeps polyforge#30's answer durable against regressions in it.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// TestW30ClaimRefusalSettlesEntry: a PROJECT_DEACTIVATED claim refusal
// settles the auto-pulled entry, records submit_rejected with the typed
// error, and leaves the queue empty (no backoff: only a transport loss
// requeues).
func TestW30ClaimRefusalSettlesEntry(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour // the wave and its claim are driven by hand
		c.DispatchTick = time.Hour
	})
	const wi = "wi_w30_claim_refused"
	mkBare(t, td.cfg.GitRemoteBase, wi)
	bundle := "demo-bundle"
	td.hub.mu.Lock()
	td.hub.repoInventory = map[string][]string{}
	td.hub.claimErr = &api.Error{
		Code:   api.ErrCoreForbidden,
		Detail: "project proj is deactivated: it is read-only, restore it first (pf projects restore proj)",
		Extra:  []byte(`{"reason":"PROJECT_DEACTIVATED"}`),
	}
	td.hub.queues = append(td.hub.queues, api.WorkItem{
		ID: wi, ProjectName: "proj", Priority: api.PriorityNormal, BundleName: &bundle,
		Content: strp(w461AcceptanceContent),
	})
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}

	td.fetchAutoWave()
	e := td.q.Take()
	if e == nil {
		t.Fatal("the auto wave enqueued nothing — the fixture must propose the item first")
	}
	if e.Origin.Kind != runtime.OriginAuto {
		t.Fatalf("the enqueued entry's origin = %q, want auto", e.Origin.Kind)
	}
	td.startEntry(*e)

	// Settled: nothing pending, nothing requeued (only DAEMON-503
	// requeues), and the refusal is the durable submit_rejected event.
	if next := td.q.Take(); next != nil {
		t.Fatalf("the refused claim left an entry in the queue: %+v", next)
	}
	var detail string
	seen := false
	for _, ev := range td.obs.Events().SinceAfter(wi, 0) {
		if ev.Kind != "submit_rejected" {
			continue
		}
		seen = true
		var body struct {
			Error string `json:"error"`
			Code   string `json:"code"`
		}
		if err := json.Unmarshal(ev.Body, &body); err != nil {
			t.Fatalf("submit_rejected body: %v (%s)", err, ev.Body)
		}
		if body.Code != api.ErrCoreForbidden {
			t.Fatalf("submit_rejected carries code %q, want the typed CORE-403", body.Code)
		}
		if !strings.Contains(body.Error, "deactivated") {
			t.Fatalf("submit_rejected carries %q, want the deactivation refusal", body.Error)
		}
		detail = body.Error
	}
	if !seen {
		t.Fatalf("no submit_rejected event for %s — the refusal must be durable", wi)
	}
	if detail == "" {
		t.Fatal("the submit_rejected event carries no error text")
	}
}
