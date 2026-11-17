package cli

// carries: internal/polyforge/cli/verbs_doctor.go, internal/polyforge/cli/w20_doctor_*_test.go

// w20_doctor_default_only_test.go (polyforge#20, AC2): on a machine with
// only the default Cell the doctor's report is the report of old — the
// human output byte for byte, the JSON rows' name, order, status and
// detail unchanged, the Cell-level rows carrying only the added `cell`
// field. The fixture is hermetic: every path the report can print lives
// under one root (substituted into the golden), PATH carries only
// controlled stubs, and the rows that read the host (scratch ctime, the
// filesystem) are pinned through the package's test seams.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w20GoldenRoot substitutes the fixture's root in the golden (captured
// from the pre-change tree, so the root differs per run).
const w20GoldenRoot = "@ROOT@"

// w20GoldenHuman is the pre-change tree's human output on the fixture.
const w20GoldenHuman = "pass   config  loaded from @ROOT@/xdg/polyforge/cli.toml\npass   workspace mapping  1 workspace(s) mapping 1 project(s)\npass   workspace dirs  all 1 mapped workspace(s) exist as directories\npass   workspace layout  every mapped workspace keeps the layout (root: .repo/, capsules/, scratch/, .polyforge/, CLAUDE.md, CLAUDE.local.md, AGENTS.md, go.work, go.work.sum, .polyforge.yaml and dot-entries other than .polyforge.yaml backups; .polyforge/: the legacy v1 repo-map/, watchers/)\npass   workspace scratch  no stray scratch entry, no batch past its retention (raw/ 3 days, tasks/ 14 days, sessions/ 14 days)\npass   capsule retention  capsules, set-aside scenes and external git dirs under @ROOT@/data/git: reclaimable 0 (0 B), retired 0 (0 B), retained 0, unknown 0 \u2014 reclaimable/retired = a wrapped, failed or canceled work item's git dir / set-aside dir, or a wrapped work item's capsule, untouched for more than 14 days and not a daemon job; a wrapped work item's capsule is slimmed to artifacts/ + capsule.json, the others are deleted (sized: what a prune frees)\npass   disk watermark  filesystems holding the mapped workspaces (warn above 70% used): 12% used (100.0 GiB of 1000.0 GiB) \u2014 @ROOT@/ws capsules/ 0 B\npass   workspace repo clones  0 .repo clone unit(s) scanned, all clean or covered (no uncovered change)\npass   light verification fallbacks  no live capsule's latest light verification ran full\nwarn   workspace memory policy  cli.toml declares no [memory_policy.\"@ROOT@/ws\".default] block \u2014 the step prompts carry the built-in default policy; run `pf init @ROOT@/ws` to write an editable copy, then edit it\npass   prerequisites  all five setup tools on PATH: gh, curl, python3, tmux, git\npass   python3  Python 3.9.7 at @ROOT@/bin/python3 (python3 on PATH; the step scripts need >= 3.9)\npass   null device  /dev/null is the character device 1,3\npass   audit tools  the dependency-audit gate's tools are on PATH: npm, govulncheck\npass   host prerequisites  no mapped repo declares a host prerequisite (`host_requires=` in its .polyforge/verify-scope.conf)\npass   server reach  the default Cell's aihub answered through the daemon\npass   daemon socket  daemon socket @ROOT@/w20.sock answers (daemon m-w20g)\nwarn   daemon supervision  the daemon runs without supervision (no tmux session polyforge-daemon, no active polyforge-daemon.service): a crash is not restarted \u2014 restart it under the loop (graceful; running jobs are checkpointed first): pf ops shutdown && while pf ops status >/dev/null 2>&1 || ! flock -n '@ROOT@/data/daemon.lock' true; do sleep 2; done; pf daemon ensure\npass   config reload  no restart-only key is pending\npass   model health  no model-health warnings over the events-retention window\npass   retired env  no retired POLYFORGE_* harness variable in effect\npass   daemon env  the daemon's environment carries no ANTHROPIC_* and no credential-looking name outside the ensure allow-list\npass   daemon command path  the PATH a step runs under (its scripts, the verification's scope engine and its `pf step \u2026`; the harness binary's directory goes in front) is \"@ROOT@/bin\"; resolves: `pf` \u2192 @ROOT@/bin/pf, `git` \u2192 @ROOT@/bin/git\npass   workspace yaml  no mapped workspace carries a .polyforge.yaml; every installed bundle's candidates name their harness\npass   harness claude  no claude CLI needed: no resolved candidate is claude:\u2026 (every step runs on pi)\nskip   harness diagram guidance  no harness installed here (no pi agent dir, ~/.claude or step-agent dir)\npass   workflow lanes  1 lane(s) in @ROOT@/lanes load under workflow contract 1; no queued work item is bound to a missing lane\npass   aihub projects  all 1 mapped project(s) are known to aihub\npass   auth  the api key got past aihub's auth boundary (the project read answered)\nskip   claude statusline  no @ROOT@/home/.claude: Claude Code is not set up here\nskip   pi package  no @ROOT@/home/.pi/agent: pi is not set up here\nskip   claude install layout  no @ROOT@/home/.claude: Claude Code is not set up here\nskip   pi install layout  no @ROOT@/home/.pi/agent: pi is not set up here\npass   v1 harness leftovers  no v1 polyforge artifacts in the pi or Claude Code setup\npass   retired chat state  no chat-subscription state left (chats.json, chat-subs.json, chat-status/, watcher runtime files)\n\nok: true\n"

