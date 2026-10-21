package daemon

// resume_paused_route_test.go: the live-holder resume door's own
// semantics (wi_seWTgHeKtaa8, v2-converge §7), pinned at the route seam.
// The B6 repro (resume_b6_repro_test.go) drives the real `pf resume`
// end to end; these pin the refusal branches the operator can hit:
//   - an unknown WI answers DAEMON-404 (this daemon is not the holder);
//   - a clean_pause flag the operator has NOT cleared answers DAEMON-409
//     and resumes nothing (an operator pause is never auto-resumed);
//   - a missing mid-flight capsule answers BLOCKED at aihub (the typed
//     blockUnrecoverable path, never the "reattaches at next start"
//     copy) and the typed refusal names the remediation;
//   - a repeated resume over a record already running again answers
//     already_live without a second attach.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// pauseForResume parks a live job through the REAL pause route and waits
// for the retained paused record.
func pauseForResume(t *testing.T, td *testDaemon, wi string) {
	t.Helper()
	td.submitClaim(t, wi)
	if _, err := td.client.Pause(wi); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitState(t, td, wi, "paused")
}

// clearHubCleanPause plays the operator's `pf resume` hub-side effect on
// the fake hub (the clean_pause=false PATCH).
func clearHubCleanPause(td *testDaemon) {
	td.hub.mu.Lock()
	td.hub.cleanPauseCleared = true
	td.hub.mu.Unlock()
}

// TestResumePausedRouteUnknownWI: no record on this daemon answers
// DAEMON-404 — the CLI's not-the-holder guidance branch.
func TestResumePausedRouteUnknownWI(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	_, err := td.client.ResumePaused("wi_not_here")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonNotFound {
		t.Fatalf("unknown WI must answer DAEMON-404, got %v", err)
	}
}

// TestResumePausedRouteFlagNotCleared: the operator's resume PATCH has
// not landed (clean_pause still set at the hub): the door refuses — an
// operator pause is never auto-resumed — and nothing is attached or
// dispatched.
func TestResumePausedRouteFlagNotCleared(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	pauseForResume(t, td, "wi_flag")
	spawns := td.spawner.callCount()
	attaches := attachCount(td.hub)
	_, err := td.client.ResumePaused("wi_flag")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("a still-set clean_pause must answer DAEMON-409, got %v", err)
	}
	if attachCount(td.hub) != attaches {
		t.Fatalf("nothing may be attached while clean_pause is set")
	}
	if td.spawner.callCount() != spawns {
		t.Fatalf("nothing may be dispatched while clean_pause is set")
	}
	waitState(t, td, "wi_flag", "paused")
}

// TestResumePausedRouteMissingCapsuleBlocks: a retained paused record
// whose capsule directory is gone takes the typed mid-flight block path
// (SessionEnd blocked naming the cause at aihub, nothing parked locally)
// and the route's refusal detail names the remediation — never the
// restart copy.
func TestResumePausedRouteMissingCapsuleBlocks(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	pauseForResume(t, td, "wi_gone")
	clearHubCleanPause(td)
	if err := os.RemoveAll(filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", "wi_gone")); err != nil {
		t.Fatal(err)
	}
	_, err := td.client.ResumePaused("wi_gone")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("a missing capsule must answer the typed refusal, got %v", err)
	}
	if want := "capsule missing at"; !strings.Contains(ae.Detail, want) {
		t.Fatalf("the refusal must name the typed cause, got %q", ae.Detail)
	}
	// blocked at aihub, nothing stays parked locally.
	waitFor(t, "blocked verdict at the hub", 5*time.Second, func() bool {
		return td.hub.lastEnd().Status == api.StatusBlocked
	})
	waitFor(t, "record dropped", 5*time.Second, func() bool {
		_, live := td.rt.Info("wi_gone")
		return !live
	})
}

// TestResumePausedRouteIdempotentWhileRunning: the first resume answers
// attached (the in-place path itself is the B6 repro's); a second resume
// over the record now running again answers already_live and does not
// attach a second time.
func TestResumePausedRouteIdempotentWhileRunning(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	pauseForResume(t, td, "wi_twice")
	clearHubCleanPause(td)
	if res, err := td.client.ResumePaused("wi_twice"); err != nil || res.Outcome != "attached" {
		t.Fatalf("first resume: %+v, %v (want the attached outcome)", res, err)
	}
	waitState(t, td, "wi_twice", "running")
	res, err := td.client.ResumePaused("wi_twice")
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	if res.Outcome != "already_live" {
		t.Fatalf("second resume outcome = %q, want already_live", res.Outcome)
	}
	if got := attachCount(td.hub); got != 1 {
		t.Fatalf("session-attach calls = %d, want one (no second attach)", got)
	}
}

// TestResumePausedRouteAttachTransportIs503: a SessionAttach that never
// reaches the hub answers DAEMON-503 (hub unreachable, nothing resumed)
// like the verify step does — not DAEMON-500 — and the record stays
// paused.
func TestResumePausedRouteAttachTransportIs503(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	pauseForResume(t, td, "wi_net")
	clearHubCleanPause(td)
	td.hub.mu.Lock()
	td.hub.attachErr = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	td.hub.mu.Unlock()
	_, err := td.client.ResumePaused("wi_net")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonHubUnavailable {
		t.Fatalf("an unreachable hub at attach must answer DAEMON-503, got %v", err)
	}
	waitState(t, td, "wi_net", "paused")
}

// TestResumePausedRouteClosedByRecovery: when the synchronous recovery
// continuation closes the job (here: the capsule already carries the
// completed verdict, so the recovery wraps it), the door answers the
// closed outcome, never "attached" (which the CLI prints as "continues
// in place").
func TestResumePausedRouteClosedByRecovery(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
	})
	pauseForResume(t, td, "wi_done")
	clearHubCleanPause(td)
	execPath := filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", "wi_done", capsule.ExecutionJSON)
	doc := map[string]any{}
	if raw, err := os.ReadFile(execPath); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	doc["workflow_status"] = capsule.WorkflowStatusCompleted
	raw, _ := json.Marshal(doc)
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := td.client.ResumePaused("wi_done")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Outcome != "closed" || res.Resumed {
		t.Fatalf("resume over a job the recovery closed = %+v, want the closed outcome", res)
	}
}
