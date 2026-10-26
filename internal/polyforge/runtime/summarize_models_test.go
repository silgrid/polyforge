package runtime

// summarize_models_test.go: the polyforge#224 claim-time snapshot of the
// summarize lane's candidate list — the workspace-wide root
// summarize.models pick (polyforge#242) riding the same ONE-yaml-read compose as the
// step lists, carried by the job record (a restart rehydrates it) and
// projected by JobInfo (the daemon's watch route reads it as the lane's
// model source — the only read face).

import (
	"context"
	"reflect"
	"testing"
)

// The claim composes the pick from the workspace yaml (the same wcfg the
// step compose reads — one yaml read per claim), the job record carries
// it, and JobInfo projects it. An edit to the yaml after the claim
// governs the NEXT claim only: the running job's list is the claim-time
// snapshot, never a re-derivation. The yaml names no project at all:
// the root list governs a claim of ANY project of the workspace.
func TestClaimSnapshotsSummarizeModels(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w224/a, claude:w224/b] }\n")
	if err := r.Start(context.Background(), "wi_w224sum", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ji, ok := r.Info("wi_w224sum")
	if !ok {
		t.Fatal("the claim must run to a live job")
	}
	if want := []string{"pi:w224/a", "claude:w224/b"}; !reflect.DeepEqual(ji.SummarizeModels, want) {
		t.Fatalf("claim-time SummarizeModels = %v, want %v", ji.SummarizeModels, want)
	}
	// polyforge#243: no summarize.language declared → the claim resolves 中文
	if ji.SummarizeLanguage != "中文" {
		t.Fatalf("claim-time SummarizeLanguage = %q, want 中文", ji.SummarizeLanguage)
	}
	// polyforge#250: no summarize.interval_sec declared → the claim resolves 60
	if ji.SummarizeIntervalSec != 60 {
		t.Fatalf("claim-time SummarizeIntervalSec = %d, want 60", ji.SummarizeIntervalSec)
	}

	// The mid-flight edit governs the next claim only: the running job's
	// list stays the claim-time snapshot.
	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w224/edited] }\n")
	ji2, _ := r.Info("wi_w224sum")
	if want := []string{"pi:w224/a", "claude:w224/b"}; !reflect.DeepEqual(ji2.SummarizeModels, want) {
		t.Fatalf("an edited yaml reached the running job's summarize lane: %v", ji2.SummarizeModels)
	}
}

// A workspace whose yaml declares no summarize key claims fine with the
// lane silently off: the pick answers nil, never an error — "off" is a
// legitimate state (polyforge#224, never the old lane's typed failure).
func TestClaimWithoutSummarizeKeyIsSilentlyOff(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    default: { models: [pi:w79/code-a] }\n")
	if err := r.Start(context.Background(), "wi_w224off", StartOpts{}); err != nil {
		t.Fatalf("Start under a keyless yaml: %v", err)
	}
	ji, ok := r.Info("wi_w224off")
	if !ok {
		t.Fatal("the claim must run to a live job")
	}
	if ji.SummarizeModels != nil {
		t.Fatalf("a keyless claim carries a summarize list: %v", ji.SummarizeModels)
	}
}

// The record roundtrip carries the list; a record written before the
// field existed (no summarize_models) loads nil — the lane is off, never
// an error (field-compatible both ways).
func TestRecordRoundtripSummarizeModels(t *testing.T) {
	j := &job{
		wi:              "wi_w224rec",
		project:         "proj",
		sessionID:       "s",
		state:           StateRunning,
		summarizeModels: []string{"pi:w224/a"},
		stepCandidates:  map[string][]string{"fix": {"pi:w79/code-a"}},
	}
	rec := j.record()
	if !reflect.DeepEqual(rec.SummarizeModels, []string{"pi:w224/a"}) {
		t.Fatalf("record carries %v", rec.SummarizeModels)
	}
	back := jobFromRecord(rec)
	if !reflect.DeepEqual(back.summarizeModels, []string{"pi:w224/a"}) {
		t.Fatalf("roundtrip lost the list: %v", back.summarizeModels)
	}
	// an old record without the field loads nil (the lane off)
	old := *rec
	old.SummarizeModels = nil
	legacy := jobFromRecord(&old)
	if legacy.summarizeModels != nil {
		t.Fatalf("an old record loads a summarize list: %v", legacy.summarizeModels)
	}
}

