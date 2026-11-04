// carries: internal/aihub/memory/*

package test

// w298_module_map_e2e_test.go: the real-binary acceptance suite for
// — `doc.module-map` as a revisioned memory type.
//
// Authored from artifacts/spec.md's `## Acceptance criteria` and
// `#### Scenario` blocks BEFORE the implementation, against the project's
// real e2e surfaces only: the REAL aihub binary this harness builds and
// boots on loopback (`newV1Install`), the real `pf` CLI (`pf recall`), and
// the existing HTTP memory face (Create / search) — never an
// implementation-internal symbol. The file compiles and vets against the
// pre-implementation tree, so every baseline red is an assertion red.
//
// Scenario map (document order is artifacts/e2e-scenarios.md):
//   S1 Create refuses a missing/malformed title (and accepts a conforming one)
//   S2 Create refuses the missing repo/module tags and names each
//   S3 Create refuses a body without the baseline line
//   S4 One refusal names every missing piece (title, tags, baseline)
//   S5 pf recall (all mode): rev2 ranks before a more relevant rev1
//   S6 pf recall (--any): rev2 ranks before a more relevant rev1
//
// Existing behaviour is covered by other suites, not re-authored here:
// browse's default updated sort already lists rev2 before rev1 (the plan
// declares browse read-only), an old revision is already readable and
// already returned by a query that matches only it, `doc.track` already
// needs no baseline line, and `supersedes` is already optional.
//
// Every test prints `PASS Sn` on its last line, so a bound scenario's
// passing run carries a line no failing run prints.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

const w298Type = "doc.module-map"

// w298Create is one real Create call against the booted aihub binary. It
// answers the stored id on success or the typed error on refusal.
func w298Create(inst *v1Install, req map[string]any) (string, *api.Error) {
	var out struct {
		ID string `json:"id"`
	}
	err := inst.client.Do("POST", "/api/v1/memories", req, &out)
	if err != nil {
		if ae, ok := err.(*api.Error); ok {
			return "", ae
		}
		return "", &api.Error{Code: "NON-API", Detail: err.Error()}
	}
	if out.ID == "" {
		return "", &api.Error{Code: "NON-API", Detail: "Create answered no id"}
	}
	return out.ID, nil
}

// w298Req is one Create body. A nil title is omitted (the absent-title case).
func w298Req(title *string, tags []string, body string) map[string]any {
	req := map[string]any{
		"project":    v1Project,
		"type":       w298Type,
		"visibility": "project",
		"content":    body,
		"tags":       tags,
	}
	if title != nil {
		req["title"] = *title
	}
	return req
}

// w298Refuse asserts one Create refusal: MEM-400 and an answer naming the
// missing piece, answered by the real binary. It returns the detail.
func w298Refuse(t *testing.T, inst *v1Install, name string, req map[string]any) string {
	t.Helper()
	id, ae := w298Create(inst, req)
	if ae == nil {
		t.Fatalf("%s: Create accepted the request and stored %s; want MEM-400", name, id)
	}
	if ae.Code != api.ErrMemBadInput {
		t.Fatalf("%s: Create refused with %q (%s), want %s", name, ae.Code, ae.Detail, api.ErrMemBadInput)
	}
	return ae.Detail
}

// w298ModuleMapCount counts the doc.module-map rows a real browse shows.
func w298ModuleMapCount(t *testing.T, inst *v1Install) int {
	t.Helper()
	raw, err := inst.client.SearchMemories(v1Project, "", w298Type, nil, 200, "", "", "")
	if err != nil {
		t.Fatalf("browse doc.module-map: %v", err)
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("browse doc.module-map answer: %v (%s)", err, raw)
	}
	return len(page.Items)
}

