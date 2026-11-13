package runtime

// c4_wrap_attrs_test.go (workflow contract 4, design #483 §5.2/§5.3,
//): the wrapped close carries every `wrap-attrs` write the
// pending-writes executor made (develop's and micro's landing
// `integration`, an integration batch's `integration_batch`) and, for
// every lane, `waivers`: each owner waiver of the run with the owner's
// words verbatim from the ask record, the agent's reading and the times.
// A step's own `waivers` write is dropped; a waiver whose cited message is
// gone is an error (the close must not wrap without it).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// ledgerWrites records a done hand-off of (step, spawn) with wrap-attrs
// writes name → value, executed when made.
func ledgerWrites(t *testing.T, dir, step string, spawn int64, made bool, writes ...[2]string) {
	t.Helper()
	var ops []stepcall.Op
	for i, w := range writes {
		ops = append(ops, stepcall.Op{ID: step + "." + string(rune('a'+i)), Verb: stepcall.HubWrapAttrs, Name: w[0], Value: json.RawMessage(w[1])})
	}
	if _, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindResult, Step: step, Spawn: spawn, Verb: stepcall.VerbHandoff,
		Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: stepcall.StatusDone}, Pending: ops}); err != nil {
		t.Fatal(err)
	}
	if !made {
		return
	}
	for _, op := range ops {
		if _, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindExec, Step: step, Spawn: spawn, Op: op.ID, Outcome: stepcall.ExecDone}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWrapAttrsCarryTheMadeWrapAttrs(t *testing.T) {
	dir := t.TempDir()
	landing := `{"state":"landed","repos":[{"repo":"app","pr":"https://github.com/o/r/pull/7","head":"abc","merge_commit":"m1"}],"merge_commits":{"app":["m1"]}}`
	ledgerWrites(t, dir, "merge", 7, true, [2]string{"integration", landing})
	ledgerWrites(t, dir, "close", 9, true, [2]string{"integration_batch", `{"result":"green"}`})
	// a dispatch whose writes were never made (dropped by a fail) carries nothing
	ledgerWrites(t, dir, "other", 4, false, [2]string{"dropped", `true`})
	// execution.json is the daemon's progress record, never read for the
	// hand-off: a contract 3 landing left in it rides nothing
	p := filepath.Join(dir, capsule.ExecutionJSON)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"workflow_status":"completed","integration":{"state":"landed","repos":[],"merge_commits":{"old":["x"]}},"integration_batch":{"result":"red"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := wrapAttrs(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	got := string(raw)
	if !strings.Contains(got, `"integration":`+landing) || !strings.Contains(got, `"integration_batch":{"result":"green"}`) || strings.Contains(got, `"red"`) ||
		strings.Contains(got, "dropped") || strings.Contains(got, `"old"`) || strings.Contains(got, "waivers") {
		t.Fatalf("wrap attrs %s, want the two made writes verbatim, no unmade write, nothing of execution.json, no waivers", got)
	}
}

func TestWrapAttrsWriteTheWaiversVerbatim(t *testing.T) {
	dir := t.TempDir()
	const words = "放行：这个 flake 是已知问题 (known), ship it"
	if err := capsule.AppendAsk(dir, capsule.AskLine{At: "2026-11-11T12:00:00Z", Step: "verification", Spawn: 5, Kind: capsule.AskKindMessage, Text: words, By: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stepcall.Append(dir, stepcall.Line{At: "2026-11-11T12:05:00Z", Kind: stepcall.KindResult, Step: "verification", Round: 2, Spawn: 5,
		Verb: stepcall.VerbHandoff, Outcome: stepcall.OutcomeOK,
		Handoff: &stepcall.Handoff{Status: stepcall.StatusWaive, PerOwner: "2026-11-11T12:00:00Z", Reason: "the owner accepts the known flake"}}); err != nil {
		t.Fatal(err)
	}
	// a step cannot write the waivers itself
	ledgerWrites(t, dir, "publish", 6, true, [2]string{"waivers", `["forged"]`})
	a, err := wrapAttrs(dir)
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := a["waivers"].([]stepcall.Waiver)
	if !ok || len(ws) != 1 {
		t.Fatalf("waivers %#v, want the one waiver of the run", a["waivers"])
	}
	want := stepcall.Waiver{Gate: "verification", Round: 2, SpawnN: 5, OwnerWords: words, OwnerBy: "alice",
		OwnerMessageAt: "2026-11-11T12:00:00Z", AgentReason: "the owner accepts the known flake", WaivedAt: "2026-11-11T12:05:00Z"}
	if ws[0] != want {
		t.Fatalf("waiver %+v, want %+v", ws[0], want)
	}
	// no daemon waiver: the step's write is still dropped
	dir2 := t.TempDir()
	ledgerWrites(t, dir2, "publish", 6, true, [2]string{"waivers", `["forged"]`})
	if a, err := wrapAttrs(dir2); err != nil || a != nil {
		t.Fatalf("a step's waivers write alone: %v %v, want no attrs", a, err)
	}
	// the cited message gone: never wrapped without the owner's words
	dir3 := t.TempDir()
	if _, err := stepcall.Append(dir3, stepcall.Line{Kind: stepcall.KindResult, Step: "verification", Spawn: 5, Verb: stepcall.VerbHandoff,
		Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: stepcall.StatusWaive, PerOwner: "2026-11-11T12:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	if a, err := wrapAttrs(dir3); err == nil {
		t.Fatalf("a waiver whose words are gone wrapped: %v", a)
	}
}
