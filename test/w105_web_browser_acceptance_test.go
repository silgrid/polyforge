package test

// w105_web_browser_acceptance_test.go: the driver that
// makes the console's browser suite part of the repo's test bar. It
// starts the REAL aihub binary with the v1 harness, seeds the
// SQL-only fixtures (machine rows, a project name the API's charset
// refuses — the same otherwise-unrepresentable fixtures the authored
// Go suite seeds), then runs the browser runner (webui/e2e/run.mjs)
// against the loopback face with the admin key.
//
// It fails LOUDLY, never silently skips: a missing node, a missing
// playwright module or browser, or no discovered suite is an explicit
// failure naming the remedy. TestW105BrowserPrerequisites is the fast
// preflight: it checks the same discovery logic WITHOUT running the
// scenarios.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// browserRunnerCandidates are the discovered runner layouts, in order:
// the expected webui/e2e/run.mjs, a playwright config, or the
// webui test:e2e script.
func browserRunnerCandidates(root string) []string {
	return []string{
		filepath.Join(root, "webui", "e2e", "run.mjs"),
	}
}

// browserPrereqError answers the explicit failure message for one
// missing prerequisite, or "" when everything the suite needs exists.
func browserPrereqError(t *testing.T) string {
	t.Helper()
	root := v1Root(t)
	if _, err := exec.LookPath("node"); err != nil {
		return "node is not installed — the console browser suite needs node (the version .nvmrc pins); install node and run the bar again"
	}
	if missing := browserPlaywrightMissing(t, root); missing != "" {
		return missing
	}
	for _, candidate := range browserRunnerCandidates(root) {
		if _, err := os.Stat(candidate); err == nil {
			return ""
		}
	}
	return "no authored browser suite found — expected webui/e2e/run.mjs (or a playwright config / webui test:e2e script); author the suite before running the bar"
}

// browserPlaywrightMissing answers the explicit failure when the
// playwright module or its chromium browser is absent.
func browserPlaywrightMissing(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("node", "-e",
		`const {chromium} = require('playwright'); process.stdout.write(chromium.executablePath())`)
	cmd.Dir = filepath.Join(root, "webui")
	cmd.Env = browserSuiteEnv(t)
	out, err := cmd.Output()
	if err != nil {
		return "the playwright module is not installed — run `cd webui && npm ci` (the locked dev dependency), then `npx playwright install chromium`"
	}
	if _, serr := os.Stat(strings.TrimSpace(string(out))); serr != nil {
		return "the playwright chromium browser is not installed — run `npx playwright install chromium`"
	}
	return ""
}

// browserCacheDir answers the playwright browsers directory. The test
// binary's HOME and XDG_CACHE_HOME are the hermetic scratch roots
// (testenv), so playwright's default resolution would look inside the
// throwaway home; the browsers belong to the operator's account, like
// the go-build cache testenv keeps real for the toolchain. An explicit
// PLAYWRIGHT_BROWSERS_PATH wins; otherwise the real account home's
// cache is used.
func browserCacheDir(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); p != "" {
		return p
	}
	if cache, cerr := os.UserCacheDir(); cerr == nil {
		if dir := filepath.Join(cache, "ms-playwright"); dirExists(dir) {
			return dir
		}
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		dir := filepath.Join(u.HomeDir, ".cache", "ms-playwright")
		if dirExists(dir) {
			return dir
		}
	}
	// Not found anywhere: the preflight reports the remedy.
	cache, _ := os.UserCacheDir()
	return filepath.Join(cache, "ms-playwright")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// browserSuiteEnv answers the child-process environment for the node
// runner: the inherited environment plus the browsers path the runner
// must resolve like the preflight did.
func browserSuiteEnv(t *testing.T) []string {
	t.Helper()
	return append(os.Environ(), "PLAYWRIGHT_BROWSERS_PATH="+browserCacheDir(t))
}

// TestW105BrowserPrerequisites is the fast preflight: the discovery
// and prerequisite logic the full driver uses, without running the
// scenarios.
func TestW105BrowserPrerequisites(t *testing.T) {
	if msg := browserPrereqError(t); msg != "" {
		t.Fatalf("the console browser suite's prerequisites are missing: %s", msg)
	}
}

