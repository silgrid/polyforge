package daemon

// analyzer_test.go: the explicit, read-only message analyzer. It never
// starts on its own, walks summarize.models in order, and one wall-clock
// deadline bounds the WHOLE run (each dispatch gets the remaining budget,
// not a fresh timeout).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

type recordingContextDispatcher struct {
	mu     sync.Mutex
	calls  []string
	block  bool
	errFor map[string]error
}

func (d *recordingContextDispatcher) Dispatch(candidate, prompt string) (string, error) {
	return d.DispatchContext(context.Background(), candidate, prompt)
}

func (d *recordingContextDispatcher) DispatchContext(ctx context.Context, candidate, prompt string) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, candidate)
	block := d.block
	err := d.errFor[candidate]
	d.mu.Unlock()
	if block {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "", errors.New("the fake dispatch outlived its context")
		}
	}
	if err != nil {
		return "", err
	}
	return "conclusion from " + candidate, nil
}

func (d *recordingContextDispatcher) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

// TestAnalyzerNeverSelfStarts: a running daemon with an assembled
// dispatcher makes no analyzer call until a human triggers the route.
func TestAnalyzerNeverSelfStarts(t *testing.T) {
	disp := &recordingContextDispatcher{}
	_ = newTestDaemon(t, func(c *Config) { c.SummarizeDispatcher = disp })
	time.Sleep(150 * time.Millisecond)
	if got := disp.snapshot(); len(got) != 0 {
		t.Fatalf("the analyzer self-started: %v", got)
	}
}

// TestRunAnalyzerUsesModelsInOrder: the first failing candidate is
// followed by the next one, and the answer names the model that produced
// it.
func TestRunAnalyzerUsesModelsInOrder(t *testing.T) {
	disp := &recordingContextDispatcher{errFor: map[string]error{"pi:first": errors.New("no")}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, model, err := runAnalyzer(ctx, disp, []string{"pi:first", "pi:second"}, "prompt")
	if err != nil || out != "conclusion from pi:second" || model != "pi:second" {
		t.Fatalf("walk result: %q %q %v", out, model, err)
	}
	if calls := disp.snapshot(); len(calls) != 2 || calls[0] != "pi:first" || calls[1] != "pi:second" {
		t.Fatalf("model order: %v", calls)
	}
}

// TestRunAnalyzerOneWallClockDeadline: a blocking first candidate exhausts
// the one budget; the walk stops instead of granting the second candidate
// another timeout.
func TestRunAnalyzerOneWallClockDeadline(t *testing.T) {
	disp := &recordingContextDispatcher{block: true}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err := runAnalyzer(ctx, disp, []string{"pi:first", "pi:second", "pi:third"}, "prompt")
	if err == nil {
		t.Fatal("an exhausted budget must fail")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the run ignored its wall-clock deadline (%v)", elapsed)
	}
	if calls := disp.snapshot(); len(calls) != 1 {
		t.Fatalf("models tried after the deadline: %v", calls)
	}
}

// The production ExecDispatcher satisfies the context seam used by the
// analyzer.
var _ summarize.ContextDispatcher = (*summarize.ExecDispatcher)(nil)

// routeAnalyzerDispatcher parks inside the one dispatch so the test can
// sample the daemon while the explicit analyzer run is mid-flight.
type routeAnalyzerDispatcher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   []string
}

func (d *routeAnalyzerDispatcher) Dispatch(candidate, prompt string) (string, error) {
	return d.DispatchContext(context.Background(), candidate, prompt)
}

