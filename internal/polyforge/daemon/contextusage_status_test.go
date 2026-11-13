package daemon

// contextusage_status_test.go: the running step session's context usage
// on GET /status's job row and on GET /jobs/{wi}/watch's
// progress block — numbers and the model id only, never transcript content.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const (
	ctxStatusTS    = "2026-01-02T03:04:05.000Z"
	ctxMarker      = "CTX-TRANSCRIPT-MARKER"
	ctxFakeSecret  = "sk-ctxtest-00000000000000000000000000000000"
	ctxFakeBearer  = "Bearer ctxtest.eyJmYWtlIjoidG9rZW4ifQ.c2lnbmF0dXJl"
	ctxStatusModel = "example-gateway/model-a"
)

// ctxTranscript is a pi session whose text and tool output carry the marker
// and credential-shaped values; its context is 5000 (the reply's usage) +
// the trailing tool result's estimate.
func ctxTranscript() (string, int64) {
	toolOut := ctxMarker + " API_KEY=" + ctxFakeSecret + " Authorization: " + ctxFakeBearer
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"` + ctxStatusTS + `","cwd":"/tmp/capsule"}`,
		`{"type":"model_change","id":"e1","timestamp":"` + ctxStatusTS + `","provider":"example-gateway","modelId":"model-a"}`,
		`{"type":"message","id":"e2","timestamp":"` + ctxStatusTS + `","message":{"role":"user","content":[{"type":"text","text":"` + ctxMarker + ` please use ` + ctxFakeSecret + `"}]}}`,
		`{"type":"message","id":"e3","timestamp":"` + ctxStatusTS + `","message":{"role":"assistant","content":[{"type":"text","text":"` + ctxMarker + `"}],"provider":"example-gateway","model":"model-a","usage":{"input":4000,"output":1000,"cacheRead":0,"cacheWrite":0,"totalTokens":5000},"stopReason":"toolUse"}}`,
		`{"type":"message","id":"e4","timestamp":"` + ctxStatusTS + `","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"` + toolOut + `"}]}}`,
	}
	return strings.Join(lines, "\n") + "\n", 5000 + int64((len(toolOut)+3)/4)
}

// startCtxDispatch claims wi and announces a resumed pi dispatch whose live
// transcript is a capsule-side file holding ctxTranscript.
func startCtxDispatch(t *testing.T, td *testDaemon, wi string) int64 {
	t.Helper()
	td.submitClaim(t, wi)
	td.waitMonitored(t, wi)
	body, tokens := ctxTranscript()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	td.sup.Link(wi).Fact(supervise.StepStarted{
		Step: "build", SpawnN: 1, Model: "model-a", Harness: "pi", Resumed: true, TranscriptPath: path,
		StartedAt: time.Now(), Thresholds: supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour},
	})
	return tokens
}

// waitStatusContext polls GET /status (bounded) until wi's row carries a
// context and answers the row.
func waitStatusContext(t *testing.T, td *testDaemon, wi string) *cli.StatusJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := td.statusOf(t)
		if j := statusJobOf(&st, wi); j != nil && j.Context != nil {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("the dispatch's context never surfaced on /status: %+v", st.Jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// watchRaw answers GET /jobs/{wi}/watch's body as the route writes it.
func watchRaw(t *testing.T, td *testDaemon, wi string) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", "/jobs/"+wi+"/watch", nil)
	req.SetPathValue("wi", wi)
	rec := httptest.NewRecorder()
	td.watchSnapshotRoute(rec, req)
	if rec.Code != 200 {
		t.Fatalf("watch %s: HTTP %d: %s", wi, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func ctxMatches(c *protocol.ContextUsage, tokens int64) bool {
	return c != nil && c.Tokens != nil && *c.Tokens == tokens && c.Model == ctxStatusModel && c.AsOf == ctxStatusTS
}

func TestStatusAndWatchCarryContextUsage(t *testing.T) {
	td := newTestDaemon(t, nil)
	tokens := startCtxDispatch(t, td, "wi_ctxst")
	j := waitStatusContext(t, td, "wi_ctxst")
	if !ctxMatches(j.Context, tokens) {
		t.Fatalf("status context %+v: want tokens %d, model %s, as_of %s", j.Context, tokens, ctxStatusModel, ctxStatusTS)
	}
	var snap protocol.WatchSnapshot
	if err := json.Unmarshal(watchRaw(t, td, "wi_ctxst"), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Progress == nil || !ctxMatches(snap.Progress.Context, tokens) {
		t.Fatalf("watch progress %+v: want the same context as /status", snap.Progress)
	}
}

func TestContextUsageWireIsNumbersOnly(t *testing.T) {
	td := newTestDaemon(t, nil)
	startCtxDispatch(t, td, "wi_ctxwire")
	waitStatusContext(t, td, "wi_ctxwire")
	statusRaw, err := td.client.Status()
	if err != nil {
		t.Fatal(err)
	}
	watch := watchRaw(t, td, "wi_ctxwire")

	allowed := map[string]bool{"tokens": true, "window": true, "percent": true, "model": true, "as_of": true}
	checkKeys := func(face string, ctx json.RawMessage) {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(ctx, &m); err != nil || len(m) == 0 {
			t.Fatalf("%s: context %s is not a non-empty object", face, ctx)
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
			if !allowed[k] {
				t.Errorf("%s: context carries key %q (allowed: tokens, window, percent, model, as_of)", face, k)
			}
		}
		sort.Strings(keys)
		t.Logf("%s context keys: %v", face, keys)
	}

	var st struct {
		Jobs []struct {
			WorkItemID string          `json:"work_item_id"`
			Context    json.RawMessage `json:"context"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(statusRaw, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range st.Jobs {
		if j.WorkItemID == "wi_ctxwire" {
			found = true
			checkKeys("status", j.Context)
		}
	}
	if !found {
		t.Fatalf("no status row for wi_ctxwire: %s", statusRaw)
	}
	var w struct {
		Progress struct {
			Context json.RawMessage `json:"context"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(watch, &w); err != nil {
		t.Fatal(err)
	}
	checkKeys("watch", w.Progress.Context)

	for face, body := range map[string][]byte{"status": statusRaw, "watch": watch} {
		for _, needle := range []string{ctxMarker, ctxFakeSecret, ctxFakeBearer, "ctxtest"} {
			if strings.Contains(string(body), needle) {
				t.Errorf("%s JSON carries transcript content %q", face, needle)
			}
		}
	}
}