// polyforge#243: the declared summarize.language is snapshotted at claim
// beside the models list; an edit governs the next claim only.
func TestClaimSnapshotsSummarizeLanguage(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w243/a], language: English }\n")
	if err := r.Start(context.Background(), "wi_w243lang", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ji, ok := r.Info("wi_w243lang")
	if !ok || ji.SummarizeLanguage != "English" {
		t.Fatalf("claim-time SummarizeLanguage = %q (live %v), want English", ji.SummarizeLanguage, ok)
	}
	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w243/a], language: Français }\n")
	if ji2, _ := r.Info("wi_w243lang"); ji2.SummarizeLanguage != "English" {
		t.Fatalf("an edited yaml reached the running job's summarize language: %q", ji2.SummarizeLanguage)
	}
}

// polyforge#243: the record carries summarize_language; a record without
// the field reads as 中文.
func TestRecordRoundtripSummarizeLanguage(t *testing.T) {
	j := &job{wi: "wi_w243rec", project: "proj", sessionID: "s", state: StateRunning, summarizeLanguage: "English"}
	rec := j.record()
	if rec.SummarizeLanguage != "English" {
		t.Fatalf("record carries %q", rec.SummarizeLanguage)
	}
	if back := jobFromRecord(rec); back.summarizeLanguage != "English" {
		t.Fatalf("roundtrip lost the language: %q", back.summarizeLanguage)
	}
	old := *rec
	old.SummarizeLanguage = ""
	if legacy := jobFromRecord(&old); legacy.summarizeLanguage != "中文" {
		t.Fatalf("a record without the field reads %q, want 中文", legacy.summarizeLanguage)
	}
}

// polyforge#250: the declared summarize.interval_sec is snapshotted at
// claim beside the models list; an edit governs the next claim only.
func TestClaimSnapshotsSummarizeInterval(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w250/a], interval_sec: 120 }\n")
	if err := r.Start(context.Background(), "wi_w250int", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ji, ok := r.Info("wi_w250int")
	if !ok || ji.SummarizeIntervalSec != 120 {
		t.Fatalf("claim-time SummarizeIntervalSec = %d (live %v), want 120", ji.SummarizeIntervalSec, ok)
	}
	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w250/a], interval_sec: 10 }\n")
	if ji2, _ := r.Info("wi_w250int"); ji2.SummarizeIntervalSec != 120 {
		t.Fatalf("an edited yaml reached the running job's summarize interval: %d", ji2.SummarizeIntervalSec)
	}
}

// polyforge#250: the record carries summarize_interval_sec; a record
// without the field reads as 60.
func TestRecordRoundtripSummarizeInterval(t *testing.T) {
	j := &job{wi: "wi_w250rec", project: "proj", sessionID: "s", state: StateRunning, summarizeIntervalSec: 300}
	rec := j.record()
	if rec.SummarizeIntervalSec != 300 {
		t.Fatalf("record carries %d", rec.SummarizeIntervalSec)
	}
	if back := jobFromRecord(rec); back.summarizeIntervalSec != 300 {
		t.Fatalf("roundtrip lost the interval: %d", back.summarizeIntervalSec)
	}
	old := *rec
	old.SummarizeIntervalSec = 0
	if legacy := jobFromRecord(&old); legacy.summarizeIntervalSec != 60 {
		t.Fatalf("a record without the field reads %d, want 60", legacy.summarizeIntervalSec)
	}
}
