package test

// w135_webui_e2e_load_wait_test.go: the console browser
// suite must survive data that arrives late. The reported drift is S18
// (the work-item table), S19 (the see-all links) and S21 (the date
// group labels): those scenario containers render while their API
// answer is still in flight, so a loaded host makes the immediate
// content assertion red although the data does arrive.
//
// The reproducer holds the scenario's own data answer behind the
// container testid: a loopback proxy fronts the real aihub and sleeps
// the named /web/api request for two seconds. Each subtest delays only
// one family, so the named scenario is the one whose container is
// already visible while its derived content is absent — and every
// OTHER scenario still runs with its data on time. A runner that waits
// for the same content it asserts passes all six scenarios; one that
// asserts the instant the container appears names the delayed scenario
// and exits non-zero.

import (
	"encoding/json"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestW135WebuiScenarioDataWait(t *testing.T) {
	if msg := browserPrereqError(t); msg != "" {
		t.Fatalf("the console browser suite's prerequisites are missing: %s", msg)
	}
	inst := newV1Install(t)
	fixtures := seedBrowserFixtures(t, inst)
	raw, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatalf("marshal browser fixtures: %v", err)
	}

	cases := []struct {
		name       string
		pathPrefix string
		scenario   string
	}{
		{"S18 work-item table data", "/web/api/work-items", "S18"},
		{"S19 search result data", "/web/api/search", "S19"},
		{"S21 memory list data", "/web/api/memories", "S21"},
	}
	const delay = 2 * time.Second
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			proxy := newDataDelayProxy(t, inst.url, tc.pathPrefix, delay)

			cmd := exec.Command("node", filepath.Join("webui", "e2e", "run.mjs"))
			cmd.Dir = v1Root(t)
			cmd.Env = append(browserSuiteEnv(t),
				"PF_WEB_URL="+proxy.URL,
				"PF_WEB_KEY="+inst.adminKey,
				"PF_WEB_FIXTURES="+string(raw),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the browser suite did not survive a %s delay on %s (the %s scenario's own data): %v\n%s",
					delay, tc.pathPrefix, tc.scenario, err, out)
			}
			for _, name := range []string{"S17", "S18", "S19", "S20", "S21", "S22"} {
				if !strings.Contains(string(out), "PASS "+name) {
					t.Fatalf("the browser suite output misses scenario %s after a %s delay on %s:\n%s",
						name, delay, tc.pathPrefix, out)
				}
			}
		})
	}
}

// newDataDelayProxy fronts target and holds the request whose path
// starts with pathPrefix for delay before forwarding it. Everything
// else passes through untouched (and the browser's Basic credential
// rides along), so only the named data family is late.
func newDataDelayProxy(t *testing.T, target, pathPrefix string, delay time.Duration) *httptest.Server {
	t.Helper()
	tu, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse aihub URL %q: %v", target, err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		if strings.HasPrefix(r.In.URL.Path, pathPrefix) {
			select {
			case <-time.After(delay):
			case <-r.In.Context().Done():
			}
		}
		r.SetURL(tu)
	}}
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	return srv
}
