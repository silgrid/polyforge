package verifyrun

// selection_report_test.go: the engine's full-tier e2e selection of an
// opted-in repository (smoke_suite=) is decoded, recorded on the repo
// report, rendered in report-mechanical.md, and — when the selection ran a
// subset of the full tail — the full tail replaces the selected e2e
// command(s) in the test bar carried to delivery.

import (
	"reflect"
	"strings"
	"testing"
)

// selReport is a full report carrying an e2e selection.
func selReport(sel map[string]any, cmds ...EngineCommand) map[string]any {
	r := report("full", cmds...)
	r["smoke_suite"] = []string{"e2e/smoke.mjs"}
	r["e2e_selection"] = sel
	return r
}

func TestSelectionCarriesTheFullBar(t *testing.T) {
	for _, mode := range []string{SelectionCarries, SelectionSmoke} {
		t.Run(mode, func(t *testing.T) {
			f := touched(t)
			eng := fakeEngine(t, map[string]any{"app": selReport(map[string]any{
				"mode": mode, "suites": []string{"e2e/panel.mjs", "e2e/smoke.mjs"},
				"uncovered_paths": []string{"scripts/ops.sh"}, "full_commands": []string{"echo FULL-E2E"},
			}, cmdOf("fast-1", "fast", "echo FAST"), cmdOf("full-1", "full", "echo SUBSET e2e/panel.mjs e2e/smoke.mjs"))})
			res := runFixture(t, f, eng, nil)
			// verification ran the selection, never the full tail
			var ran []string
			for _, c := range res.Repos[0].Commands {
				ran = append(ran, c.Command)
			}
			if want := []string{"echo FAST", "echo SUBSET e2e/panel.mjs e2e/smoke.mjs"}; !reflect.DeepEqual(ran, want) {
				t.Fatalf("ran %v, want %v", ran, want)
			}
			want := []TestBarEntry{{"app", "echo FAST"}, {"app", "echo FULL-E2E"}}
			if !reflect.DeepEqual(res.TestBar, want) {
				t.Fatalf("test_bar = %+v, want the fast gate plus the carried full command %+v", res.TestBar, want)
			}
			if len(res.TestBarNotes) != 1 || !strings.Contains(res.TestBarNotes[0], "carried for delivery") {
				t.Fatalf("test_bar_notes = %v", res.TestBarNotes)
			}
			sel := res.Repos[0].E2ESelection
			if sel == nil || sel.Mode != mode || !reflect.DeepEqual(sel.UncoveredPaths, []string{"scripts/ops.sh"}) {
				t.Fatalf("repo e2e_selection = %+v", sel)
			}
			md := renderReport(res)
			for _, s := range []string{"e2e selection (smoke_suite opt-in): mode **" + mode + "**",
				"`e2e/panel.mjs`, `e2e/smoke.mjs`", "uncovered paths `scripts/ops.sh`", "carried full command(s) `echo FULL-E2E`",
				"### Test bar (carried to delivery)"} {
				if !strings.Contains(md, s) {
					t.Fatalf("report-mechanical.md lacks %q:\n%s", s, md)
				}
			}
		})
	}
}

// TestSelectionFallbackAndNonOptedKeepTheBarAsRun: a selection that fell
// back to the full tail, a non-opted repo and a scoped run keep the test
// bar as run.
func TestSelectionFallbackAndNonOptedKeepTheBarAsRun(t *testing.T) {
	f := touched(t)
	full := selReport(map[string]any{"mode": SelectionFull, "suites": []string{}, "uncovered_paths": []string{},
		"full_commands": []string{"echo FULL-E2E"}}, cmdOf("fast-1", "fast", "echo FAST"), cmdOf("full-1", "full", "echo FULL-E2E"))
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": full}), nil)
	if want := []TestBarEntry{{"app", "echo FAST"}, {"app", "echo FULL-E2E"}}; !reflect.DeepEqual(res.TestBar, want) || len(res.TestBarNotes) != 0 {
		t.Fatalf("fallback test_bar = %+v notes %v", res.TestBar, res.TestBarNotes)
	}
	if s := res.Repos[0].E2ESelection; s == nil || s.Mode != SelectionFull {
		t.Fatalf("the fallback is still recorded: %+v", s)
	}

	plain := report("full", cmdOf("fast-1", "fast", "echo FAST"), cmdOf("full-1", "full", "echo FULL-E2E"))
	res = runFixture(t, f, fakeEngine(t, map[string]any{"app": plain}), nil)
	if want := []TestBarEntry{{"app", "echo FAST"}, {"app", "echo FULL-E2E"}}; !reflect.DeepEqual(res.TestBar, want) || res.Repos[0].E2ESelection != nil {
		t.Fatalf("non-opted test_bar = %+v selection %+v", res.TestBar, res.Repos[0].E2ESelection)
	}

	scoped := report("scoped", cmdOf("fast-1", "fast", "echo FAST"), cmdOf("scoped-1", "scoped", "echo S"))
	scoped["e2e_selection"] = nil
	scoped["smoke_suite"] = []string{"e2e/smoke.mjs"}
	res = runFixture(t, f, fakeEngine(t, map[string]any{"app": scoped}), func(o *Options) { o.Tier = TierScoped })
	if want := []TestBarEntry{{"app", "echo FAST"}, {"app", "echo S"}}; !reflect.DeepEqual(res.TestBar, want) {
		t.Fatalf("scoped test_bar = %+v", res.TestBar)
	}
}

// TestSelectionNoDuplicateFullCommand: a full command that already ran is
// not carried twice.
func TestSelectionNoDuplicateFullCommand(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": selReport(map[string]any{
		"mode": SelectionCarries, "suites": []string{"e2e/smoke.mjs"}, "uncovered_paths": []string{},
		"full_commands": []string{"echo FAST", "echo FULL-E2E"},
	}, cmdOf("fast-1", "fast", "echo FAST"), cmdOf("full-1", "full", "echo SUBSET e2e/smoke.mjs"))})
	res := runFixture(t, f, eng, nil)
	if want := []TestBarEntry{{"app", "echo FAST"}, {"app", "echo FULL-E2E"}}; !reflect.DeepEqual(res.TestBar, want) {
		t.Fatalf("test_bar = %+v, want %+v", res.TestBar, want)
	}
}

// TestSelectionModeValidated: an unknown selection mode is an unreadable
// report.
func TestSelectionModeValidated(t *testing.T) {
	raw := `{"schema":1,"mode":"full","commands":[],"e2e_selection":{"mode":"bogus"}}`
	if _, err := decodeReport([]byte(raw)); err == nil || !strings.Contains(err.Error(), "e2e_selection mode") {
		t.Fatalf("decodeReport = %v", err)
	}
}