func (d *routeAnalyzerDispatcher) DispatchContext(ctx context.Context, candidate, prompt string) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, candidate)
	d.mu.Unlock()
	d.once.Do(func() { close(d.entered) })
	select {
	case <-d.release:
		return routeAnalyzerConclusion, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

const routeAnalyzerConclusion = "route-only conclusion"

type routeHubCall struct {
	method string
	path   string
	body   string
}

// TestAnalyzeRouteReturnsConclusionOnlyToCaller: the explicit route run
// answers the caller, makes no aihub write (and sends nothing to aihub
// after the dispatch), takes no job slot, leaves the capsule/repo tree
// untouched, and refuses a concurrent run with DAEMON-409.
func TestAnalyzeRouteReturnsConclusionOnlyToCaller(t *testing.T) {
	disp := &routeAnalyzerDispatcher{entered: make(chan struct{}), release: make(chan struct{})}
	td := newTestDaemon(t, func(c *Config) { c.SummarizeDispatcher = disp })

	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("summarize: { models: [pi:prov/route] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var hubMu sync.Mutex
	var hubCalls []routeHubCall
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, body, out any) error {
		hubMu.Lock()
		hubCalls = append(hubCalls, routeHubCall{method: method, path: path, body: fmt.Sprint(body)})
		hubMu.Unlock()
		if method == "GET" && path == "/api/v1/messages/msg_1" {
			raw, _ := json.Marshal(api.Message{
				ID: "msg_1", Seq: 7, ConversationID: "conv_1", SenderID: "u2",
				Text: "please review", CreatedAt: "2026-10-25T00:00:00Z",
			})
			return json.Unmarshal(raw, out)
		}
		return &api.Error{Code: api.ErrCoreNotFound, Detail: "fakeHub: unscripted " + method + " " + path}
	}
	td.hub.mu.Unlock()

	treeBefore := routeTree(t, ws, td.cfg.GitRemoteBase)
	slotsBefore := routeJobSlots(td)

	type answer struct {
		raw json.RawMessage
		err error
	}
	first := make(chan answer, 1)
	go func() {
		raw, err := td.client.AnalyzeMessage("msg_1")
		first <- answer{raw: raw, err: err}
	}()

	select {
	case <-disp.entered:
	case <-time.After(5 * time.Second):
		close(disp.release)
		t.Fatal("the explicit route never reached the dispatcher")
	}

	hubMu.Lock()
	callsDuring := append([]routeHubCall(nil), hubCalls...)
	hubMu.Unlock()
	if slots := routeJobSlots(td); slots != slotsBefore {
		t.Fatalf("the analyzer took a job slot: %v -> %v", slotsBefore, slots)
	}
	if tree := routeTree(t, ws, td.cfg.GitRemoteBase); !reflect.DeepEqual(tree, treeBefore) {
		t.Fatalf("the analyzer changed the capsule/repo tree: %v -> %v", treeBefore, tree)
	}

	// A concurrent run must be refused while the first is still running.
	if _, err := td.client.AnalyzeMessage("msg_1"); err == nil {
		t.Fatal("a concurrent analyzer run was accepted")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
			t.Fatalf("concurrent analyzer run = %v, want DAEMON-409", err)
		}
	}

	close(disp.release)
	var got answer
	select {
	case got = <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the analyzer route did not answer")
	}
	if got.err != nil {
		t.Fatalf("analyze route: %v", got.err)
	}
	var result api.AnalyzeResult
	if err := json.Unmarshal(got.raw, &result); err != nil {
		t.Fatalf("analyze answer: %v (%s)", err, got.raw)
	}
	if result.MessageID != "msg_1" || result.Conclusion != routeAnalyzerConclusion || result.Model != "pi:prov/route" {
		t.Fatalf("analyze answer = %+v, want the dispatch conclusion only to the caller", result)
	}

	hubMu.Lock()
	callsAfter := append([]routeHubCall(nil), hubCalls...)
	hubMu.Unlock()
	if len(callsAfter) != len(callsDuring) {
		t.Fatalf("the analyzer kept talking to aihub after the dispatch: %v -> %v", callsDuring, callsAfter)
	}
	for _, call := range callsAfter {
		if call.method != "GET" {
			t.Fatalf("the analyzer made an aihub write call: %+v", call)
		}
		for _, text := range []string{call.path, call.body} {
			if strings.Contains(text, routeAnalyzerConclusion) {
				t.Fatalf("the conclusion reached aihub in %+v", call)
			}
		}
	}
	if slots := routeJobSlots(td); slots != slotsBefore {
		t.Fatalf("the analyzer left the job queue changed: %v -> %v", slotsBefore, slots)
	}
	if tree := routeTree(t, ws, td.cfg.GitRemoteBase); !reflect.DeepEqual(tree, treeBefore) {
		t.Fatalf("the analyzer left the capsule/repo tree changed: %v -> %v", treeBefore, tree)
	}
}

// routeJobSlots is (pending, inflight, capacity, live jobs).
func routeJobSlots(td *testDaemon) [4]int {
	td.q.mu.Lock()
	pending, inflight := len(td.q.pending), len(td.q.inflight)
	td.q.mu.Unlock()
	return [4]int{pending, inflight, td.q.MaxConcurrent(), len(td.rt.List())}
}

// routeTree snapshots names, modes and bytes under every root.
func routeTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[path] = info.Mode().String() + "/"
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = info.Mode().String() + ":" + string(raw)
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot %s: %v", root, err)
		}
	}
	return out
}

// TestAnalyzerEscapesMessageIDs pins B3: both message ids the analyzer
// puts in an aihub path (the route's and the fetched message's reply_to)
// are one percent-encoded segment, so a `?`, `#` or `/` inside an id can
// never rewrite the query or the route aihub sees.
func TestAnalyzerEscapesMessageIDs(t *testing.T) {
	td := newTestDaemon(t, nil)
	var mu sync.Mutex
	var paths []string
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, _, out any) error {
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		if m, ok := out.(*api.Message); ok && len(paths) == 1 {
			m.ReplyTo = "re/p?x=1#y"
		}
		return nil
	}
	td.hub.mu.Unlock()
	req := httptest.NewRequest("POST", "/messages/x/analyze", nil)
	req.SetPathValue("id", "m/1?all=1#frag")
	td.analyzeRoute(httptest.NewRecorder(), req)
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/api/v1/messages/m%2F1%3Fall=1%23frag", "/api/v1/messages/re%2Fp%3Fx=1%23y"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("hub request paths = %q, want %q", paths, want)
	}
}