// w298RecallIDs runs the REAL `pf --json recall` and answers the hit ids in
// the answer's order.
func w298RecallIDs(t *testing.T, inst *v1Install, cfg string, args ...string) []string {
	t.Helper()
	out, code := w123Run(t, inst, cfg, nil, append([]string{"--json", "recall", "--project", v1Project}, args...)...)
	if code != 0 {
		t.Fatalf("pf recall %v: exit %d: %s", args, code, out)
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("pf recall %v answer: %v (%s)", args, err, out)
	}
	ids := make([]string, 0, len(list.Items))
	for _, it := range list.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// w298Index answers the first position of id, or -1.
func w298Index(ids []string, id string) int {
	for i, got := range ids {
		if got == id {
			return i
		}
	}
	return -1
}

// w298WantCurrentFirst asserts rev2 (the later revision) precedes rev1 in
// one answer, and that both are still present.
func w298WantCurrentFirst(t *testing.T, mode string, ids []string, rev1, rev2 string) {
	t.Helper()
	i1, i2 := w298Index(ids, rev1), w298Index(ids, rev2)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("%s: answer %v lacks rev1 %s or rev2 %s (ordering must never hide an entry)", mode, ids, rev1, rev2)
	}
	if i2 > i1 {
		t.Fatalf("%s: answer order %v puts the current revision rev2 at %d after the older rev1 at %d; want rev2 first whatever the bm25 relevance", mode, ids, i2, i1)
	}
}

// w298SeedRevisions creates rev1 with a deliberately better bm25 match for
// "sprocket" than rev2 (rev2 is a much longer document carrying the term
// once), then rev2 with a later updated_at, both under one module key.
func w298SeedRevisions(t *testing.T) (inst *v1Install, rev1, rev2 string) {
	t.Helper()
	inst = newV1Install(t)
	rev1Body := "baseline @abc1234 2026-11-01\n\nsprocket sprocket sprocket sprocket sprocket sprocket sprocket sprocket\n"
	rev2Body := "baseline @def5678 2026-11-02\n\nsprocket\n\n" +
		strings.Repeat("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu ", 6)
	var ae *api.Error
	rev1, ae = w298Create(inst, w298Req(strPtr("demo/widget rev1"), []string{"demo", "widget"}, rev1Body))
	if ae != nil {
		t.Fatalf("seed rev1: %s: %s", ae.Code, ae.Detail)
	}
	rev2, ae = w298Create(inst, w298Req(strPtr("demo/widget rev2"), []string{"demo", "widget"}, rev2Body))
	if ae != nil {
		t.Fatalf("seed rev2: %s: %s", ae.Code, ae.Detail)
	}
	return inst, rev1, rev2
}

// S1: a missing or malformed title is refused naming the title requirement;
// a conforming entry (no supersedes line) is accepted and stored.
func TestW298CreateRefusesMissingTitle(t *testing.T) {
	inst := newV1Install(t)
	body := "baseline @abc1234 2026-11-01\n\nthe module map body\n"
	tags := []string{"demo", "widget"}
	cases := []struct {
		name  string
		title *string
	}{
		{"absent title", nil},
		{"blank title", strPtr("")},
		{"no revision suffix", strPtr("demo/widget")},
		{"non-numeric revision", strPtr("demo/widget revX")},
		{"zero revision", strPtr("demo/widget rev0")},
		{"empty repo segment", strPtr("/widget rev2")},
		{"empty module segment", strPtr("demo/ rev2")},
	}
	for _, c := range cases {
		detail := w298Refuse(t, inst, c.name, w298Req(c.title, tags, body))
		low := strings.ToLower(detail)
		if !strings.Contains(low, "title") || !strings.Contains(low, "rev") {
			t.Fatalf("%s: MEM-400 detail %q does not name the title requirement", c.name, detail)
		}
	}
	if n := w298ModuleMapCount(t, inst); n != 0 {
		t.Fatalf("the refused Creates stored %d doc.module-map rows, want 0", n)
	}
	id, ae := w298Create(inst, w298Req(strPtr("demo/widget rev2"), tags, "baseline @abc1234 2026-11-01\n\ncurrent\n"))
	if ae != nil {
		t.Fatalf("a conforming create (no supersedes line) was refused: %s: %s", ae.Code, ae.Detail)
	}
	if id == "" {
		t.Fatal("the conforming create answered no id")
	}
	if n := w298ModuleMapCount(t, inst); n != 1 {
		t.Fatalf("after one conforming create, doc.module-map rows = %d, want 1", n)
	}
	fmt.Printf("PASS S1\n")
}