// TestW105BrowserConsoleAcceptance runs the browser suite against the
// real binary. make test has already built the frontend (webui-build),
// so the embedded shell and assets exist; this driver still checks
// node and playwright itself — the bar fails loudly, never skips.
func TestW105BrowserConsoleAcceptance(t *testing.T) {
	if msg := browserPrereqError(t); msg != "" {
		t.Fatalf("the console browser suite's prerequisites are missing: %s", msg)
	}
	inst := newV1Install(t)
	fixtures := seedBrowserFixtures(t, inst)

	cmd := exec.Command("node", filepath.Join("webui", "e2e", "run.mjs"))
	cmd.Dir = v1Root(t)
	raw, _ := json.Marshal(fixtures)
	cmd.Env = append(browserSuiteEnv(t),
		"PF_WEB_URL="+inst.url,
		"PF_WEB_KEY="+inst.adminKey,
		"PF_WEB_FIXTURES="+string(raw),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the console browser suite failed (%v):\n%s", err, out)
	}
	for _, name := range []string{
		"S17", "S18", "S19", "S20", "S21", "S22",
	} {
		if !strings.Contains(string(out), "PASS "+name) {
			t.Fatalf("the browser suite output misses scenario %s:\n%s", name, out)
		}
	}
	// Log the runner's PASS lines: the authored scenarios bind their
	// result to one marker line each, and -v surfaces them to the
	// verification harness.
	t.Logf("browser suite output:\n%s", out)
}

// browserFixtures carries the ids the driver seeded to the suite.
type browserFixtures struct {
	Project     string `json:"project"`
	HostileWI   string `json:"hostile_wi"`
	OddProject  string `json:"odd_project"`
	OddWI       string `json:"odd_wi"`
	OddGoal     string `json:"odd_goal"`
	OddMem      string `json:"odd_mem"`
	OddMemTitle string `json:"odd_mem_title"`
}