// w20GoldenJSON is the pre-change tree's --json output on the fixture.
const w20GoldenJSON = "{\"ok\":true,\"checks\":[{\"name\":\"config\",\"status\":\"pass\",\"detail\":\"loaded from @ROOT@/xdg/polyforge/cli.toml\"},{\"name\":\"workspace mapping\",\"status\":\"pass\",\"detail\":\"1 workspace(s) mapping 1 project(s)\"},{\"name\":\"workspace dirs\",\"status\":\"pass\",\"detail\":\"all 1 mapped workspace(s) exist as directories\"},{\"name\":\"workspace layout\",\"status\":\"pass\",\"detail\":\"every mapped workspace keeps the layout (root: .repo/, capsules/, scratch/, .polyforge/, CLAUDE.md, CLAUDE.local.md, AGENTS.md, go.work, go.work.sum, .polyforge.yaml and dot-entries other than .polyforge.yaml backups; .polyforge/: the legacy v1 repo-map/, watchers/)\"},{\"name\":\"workspace scratch\",\"status\":\"pass\",\"detail\":\"no stray scratch entry, no batch past its retention (raw/ 3 days, tasks/ 14 days, sessions/ 14 days)\"},{\"name\":\"capsule retention\",\"status\":\"pass\",\"detail\":\"capsules, set-aside scenes and external git dirs under @ROOT@/data/git: reclaimable 0 (0 B), retired 0 (0 B), retained 0, unknown 0 \u2014 reclaimable/retired = a wrapped, failed or canceled work item's git dir / set-aside dir, or a wrapped work item's capsule, untouched for more than 14 days and not a daemon job; a wrapped work item's capsule is slimmed to artifacts/ + capsule.json, the others are deleted (sized: what a prune frees)\"},{\"name\":\"disk watermark\",\"status\":\"pass\",\"detail\":\"filesystems holding the mapped workspaces (warn above 70% used): 12% used (100.0 GiB of 1000.0 GiB) \u2014 @ROOT@/ws capsules/ 0 B\"},{\"name\":\"workspace repo clones\",\"status\":\"pass\",\"detail\":\"0 .repo clone unit(s) scanned, all clean or covered (no uncovered change)\"},{\"name\":\"light verification fallbacks\",\"status\":\"pass\",\"detail\":\"no live capsule's latest light verification ran full\"},{\"name\":\"workspace memory policy\",\"status\":\"warn\",\"detail\":\"cli.toml declares no [memory_policy.\\\"@ROOT@/ws\\\".default] block \u2014 the step prompts carry the built-in default policy; run `pf init @ROOT@/ws` to write an editable copy, then edit it\"},{\"name\":\"prerequisites\",\"status\":\"pass\",\"detail\":\"all five setup tools on PATH: gh, curl, python3, tmux, git\"},{\"name\":\"python3\",\"status\":\"pass\",\"detail\":\"Python 3.9.7 at @ROOT@/bin/python3 (python3 on PATH; the step scripts need \\u003e= 3.9)\"},{\"name\":\"null device\",\"status\":\"pass\",\"detail\":\"/dev/null is the character device 1,3\"},{\"name\":\"audit tools\",\"status\":\"pass\",\"detail\":\"the dependency-audit gate's tools are on PATH: npm, govulncheck\"},{\"name\":\"host prerequisites\",\"status\":\"pass\",\"detail\":\"no mapped repo declares a host prerequisite (`host_requires=` in its .polyforge/verify-scope.conf)\"},{\"name\":\"server reach\",\"status\":\"pass\",\"detail\":\"the default Cell's aihub answered through the daemon\"},{\"name\":\"daemon socket\",\"status\":\"pass\",\"detail\":\"daemon socket @ROOT@/w20.sock answers (daemon m-w20g)\"},{\"name\":\"daemon supervision\",\"status\":\"warn\",\"detail\":\"the daemon runs without supervision (no tmux session polyforge-daemon, no active polyforge-daemon.service): a crash is not restarted \u2014 restart it under the loop (graceful; running jobs are checkpointed first): pf ops shutdown \\u0026\\u0026 while pf ops status \\u003e/dev/null 2\\u003e\\u00261 || ! flock -n '@ROOT@/data/daemon.lock' true; do sleep 2; done; pf daemon ensure\"},{\"name\":\"config reload\",\"status\":\"pass\",\"detail\":\"no restart-only key is pending\"},{\"name\":\"model health\",\"status\":\"pass\",\"detail\":\"no model-health warnings over the events-retention window\"},{\"name\":\"retired env\",\"status\":\"pass\",\"detail\":\"no retired POLYFORGE_* harness variable in effect\"},{\"name\":\"daemon env\",\"status\":\"pass\",\"detail\":\"the daemon's environment carries no ANTHROPIC_* and no credential-looking name outside the ensure allow-list\"},{\"name\":\"daemon command path\",\"status\":\"pass\",\"detail\":\"the PATH a step runs under (its scripts, the verification's scope engine and its `pf step \u2026`; the harness binary's directory goes in front) is \\\"@ROOT@/bin\\\"; resolves: `pf` \u2192 @ROOT@/bin/pf, `git` \u2192 @ROOT@/bin/git\"},{\"name\":\"workspace yaml\",\"status\":\"pass\",\"detail\":\"no mapped workspace carries a .polyforge.yaml; every installed bundle's candidates name their harness\"},{\"name\":\"harness claude\",\"status\":\"pass\",\"detail\":\"no claude CLI needed: no resolved candidate is claude:\u2026 (every step runs on pi)\"},{\"name\":\"harness diagram guidance\",\"status\":\"skip\",\"detail\":\"no harness installed here (no pi agent dir, ~/.claude or step-agent dir)\"},{\"name\":\"workflow lanes\",\"status\":\"pass\",\"detail\":\"1 lane(s) in @ROOT@/lanes load under workflow contract 1; no queued work item is bound to a missing lane\"},{\"name\":\"aihub projects\",\"status\":\"pass\",\"detail\":\"all 1 mapped project(s) are known to aihub\"},{\"name\":\"auth\",\"status\":\"pass\",\"detail\":\"the api key got past aihub's auth boundary (the project read answered)\"},{\"name\":\"claude statusline\",\"status\":\"skip\",\"detail\":\"no @ROOT@/home/.claude: Claude Code is not set up here\"},{\"name\":\"pi package\",\"status\":\"skip\",\"detail\":\"no @ROOT@/home/.pi/agent: pi is not set up here\"},{\"name\":\"claude install layout\",\"status\":\"skip\",\"detail\":\"no @ROOT@/home/.claude: Claude Code is not set up here\"},{\"name\":\"pi install layout\",\"status\":\"skip\",\"detail\":\"no @ROOT@/home/.pi/agent: pi is not set up here\"},{\"name\":\"v1 harness leftovers\",\"status\":\"pass\",\"detail\":\"no v1 polyforge artifacts in the pi or Claude Code setup\"},{\"name\":\"retired chat state\",\"status\":\"pass\",\"detail\":\"no chat-subscription state left (chats.json, chat-subs.json, chat-status/, watcher runtime files)\"}]}\n"