// S2: missing repo/module tags are refused, each missing tag named.
func TestW298CreateRefusesMissingTags(t *testing.T) {
	inst := newV1Install(t)
	body := "baseline @abc1234 2026-11-01\n\nmodule map body\n"
	cases := []struct {
		name   string
		tags   []string
		absent []string
	}{
		{"both tags missing", nil, []string{"demo", "widget"}},
		{"repo tag missing", []string{"widget"}, []string{"demo"}},
		{"module tag missing", []string{"demo"}, []string{"widget"}},
	}
	for _, c := range cases {
		detail := w298Refuse(t, inst, c.name, w298Req(strPtr("demo/widget rev2"), c.tags, body))
		for _, tag := range c.absent {
			if !strings.Contains(detail, tag) || !strings.Contains(strings.ToLower(detail), "tag") {
				t.Fatalf("%s: MEM-400 detail %q does not name the missing tag %q", c.name, detail, tag)
			}
		}
	}
	if n := w298ModuleMapCount(t, inst); n != 0 {
		t.Fatalf("the refused Creates stored %d doc.module-map rows, want 0", n)
	}
	fmt.Printf("PASS S2\n")
}

// S3: a body without the baseline line is refused naming it.
func TestW298CreateRefusesMissingBaseline(t *testing.T) {
	inst := newV1Install(t)
	detail := w298Refuse(t, inst, "no baseline line", w298Req(
		strPtr("demo/widget rev2"), []string{"demo", "widget"}, "a module map body with no pin line\n"))
	if !strings.Contains(strings.ToLower(detail), "baseline") {
		t.Fatalf("MEM-400 detail %q does not name the baseline line", detail)
	}
	if n := w298ModuleMapCount(t, inst); n != 0 {
		t.Fatalf("the refused Create stored %d doc.module-map rows, want 0", n)
	}
	fmt.Printf("PASS S3\n")
}

// S4: one refusal names all three missing pieces, not only the first found.
func TestW298CreateNamesEveryMissingPiece(t *testing.T) {
	inst := newV1Install(t)
	detail := w298Refuse(t, inst, "all three missing", w298Req(
		strPtr("not a module title"), nil, "a body with no pin line\n"))
	low := strings.ToLower(detail)
	for _, piece := range []string{"title", "tag", "baseline"} {
		if !strings.Contains(low, piece) {
			t.Fatalf("MEM-400 detail %q does not name the missing %s piece", detail, piece)
		}
	}
	if n := w298ModuleMapCount(t, inst); n != 0 {
		t.Fatalf("the refused Create stored %d doc.module-map rows, want 0", n)
	}
	fmt.Printf("PASS S4\n")
}

// S5: recall in `all` mode ranks rev2 (current) before a more relevant rev1.
func TestW298RecallAllRanksCurrentFirst(t *testing.T) {
	inst, rev1, rev2 := w298SeedRevisions(t)
	ids := w298RecallIDs(t, inst, w123Cfg(t), "sprocket")
	w298WantCurrentFirst(t, "recall all", ids, rev1, rev2)
	fmt.Printf("PASS S5\n")
}

// S6: recall in `--any` mode ranks rev2 (current) before a more relevant rev1.
func TestW298RecallAnyRanksCurrentFirst(t *testing.T) {
	inst, rev1, rev2 := w298SeedRevisions(t)
	ids := w298RecallIDs(t, inst, w123Cfg(t), "--any", "sprocket")
	w298WantCurrentFirst(t, "recall --any", ids, rev1, rev2)
	fmt.Printf("PASS S6\n")
}