// seedBrowserFixtures seeds the suite's data through the real API
// where the API can represent it and through direct SQL only where it
// cannot (machine rows, a project name outside the API charset) — the
// same split the authored Go suite uses.
func seedBrowserFixtures(t *testing.T, inst *v1Install) browserFixtures {
	t.Helper()
	f := browserFixtures{Project: v1Project}

	// The hostile corpus: a work item whose goal, labels and content
	// carry script tags, event handlers, javascript: links AND unicode
	// tricks (an RTL override, a fullwidth lookalike tag, an invisible
	// separator) — plus a hostile event payload seeded below, so the
	// timeline row must render inert as well.
	wi, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: v1Project,
		Goal:    "<script>alert(1)</script> hostile browser goal \u202e<img src=x onerror=alert(6)> \u2062",
		Content: strPtr("# Head\n\n<script>alert(2)</script>\n\n[click](javascript:alert(3))\n\n<img src=x onerror=alert(4)>\n\n\uff1cscript\uff1ealert(7)\uff1c/script\uff1e \u202eonerror=alert(8)"),
		Labels:  []string{"<img onerror=alert(5)>"},
	})
	if err != nil {
		t.Fatalf("hostile work item: %v", err)
	}
	var hostile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wi, &hostile); err != nil || hostile.ID == "" {
		t.Fatalf("hostile work item answer: %v (%s)", err, wi)
	}
	f.HostileWI = hostile.ID
	inst.trackWI(hostile.ID)

	// Searchable rows for the grouped search page.
	for i := 0; i < 2; i++ {
		if _, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
			Project: v1Project, Goal: fmt.Sprintf("browser needle item %d", i),
		}); err != nil {
			t.Fatalf("search work item: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
			"project": v1Project, "type": "fact.note",
			"title":   fmt.Sprintf("browser needle memory %d", i),
			"content": "needle body", "tags": []string{"browser"},
			"visibility": "project",
		}, nil); err != nil {
			t.Fatalf("search memory: %v", err)
		}
	}
	// One memory per remaining kind hook (fact is seeded above): the
	// four kinds, the three named doc.* types and a plain doc, so the
	// colour scenario measures every hook side by side.
	for _, typ := range []string{"experience.wrap", "rule.guard", "methodology.flow", "doc.decision", "doc.design", "doc.review", "doc"} {
		if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
			"project": v1Project, "type": typ,
			"title":   "browser kind memory " + typ,
			"content": "kind body", "tags": []string{"kind"},
			"visibility": "project",
		}, nil); err != nil {
			t.Fatalf("kind memory %s: %v", typ, err)
		}
	}

	// Machine rows (SQL: the heartbeat face is not reachable from the
	// test process) — 1 minute / 20 minutes / 2 hours, the third
	// superseded, mirroring the authored machines scenario.
	operator, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: "w105-browser-operator", Email: "w105-browser-operator@w105.invalid",
		UserType: "human", Role: "writer",
	})
	if err != nil {
		t.Fatalf("operator user: %v", err)
	}
	var opUser struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(operator, &opUser); err != nil || opUser.ID == "" {
		t.Fatalf("operator answer: %v (%s)", err, operator)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()

	// A hostile event payload on the hostile work item: the timeline
	// must render it as escaped text (the s24 fixture precedent —
	// events are not writable through the public API).
	var hostileActor string
	if err := db.QueryRow(`SELECT reporter FROM work_items WHERE id=?1`, f.HostileWI).Scan(&hostileActor); err != nil {
		t.Fatalf("read the hostile work item's reporter: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO wi_events(work_item_id, created_at, actor, event_type, payload) VALUES (?1, ?2, ?3, 'wi_content_updated', ?4)`,
		f.HostileWI, now.Format(time.RFC3339Nano), hostileActor,
		`{"note":"<script>alert(9)</script> payload","session_hash":"w105-browser-hash-sentinel"}`); err != nil {
		t.Fatalf("hostile event row: %v", err)
	}

	for _, m := range []struct {
		id    string
		age   time.Duration
		super bool
	}{
		{"web-green", time.Minute, false},
		{"web-yellow", 20 * time.Minute, false},
		{"web-red", 2 * time.Hour, true},
	} {
		var super any
		if m.super {
			super = now.Add(-time.Hour).Format(time.RFC3339Nano)
		}
		if _, err := db.Exec(`INSERT INTO machines(id, runtime_version, mode, live_wi_ids, last_heartbeat_at, public_key, launcher, role, operator_id, superseded_at, capability)
			VALUES (?1, 'v9.9.9', 'runner', '[]', ?2, 'w105-browser-pk', 'pf-cli', 'runner', ?3, ?4, '{}')`,
			m.id, now.Add(-m.age).Format(time.RFC3339Nano), opUser.ID, super); err != nil {
			t.Fatalf("machine row %s: %v", m.id, err)
		}
	}

	// The odd project name the API's charset refuses (?, #, /, %, space
	// and non-ASCII) — seeded by SQL, with its objects created through
	// the API by the row's id.
	f.OddProject = "w105 ?#/% ünïcode"
	if _, err := db.Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES (?1, ?2, ?3, ?3)`,
		"prj_w105_odd", f.OddProject, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("odd project: %v", err)
	}
	f.OddGoal = "odd-named project's exact work item"
	oddRaw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: "prj_w105_odd", Goal: f.OddGoal,
	})
	if err != nil {
		t.Fatalf("odd work item: %v", err)
	}
	var oddWI struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(oddRaw, &oddWI); err != nil || oddWI.ID == "" {
		t.Fatalf("odd work item answer: %v (%s)", err, oddRaw)
	}
	f.OddWI = oddWI.ID
	inst.trackWI(oddWI.ID)
	f.OddMemTitle = "odd project memory"
	var oddMem struct {
		ID string `json:"id"`
	}
	if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
		"project": "prj_w105_odd", "type": "fact.note",
		"title": f.OddMemTitle, "content": "odd body",
		"tags": []string{"odd"}, "visibility": "project",
	}, &oddMem); err != nil || oddMem.ID == "" {
		t.Fatalf("odd memory: %v", err)
	}
	f.OddMem = oddMem.ID
	return f
}
