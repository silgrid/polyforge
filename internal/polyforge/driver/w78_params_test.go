package driver

// w78_params_test.go (polyforge#78 AC4): a step's params take their final
// values at every dispatch. The driver composes them per dispatch beside
// the candidate lists (Config.Params; a failing composition blocks the
// workflow like a failing candidate composition) and records them on the
// step_started event; the spawn adapter renders the definition's
// {{name}} placeholders with them, rewrites workflows/_rendered/<step>.md
// with them, hands them to the derivation (spawn.Params.Params, the
// PF_PARAM_<NAME> environment) and records them on the spawn-journal
// line; a resume of the same conversation whose params changed since the
// dispatch it resumes states the new values in its first message, in
// every resume shape.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// Every dispatch composes the params afresh: a value that changes between
// two dispatches is what the next dispatch carries — on its spawn order
// and on its step_started event. A failing composition blocks the
// workflow before anything is spawned; without a composition the lane
// defaults govern.
func TestW78ParamsRideStepStart(t *testing.T) {
	steps := []StepSpec{
		{ID: "a", Params: map[string]string{"image": "lane-a"}},
		{ID: "b", Params: map[string]string{"image": "lane-b", "flag": ""}},
		{ID: "c"},
	}
	newDriver := func(t *testing.T, params func() (map[string]map[string]string, error)) (*Driver, *fakeSpawner, *eventLog, string) {
		t.Helper()
		dir := t.TempDir()
		execPath := filepath.Join(dir, capsule.ExecutionJSON)
		sp := &fakeSpawner{}
		log := &eventLog{}
		d, err := New(Config{
			WI:       WIInfo{ID: "wi_w78", CapsuleDir: dir, BundleName: "b"},
			ExecPath: execPath,
			Workflow: testLane(2, steps...),
			Spawner:  sp,
			Log:      log,
			Params:   params,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d, sp, log, execPath
	}

	// the composition answers what the overlay holds at each call: the
	// ticket value changes after the first dispatch
	calls := 0
	compose := func() (map[string]map[string]string, error) {
		calls++
		return map[string]map[string]string{
			"a": {"image": "ticket-" + strconv.Itoa(calls)},
			"b": {"image": "ticket-" + strconv.Itoa(calls), "flag": "on"},
		}, nil
	}
	d, sp, log, _ := newDriver(t, compose)
	sp.script = []fakeEntry{done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 3 {
		t.Fatalf("the params composed %d times over three dispatches, want once per dispatch", calls)
	}
	want := []map[string]string{
		{"image": "ticket-1"},
		{"image": "ticket-2", "flag": "on"},
		nil, // c declares no params
	}
	for i, w := range want {
		if got := sp.spawnAt(i).Params; !reflect.DeepEqual(got, w) && (len(got) != 0 || len(w) != 0) {
			t.Errorf("dispatch %d (%s) carries params %v, want %v", i+1, sp.spawnAt(i).StepID, got, w)
		}
	}
	started := log.bodies("step_started")
	if len(started) != 3 {
		t.Fatalf("%d step_started events, want 3: %v", len(started), log.kinds())
	}
	for i, w := range want {
		got, has := started[i]["params"]
		if w == nil {
			if has {
				t.Errorf("step_started of %v carries params %v for a step that declares none", started[i]["step"], got)
			}
			continue
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("step_started of %v carries params %v, want the dispatch's final values %v", started[i]["step"], got, w)
		}
		// the event body only grows: the fields it always had stay
		if started[i]["step"] != steps[i].ID || started[i]["spawn_n"] == nil {
			t.Errorf("step_started lost its existing fields: %v", started[i])
		}
	}

	// a failing composition blocks the workflow: nothing is spawned
	d, sp, log, execPath := newDriver(t, func() (map[string]map[string]string, error) {
		return nil, errors.New("DAEMON-414: broken yaml")
	})
	sp.script = []fakeEntry{done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 0 {
		t.Fatalf("a failing param composition dispatched %v", got)
	}
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusBlocked {
		t.Fatalf("a failing param composition leaves the workflow %q, want blocked", exec.WorkflowStatus)
	}
	if b := log.bodies("blocked"); len(b) != 1 || !strings.Contains(b[0]["reason"].(string), "DAEMON-414") {
		t.Fatalf("the blocked event must name the composition's failure: %v", b)
	}

	// no composition: the lane defaults govern
	d, sp, _, _ = newDriver(t, nil)
	sp.script = []fakeEntry{done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.spawnAt(1).Params; !reflect.DeepEqual(got, map[string]string{"image": "lane-b", "flag": ""}) {
		t.Fatalf("without a composition b carries %v, want its lane defaults", got)
	}
}

// w78Lane is a probe lane whose step declares two params, its definition
// using both.
const w78Lane = `
name: w78
description: probe lane of the param dispatch tests
steps:
  - id: probe
    params: { image: lane-img, flag: "" }
`

const w78Body = "Run the probe against {{image}} (flag `{{flag}}`)."

// w78SpawnEnv lays out a capsule with the w78 lane, its claim-time
// rendered definition (lane defaults) and a spawn adapter over it.
func w78SpawnEnv(t *testing.T) (*StepSpawnAdapter, *captureSpawner, string) {
	t.Helper()
	capDir := t.TempDir()
	writeCapsuleFile(t, capDir, "workflows/w78/"+workflow.WorkflowFile,
		"contract: "+strconv.Itoa(workflow.ContractVersion)+w78Lane)
	writeStepHeader(t, filepath.Join(capDir, "workflows"))
	writeCapsuleFile(t, capDir, "workflows/w78/steps/probe.md", extDef("probe", w78Body))
	// the claim wrote the rendered definition with the lane defaults, read-only
	rendered := filepath.Join(capDir, "workflows", "_rendered", "probe.md")
	writeCapsuleFile(t, capDir, "workflows/_rendered/probe.md", extDef("probe", "Run the probe against lane-img (flag ``)."))
	if err := os.Chmod(rendered, 0o444); err != nil {
		t.Fatal(err)
	}
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:      WIInfo{ID: "wi_w78", CapsuleDir: capDir, BundleName: "w78", SessionHandle: "ab12cd34"},
		Spawner: sp,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	return ad, sp, capDir
}

// The dispatch's final values fill the definition's placeholders, rewrite
// the rendered definition, ride the derivation and land on its journal
// line — afresh at every dispatch.
func TestW78SpawnParamsPrompt(t *testing.T) {
	ad, sp, capDir := w78SpawnEnv(t)
	rendered := filepath.Join(capDir, "workflows", "_rendered", "probe.md")
	for i, tc := range []struct {
		params map[string]string
		line   string
	}{
		{map[string]string{"image": "reg/img:2", "flag": "on"}, "Run the probe against reg/img:2 (flag `on`)."},
		{map[string]string{"image": "reg/img:3", "flag": ""}, "Run the probe against reg/img:3 (flag ``)."},
	} {
		n := int64(i + 1)
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "probe", WIID: "wi_w78", SpawnN: n, Harness: harness.Pi, Params: tc.params}); err != nil {
			t.Fatalf("dispatch %d: SpawnStep with params %v: %v", n, tc.params, err)
		}
		p, _ := sp.last()
		if !strings.Contains(p.InitialPrompt, tc.line) {
			t.Fatalf("dispatch %d: the prompt must carry the definition rendered with the final values (%q):\n%s", n, tc.line, p.InitialPrompt)
		}
		if strings.Contains(p.InitialPrompt, "{{image}}") || strings.Contains(p.InitialPrompt, "lane-img") {
			t.Fatalf("dispatch %d: the prompt carries a raw placeholder or the lane default:\n%s", n, p.InitialPrompt)
		}
		if !reflect.DeepEqual(p.Params, tc.params) {
			t.Fatalf("dispatch %d: the derivation carries params %v, want %v", n, p.Params, tc.params)
		}
		raw, err := os.ReadFile(rendered)
		if err != nil {
			t.Fatalf("dispatch %d: %v", n, err)
		}
		if want := extDef("probe", tc.line); strings.TrimSpace(string(raw)) != strings.TrimSpace(want) {
			t.Fatalf("dispatch %d: workflows/_rendered/probe.md =\n%s\nwant the definition rendered with the final values:\n%s", n, raw, want)
		}
		if fi, err := os.Stat(rendered); err != nil || fi.Mode().Perm() != 0o444 {
			t.Fatalf("dispatch %d: the rendered definition stays read-only: %v %v", n, fi.Mode(), err)
		}
		if err := p.Journal(4242, "2026-11-16T00:00:00Z"); err != nil {
			t.Fatalf("dispatch %d: journal: %v", n, err)
		}
		entries, err := capsule.ReadSpawnJournal(capDir)
		if err != nil || len(entries) != i+1 {
			t.Fatalf("dispatch %d: journal %v (%v)", n, entries, err)
		}
		if got := entries[i].Params; !reflect.DeepEqual(got, tc.params) {
			t.Fatalf("dispatch %d: the journal line records params %v, want %v", n, got, tc.params)
		}
	}
}

// A resume of the same conversation whose params changed since the
// dispatch it resumes states the new values in its first message — the
// full prompt, the short owner-message resume and the short
// ended-without-hand-off reminder alike; unchanged params add nothing.
func TestW78ResumeParamsChanged(t *testing.T) {
	const heading = "## Step params changed"
	first := map[string]string{"image": "reg/img:2", "flag": "on"}
	changed := map[string]string{"image": "reg/img:3", "flag": "on"}
	msgs := []capsule.AskLine{{At: "2026-11-16T00:00:01Z", Step: "probe", Spawn: 1, Kind: capsule.AskKindMessage, Text: "use the new image"}}
	for _, tc := range []struct {
		name   string
		sp     StepSpawn
		params map[string]string
		want   bool
	}{
		{"full prompt", StepSpawn{Resume: true}, changed, true},
		{"owner-message resume", StepSpawn{Resume: true, MessageResume: true, OwnerMessages: msgs}, changed, true},
		{"ended-without-hand-off reminder", StepSpawn{Resume: true, PriorEndedWithoutResult: true, RetryTriggered: true}, changed, true},
		{"unchanged params", StepSpawn{Resume: true}, first, false},
		{"unchanged params, owner-message resume", StepSpawn{Resume: true, MessageResume: true, OwnerMessages: msgs}, first, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ad, sp, capDir := w78SpawnEnv(t)
			if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "probe", WIID: "wi_w78", SpawnN: 1, Harness: harness.Pi, Params: first}); err != nil {
				t.Fatalf("first dispatch: %v", err)
			}
			p1, _ := sp.last()
			writeCapsuleFile(t, capDir, strings.TrimPrefix(p1.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
			if err := p1.Journal(4242, "2026-11-16T00:00:00Z"); err != nil {
				t.Fatalf("journal: %v", err)
			}
			next := tc.sp
			next.StepID, next.WIID, next.SpawnN, next.Harness, next.Params = "probe", "wi_w78", 2, harness.Pi, tc.params
			if _, err := ad.SpawnStep(context.Background(), next); err != nil {
				t.Fatalf("resume: %v", err)
			}
			p2, _ := sp.last()
			if !p2.Resume {
				t.Fatalf("precondition: the dispatch must resume the same conversation: %+v", p2)
			}
			has := strings.Contains(p2.InitialPrompt, heading)
			if has != tc.want {
				t.Fatalf("the resume's first message carries %q: %v, want %v:\n%s", heading, has, tc.want, p2.InitialPrompt)
			}
			if !tc.want {
				return
			}
			section := p2.InitialPrompt[strings.Index(p2.InitialPrompt, heading):]
			if !strings.Contains(section, "- `image`: `reg/img:3`") {
				t.Fatalf("the section must state the changed param's new value:\n%s", section)
			}
			if strings.Contains(section, "- `flag`") {
				t.Fatalf("the section must list only the params that changed:\n%s", section)
			}
		})
	}
}