// w20GoldenFixture builds the hermetic default-only machine: one root
// holding the config, the workspace, the data dir, the HOME, the lanes
// dir, the stub bin and the daemon socket; PATH is the stub bin alone;
// the aihub and the daemon are fakes; scratch ctime and the
// filesystem come from the test seams. It answers the runPf env and the
// root.
func w20GoldenFixture(t *testing.T) (map[string]string, string) {
	t.Helper()
	base := t.TempDir()
	for _, d := range []string{"xdg/polyforge", "ws", "data", "home", "lanes", "bin"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(base, "bin", name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"gh", "curl", "git", "npm", "govulncheck", "pf"} {
		stub(tool, "#!/bin/sh\nexit 0\n")
	}
	stub("python3", "#!/bin/sh\necho 3.9.7\n")
	stub("tmux", "#!/bin/sh\nexit 1\n") // has-session: no supervision loop
	// the fake aihub: the reach read, the ready queue, the queued list
	hub := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/projects":
			io.WriteString(w, `{"items":[]}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/ready-queue"):
			io.WriteString(w, `{"project":"proj","items":[],"running":[],"paused":[]}`)
		case r.URL.Path == "/api/v1/work-items":
			io.WriteString(w, `{"items":[]}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	writeLane(t, filepath.Join(base, "lanes"), "develop", "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n")
	// the fake daemon: one socket under the root, answering GET /status
	// with the fixed facts and /aihub/* through to the fake aihub
	hubURL, err := url.Parse(hub.URL)
	if err != nil {
		t.Fatalf("hub URL %s: %v", hub.URL, err)
	}
	sock := filepath.Join(base, "w20.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	status := `{"machine_id":"m-w20g","workflows_dir":` + jsonBody(filepath.Join(base, "lanes")) +
		`,"command_path":` + jsonBody(filepath.Join(base, "bin")) +
		`,"step_path":` + jsonBody(filepath.Join(base, "bin")) +
		`,"env_names":["HOME","PATH"],"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":1},"reload":{},` +
		`"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},"warnings":[]}}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, status)
	})
	// the data-plane proxy face: /aihub/<path> reaches the fake aihub
	// (the profile header consumed, as the real Host's proxy does)
	toHub := httputil.NewSingleHostReverseProxy(hubURL)
	hubDirector := toHub.Director
	toHub.Director = func(r *http.Request) {
		hubDirector(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
		r.URL.RawPath = ""
		r.Header.Del(protocol.ProfileHeader)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/aihub/") {
			toHub.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	cfg := "server = " + jsonBody(hub.URL) + "\napi_key = \"k\"\ndaemon_socket = " + jsonBody(sock) + "\n\n" +
		"[workspace.\"" + filepath.Join(base, "ws") + "\"]\nprojects = [\"proj\"]\n"
	if err := os.WriteFile(filepath.Join(base, "xdg", "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"XDG_CONFIG_HOME":              filepath.Join(base, "xdg"),
		"HOME":                         filepath.Join(base, "home"),
		"POLYFORGE_DATA_DIR":           filepath.Join(base, "data"),
		"PATH":                         filepath.Join(base, "bin"),
		"POLYFORGE_HARNESS_ROOT":       "",
		"POLYFORGE_OPERATOR_AGENT_DIR": "",
	}
	return env, base
}

// w20GoldenSeams pins the host-reading rows: scratch judged by mtime
// alone (a ctime cannot be backdated) and one filesystem 12% used.
func w20GoldenSeams(t *testing.T) {
	t.Helper()
	origCT := workspace.ChangeTime
	workspace.ChangeTime = func(os.FileInfo) time.Time { return time.Time{} }
	origDisk := diskStat
	diskStat = func(string) (diskFS, error) {
		return diskFS{Dev: 1, Total: 1000 << 30, Used: 100 << 30, Avail: 800 << 30}, nil
	}
	t.Cleanup(func() {
		workspace.ChangeTime = origCT
		diskStat = origDisk
	})
}

// TestW20DoctorDefaultOnly (AC2): a default-only machine's report is the
// report of old — the human output byte for byte (the golden captured
// from the pre-change tree), the JSON rows' name, order, status and
// detail unchanged, the Cell-level rows carrying only the added `cell`
// field.
func TestW20DoctorDefaultOnly(t *testing.T) {
	env, base := w20GoldenFixture(t)
	w20GoldenSeams(t)
	code, human, stderr := runPf(t, env, "doctor")
	if code != 0 {
		t.Fatalf("doctor: exit %d (stderr %s)", code, stderr)
	}
	_, jout, _ := runPf(t, env, "--json", "doctor")
	// the report of old with the two deltas the ticket itself mandates:
	// AC4 moves the retention row's git root into the Cell dir
	// (<data>/git → <data>/cells/default/git), AC1 adds the cell field to
	// the Cell-level JSON rows — everything else is byte for byte
	wantHuman := strings.ReplaceAll(strings.ReplaceAll(w20GoldenHuman, w20GoldenRoot, base),
		filepath.Join(base, "data", "git"), filepath.Join(base, "data", "cells", "default", "git"))
	if human != wantHuman {
		t.Fatalf("the human output drifted from the pre-change report:\n--- want ---\n%s\n--- got ---\n%s", wantHuman, human)
	}
	var gotDoc, wantDoc w20Doc
	if err := json.Unmarshal([]byte(jout), &gotDoc); err != nil {
		t.Fatalf("doctor --json: %v (%s)", err, jout)
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(strings.ReplaceAll(w20GoldenJSON, w20GoldenRoot, base),
		filepath.Join(base, "data", "git"), filepath.Join(base, "data", "cells", "default", "git"))), &wantDoc); err != nil {
		t.Fatalf("the golden JSON does not parse: %v", err)
	}
	if len(gotDoc.Checks) != len(wantDoc.Checks) {
		t.Fatalf("row count %d, want %d", len(gotDoc.Checks), len(wantDoc.Checks))
	}
	hostRows := map[string]bool{"prerequisites": true, "python3": true, "null device": true, "audit tools": true,
		"daemon socket": true, "daemon supervision": true, "retired env": true, "harness diagram guidance": true,
		docChClaudeStatusline: true, docChPiPackage: true, docChClaudeLayout: true, docChPiLayout: true,
		docChV1Leftovers: true, docChRetiredChat: true}
	for i, got := range gotDoc.Checks {
		want := wantDoc.Checks[i]
		if got.Name != want.Name || got.Status != want.Status || got.Detail != want.Detail {
			t.Fatalf("row %d drifted: got %+v, want %+v", i, got, want)
		}
		// the one added field: the Cell-level rows name their Cell; the
		// Host-level rows carry nothing
		if hostRows[got.Name] {
			if got.Cell != "" {
				t.Fatalf("Host-level row %q carries cell %q", got.Name, got.Cell)
			}
			continue
		}
		if got.Cell != "default" {
			t.Fatalf("Cell-level row %q carries cell %q, want \"default\"", got.Name, got.Cell)
		}
	}
	if !gotDoc.OK || !wantDoc.OK {
		t.Fatalf("ok drifted: got %t want %t", gotDoc.OK, wantDoc.OK)
	}
}
