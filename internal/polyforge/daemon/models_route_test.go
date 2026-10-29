package daemon

// models_route_test.go: GET /models — the read-only profile
// beside GET /status, its method refusal, its purity, the step-peer read
// path and the additive model_health summary.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func w14Entry(t *testing.T, at time.Time, wi, kind string, seq int64, body map[string]any) protocol.EventEntry {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.EventEntry{At: at.UTC().Format(time.RFC3339Nano), Kind: kind, WI: wi, Seq: seq, Body: raw}
}

func w14Raw(t *testing.T, sock, method, path string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, err := http.NewRequest(method, "http://daemon"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestModelsRouteReadOnlyAndStatusSummary(t *testing.T) {
	td := newTestDaemon(t, nil)
	now := time.Now().UTC()
	seq := int64(0)
	for i := 0; i < 3; i++ {
		spawn := int64(i + 1)
		when := now.Add(time.Duration(-3+i) * time.Hour)
		seq++
		td.modelHealthTracker().Ingest(w14Entry(t, when, "wi_m1", "step_started", seq,
			map[string]any{"step": "verification", "model": "glm-5.3-flash", "spawn_n": spawn}))
		seq++
		td.modelHealthTracker().Ingest(w14Entry(t, when.Add(time.Minute), "wi_m1", "step_failed", seq,
			map[string]any{"step": "verification", "cause": "model_error", "spawn_n": spawn}))
	}

	code, body := w14Raw(t, td.sock, "GET", "/models")
	if code != http.StatusOK {
		t.Fatalf("GET /models = %d, want 200; body: %s", code, body)
	}
	for _, want := range []string{`"window"`, "glm-5.3-flash", "verification"} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /models must carry %q; body: %s", want, body)
		}
	}
	for _, leak := range []string{td.cfg.DataDir, "transcript", "prompt", "api_key", "token"} {
		if leak != "" && strings.Contains(body, leak) {
			t.Fatalf("GET /models leaked %q; body: %s", leak, body)
		}
	}
	if code, _ := w14Raw(t, td.sock, "POST", "/models"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /models = %d, want 405", code)
	}

	code, status := w14Raw(t, td.sock, "GET", "/status")
	if code != http.StatusOK || !strings.Contains(status, `"model_health"`) || !strings.Contains(status, "glm-5.3-flash") {
		t.Fatalf("GET /status must carry the additive summary; code=%d body: %s", code, status)
	}

	// A GET without an identifiable peer (no refusal for reads) is served:
	// the step-origin guard only ever refuses mutating methods.
	h := td.handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("a GET /models with no peer credential = %d, want 200 (reads stay open)", rec.Code)
	}
}

func TestStatusSummaryIsExplicitWhenSilent(t *testing.T) {
	td := newTestDaemon(t, nil)
	code, body := w14Raw(t, td.sock, "GET", "/status")
	if code != http.StatusOK {
		t.Fatalf("GET /status = %d", code)
	}
	if !strings.Contains(body, `"model_health"`) || !strings.Contains(body, `"warnings":[]`) {
		t.Fatalf("a clean daemon must still carry an explicit empty summary; body: %s", body)
	}
}
