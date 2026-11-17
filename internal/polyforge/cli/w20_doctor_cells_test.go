package cli

// carries: internal/polyforge/cli/verbs_doctor.go, internal/polyforge/cli/doctor_*.go, internal/polyforge/cli/verbs_workflow.go, internal/polyforge/cli/w20_*_test.go

// w20_doctor_cells_test.go (polyforge#20): `pf doctor` examines every
// Cell — the default Cell (cli.toml) and each named Cell (cli-*.toml) —
// with every Cell-level daemon read (GET /status, the /aihub proxy)
// carrying that Cell's X-PF-Profile, and the Host-level rows running
// once. The fixture is the dual-Cell shape (polyforge#17's): one fake
// AIHub per Cell and a fake Host socket that routes /aihub/* by the
// profile header, answers GET /status per profile (a machine_id per
// Cell) and records every request's profile header.

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

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// w20Doc decodes the w20 --json report: doctorDoc predates the cell
// field, so this suite reads it.
type w20Doc struct {
	OK     bool `json:"ok"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
		Cell   string `json:"cell"`
	} `json:"checks"`
}

// w20Find answers the FIRST check row with the given name and cell
// ("" = any cell: today's single-row shape).
func w20Find(t *testing.T, doc w20Doc, cell, name string) (string, string) {
	t.Helper()
	for _, c := range doc.Checks {
		if c.Name == name && (cell == "" || c.Cell == cell) {
			return c.Status, c.Detail
		}
	}
	t.Fatalf("no check named %q (cell %q) in %d rows", name, cell, len(doc.Checks))
	return "", ""
}

// w20FakeHub is one Cell's fake AIHub for the doctor's reads: the
// project list (the reach probe), the ready queue (the projects row)
// and the queued work-item list (the workflow lanes row), recording
// every request on its own recorder.
func w20FakeHub(t *testing.T) *w17Hub {
	t.Helper()
	h := &w17Hub{rec: &reqRecorder{}}
	h.srv = newFakeAihub(t, h.rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/projects":
			io.WriteString(w, `{"items":[]}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/ready-queue"):
			io.WriteString(w, `{"project":"`+strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/ready-queue")+`","items":[],"running":[],"paused":[]}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/work-items/"):
			io.WriteString(w, `{"id":"`+strings.TrimPrefix(r.URL.Path, "/api/v1/work-items/")+`","status":"wrapped"}`)
		case r.URL.Path == "/api/v1/work-items":
			io.WriteString(w, `{"items":[]}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	return h
}

// w20CellHost is the fake Host on a unix socket: GET /profiles answers
// the dual-Cell index, GET /status answers per profile header (a
// machine_id per Cell) and is recorded, and ANY /aihub/<path> forwards
// to the AIHub of the X-PF-Profile Cell ("" and "default" → the default
// hub) — the profile header consumed, exactly like the real Host.
func w20CellHost(t *testing.T, def, alpha *w17Hub, wsD, wsA, wf, bin string, rec *reqRecorder) string {
	t.Helper()
	proxies := map[string]*httputil.ReverseProxy{}
	for profile, hub := range map[string]*w17Hub{"default": def, "alpha": alpha} {
		u, err := url.Parse(hub.srv.URL)
		if err != nil {
			t.Fatalf("hub URL %s: %v", hub.srv.URL, err)
		}
		p := httputil.NewSingleHostReverseProxy(u)
		base := p.Director
		p.Director = func(r *http.Request) {
			base(r)
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
			r.URL.RawPath = ""
			r.Header.Del(protocol.ProfileHeader)
		}
		proxies[profile] = p
	}
	statusBody := func(machineID string) string {
		path, _ := json.Marshal(bin + string(os.PathListSeparator) + "/usr/bin:/bin")
		return `{"machine_id":"` + machineID + `","workflows_dir":` + jsonBody(wf) +
			`,"command_path":` + string(path) + `,"step_path":` + string(path) +
			`,"env_names":["HOME","PATH"],"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":1},"reload":{},` +
			`"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},"warnings":[]}}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /profiles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ProfilesResponse{
			Profiles: []ProfileInfo{
				{Profile: "default", Valid: true, State: "serving", Workspaces: []string{wsD}},
				{Profile: "alpha", Valid: true, State: "serving", Workspaces: []string{wsA}},
			},
			Index: []cellcfg.IndexEntry{
				{Profile: "default", Workspace: wsD},
				{Profile: "alpha", Workspace: wsA},
			},
		})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch p := r.Header.Get(protocol.ProfileHeader); p {
		case "", "default":
			io.WriteString(w, statusBody("m-w20d"))
		case "alpha":
			io.WriteString(w, statusBody("m-w20a"))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		profile := r.Header.Get(protocol.ProfileHeader)
		if profile == "" {
			profile = "default"
		}
		p, ok := proxies[profile]
		if !ok || !strings.HasPrefix(r.URL.Path, "/aihub/") {
			http.NotFound(w, r)
			return
		}
		p.ServeHTTP(w, r)
	})
	sock := filepath.Join(shortTempDir(t), "w20.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// w20DualCellFixture builds the dual-Cell machine: cli.toml (the
// default Cell, mapping wsD to dproj) and cli-alpha.toml (mapping wsA
// to aproj), the two fake AIHubs, the fake Host socket, and the runPf
// env. The Host recorder answers the recorded /status profile headers.
func w20DualCellFixture(t *testing.T) (env map[string]string, wsD, wsA string, def, alpha *w17Hub, hostRec *reqRecorder, cfgDir string) {
	t.Helper()
	def, alpha = w20FakeHub(t), w20FakeHub(t)
	wsD, wsA = t.TempDir(), t.TempDir()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	cfgDir = filepath.Join(xdg, "polyforge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n")
	// the bin dir the daemon's step PATH names (command_path/step_path):
	// pf and git resolve under it, so the daemon command path row passes
	bin := t.TempDir()
	for _, tool := range []string{"pf", "git"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hostRec = &reqRecorder{}
	sock := w20CellHost(t, def, alpha, wsD, wsA, wf, bin, hostRec)
	host := "server = " + jsonBody(def.srv.URL) + "\napi_key = \"kd\"\ndaemon_socket = " + jsonBody(sock) + "\n\n" +
		"[workspace.\"" + wsD + "\"]\nprojects = [\"dproj\"]\n"
	cell := "server = " + jsonBody(alpha.srv.URL) + "\napi_key = \"ka\"\n\n" +
		"[workspace.\"" + wsA + "\"]\nprojects = [\"aproj\"]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "cli.toml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "cli-alpha.toml"), []byte(cell), 0o600); err != nil {
		t.Fatal(err)
	}
	env = map[string]string{"XDG_CONFIG_HOME": xdg, "POLYFORGE_DATA_DIR": t.TempDir()}
	return env, wsD, wsA, def, alpha, hostRec, cfgDir
}

// w20RowName answers a human-mode row's check name (the row prints as
// "<status padded to 6> <name>  <detail>": the name is the field
// between the status padding and the double space before the detail).
func w20RowName(line string) string {
	parts := strings.SplitN(line, "  ", 3)
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// w20SectionRows splits the human output into sections: the default
// section (no header) and one list of lines per "== Cell <name>: <file> =="
// header.
func w20Sections(out string) (def []string, named map[string][]string, headers []string) {
	named = map[string][]string{}
	cur := ""
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "ok: ") {
			continue
		}
		if strings.HasPrefix(line, "== Cell ") && strings.HasSuffix(line, " ==") {
			// header line: "== Cell <profile>: <file> =="
			rest := strings.TrimSuffix(strings.TrimPrefix(line, "== Cell "), " ==")
			name := rest[:strings.Index(rest, ":")]
			headers = append(headers, line)
			cur = name
			named[name] = nil
			continue
		}
		if cur == "" {
			def = append(def, line)
		} else {
			named[cur] = append(named[cur], line)
		}
	}
	return def, named, headers
}

// TestW20DoctorEveryCell (AC1): on a dual-Cell machine `pf doctor`
// checks the default Cell and each named Cell once — the Host-level rows
// once, every Cell-level read carrying that Cell's X-PF-Profile (each
// fake AIHub sees only its own Cell's projects), a section per named
// Cell in the human output, the `cell` field on the Cell-level JSON
// rows, and `--profile` narrowing nothing.
func TestW20DoctorEveryCell(t *testing.T) {
	env, _, _, def, alpha, hostRec, cfgDir := w20DualCellFixture(t)

	// ── the report runs green (warn/skip rows never fail it) ──
	code, stdout, stderr := runPf(t, env, "--json", "doctor")
	if code != 0 {
		t.Fatalf("doctor on a healthy dual-Cell machine: exit %d (stdout %s stderr %s)", code, stdout, stderr)
	}
	var doc w20Doc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("doctor --json: %v (stdout %q)", err, stdout)
	}
	if !doc.OK {
		t.Fatalf("ok = false on a healthy dual-Cell machine (%d rows)", len(doc.Checks))
	}

	// ── the JSON rows: the default section is today's full report (the
	// ladder with the Host rows interleaved, then the harness family),
	// then the named Cell's section; the Cell-level rows carry the cell
	// field, the Host-level rows do not
	hostRows := []string{"prerequisites", "python3", "null device", "audit tools", "daemon socket",
		"daemon supervision", "retired env", "harness diagram guidance"}
	cellRows := []string{"workspace mapping", "workspace dirs", "workspace layout", "workspace scratch",
		"capsule retention", "disk watermark", "workspace repo clones", "light verification fallbacks",
		"workspace memory policy", "host prerequisites", "server reach", "config reload", "model health",
		"daemon env", "daemon command path", "workspace yaml", "harness claude", "workflow lanes",
		"aihub projects", "auth"}
	want := []string{"config"}
	want = append(want, cellRows[:9]...) // mapping … workspace memory policy
	want = append(want, hostRows[:4]...) // prerequisites … audit tools
	want = append(want, "host prerequisites", "server reach", "daemon socket", "daemon supervision", "config reload", "model health")
	want = append(want, hostRows[6]) // retired env
	want = append(want, "daemon env", "daemon command path", "workspace yaml", "harness claude")
	want = append(want, hostRows[7]) // harness diagram guidance
	want = append(want, "workflow lanes", "aihub projects", "auth")
	// the harness family belongs to the default section — today's full
	// report — so it prints before the alpha section (Q1=A')
	want = append(want, docChClaudeStatusline, docChPiPackage, docChClaudeLayout, docChPiLayout, docChV1Leftovers, docChRetiredChat)
	want = append(want, "config") // the alpha section's config row
	want = append(want, cellRows...)
	var got []string
	for _, c := range doc.Checks {
		got = append(got, c.Name)
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("row order changed:\n got %v\nwant %v", got, want)
	}
	for _, c := range doc.Checks {
		isCell := false
		for _, n := range append([]string{"config"}, cellRows...) {
			if c.Name == n {
				isCell = true
				break
			}
		}
		if !isCell {
			if c.Cell != "" {
				t.Errorf("Host-level row %q carries cell %q", c.Name, c.Cell)
			}
			continue
		}
		if c.Cell != "default" && c.Cell != "alpha" {
			t.Errorf("Cell-level row %q carries cell %q, want default or alpha", c.Name, c.Cell)
		}
	}
	// the two Cells' own rows: default first, alpha after
	seenAlpha := false
	for _, c := range doc.Checks {
		if c.Cell == "alpha" {
			seenAlpha = true
			continue
		}
		if seenAlpha && c.Cell == "default" {
			t.Fatalf("a default row follows the alpha section: %+v", c)
		}
	}
	// the server reach row speaks each Cell's name
	if st, detail := w20Find(t, doc, "default", "server reach"); st != "pass" || !strings.Contains(detail, "the default Cell's aihub answered") {
		t.Fatalf("default server reach = %s %q", st, detail)
	}
	if st, detail := w20Find(t, doc, "alpha", "server reach"); st != "pass" || !strings.Contains(detail, "the alpha Cell's aihub answered") {
		t.Fatalf("alpha server reach = %s %q", st, detail)
	}

	// ── the human output: the default section, then one section per
	// named Cell, the Host-level rows once ──
	code, out, _ := runPf(t, env, "doctor")
	if code != 0 {
		t.Fatalf("human doctor: exit %d (%s)", code, out)
	}
	humanRows := 0
	counts := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if n := w20RowName(line); n != "" {
			counts[n]++
			humanRows++
		}
	}
	alphaHeader := "== Cell alpha: " + filepath.Join(cfgDir, "cli-alpha.toml") + " =="
	if !strings.Contains(out, alphaHeader) {
		t.Fatalf("no alpha section header %q in:\n%s", alphaHeader, out)
	}
	for _, n := range hostRows {
		if counts[n] != 1 {
			t.Errorf("Host-level row %q prints %d times, want 1", n, counts[n])
		}
	}
	for _, n := range cellRows {
		if counts[n] != 2 {
			t.Errorf("Cell-level row %q prints %d times, want 2 (once per Cell)", n, counts[n])
		}
	}
	if counts["config"] != 2 {
		t.Errorf("config row prints %d times, want 2", counts["config"])
	}
	if humanRows != len(doc.Checks) {
		t.Errorf("human rows %d != json rows %d", humanRows, len(doc.Checks))
	}
	// the alpha section carries only Cell-level rows
	defRows, namedRows, _ := w20Sections(out)
	if len(defRows) == 0 || len(namedRows["alpha"]) != len(cellRows)+1 {
		t.Fatalf("sections: default %d rows, alpha %d rows (want %d)", len(defRows), len(namedRows["alpha"]), len(cellRows)+1)
	}
	for _, line := range namedRows["alpha"] {
		n := w20RowName(line)
		if n == "" || n == "prerequisites" || n == "python3" || n == "null device" || n == "audit tools" ||
			n == "daemon socket" || n == "daemon supervision" || n == "retired env" || n == "harness diagram guidance" {
			t.Errorf("Host-level row inside the alpha section: %q", line)
		}
	}
	// the alpha section's config row names its file
	alphaConfig := strings.Join(namedRows["alpha"], "\n")
	if !strings.Contains(alphaConfig, "pass   config  loaded from "+filepath.Join(cfgDir, "cli-alpha.toml")) {
		t.Errorf("alpha config row does not name its file:\n%s", alphaConfig)
	}

	// ── each Cell's reads reach only its own AIHub: the two hubs each
	// saw their own Cell's projects and nothing of the other Cell's ──
	dProjSeen, aProjSeen := false, false
	for _, r := range def.rec.all() {
		if strings.Contains(r.Path, "aproj") {
			t.Errorf("the default Cell's AIHub saw an alpha read: %s", r.Path)
		}
		if strings.Contains(r.Path, "dproj") {
			dProjSeen = true
		}
	}
	for _, r := range alpha.rec.all() {
		if strings.Contains(r.Path, "dproj") {
			t.Errorf("the alpha Cell's AIHub saw a default read: %s", r.Path)
		}
		if strings.Contains(r.Path, "aproj") {
			aProjSeen = true
		}
	}
	if !dProjSeen || !aProjSeen {
		t.Fatalf("per-Cell project reads missing (default saw dproj: %t, alpha saw aproj: %t)", dProjSeen, aProjSeen)
	}
	// the hubs' reads came through the profile-routed proxy: the default
	// Cell's reads (the default hub's project list) and alpha's each
	// exist, so both Cells' reach probes ran against their own hub
	defReach, alphaReach := false, false
	for _, r := range def.rec.all() {
		if r.Path == "/api/v1/projects" {
			defReach = true
		}
	}
	for _, r := range alpha.rec.all() {
		if r.Path == "/api/v1/projects" {
			alphaReach = true
		}
	}
	if !defReach || !alphaReach {
		t.Fatalf("per-Cell reach probes missing (default %t, alpha %t)", defReach, alphaReach)
	}

	// ── GET /status per Cell: the alpha read carries X-PF-Profile: alpha ──
	alphaStatus, defStatus := false, false
	for _, r := range hostRec.all() {
		if r.Path != "/status" {
			continue
		}
		switch r.Header.Get(protocol.ProfileHeader) {
		case "alpha":
			alphaStatus = true
		case "", "default":
			defStatus = true
		}
	}
	if !alphaStatus {
		t.Fatalf("no GET /status carried X-PF-Profile: alpha (host saw %d requests)", len(hostRec.all()))
	}
	if !defStatus {
		t.Fatalf("no default GET /status")
	}

	// ── --profile narrows nothing: the same sections and rows ──
	code, pOut, _ := runPf(t, env, "--profile", "alpha", "doctor")
	if code != 0 {
		t.Fatalf("doctor --profile alpha: exit %d (%s)", code, pOut)
	}
	if !strings.Contains(pOut, alphaHeader) {
		t.Fatalf("doctor --profile alpha drops the alpha section header:\n%s", pOut)
	}
	if strings.Count(pOut, alphaHeader) != 1 {
		t.Fatalf("doctor --profile alpha repeats the alpha section:\n%s", pOut)
	}
	_, namedP, _ := w20Sections(pOut)
	if len(namedP["alpha"]) != len(namedRows["alpha"]) {
		t.Fatalf("doctor --profile alpha changes the alpha section: %d rows, want %d", len(namedP["alpha"]), len(namedRows["alpha"]))
	}
	// the default section is still there in full, the Host rows once and
	// every Cell-level row twice — the same report as without the flag
	if !strings.Contains(pOut, "pass   config  loaded from "+filepath.Join(cfgDir, "cli.toml")) {
		t.Fatalf("doctor --profile alpha drops the default Cell's rows:\n%s", pOut)
	}
	pCounts := map[string]int{}
	for _, line := range strings.Split(pOut, "\n") {
		if n := w20RowName(line); n != "" {
			pCounts[n]++
		}
	}
	for _, n := range hostRows {
		if pCounts[n] != 1 {
			t.Errorf("under --profile alpha the Host-level row %q prints %d times, want 1", n, pCounts[n])
		}
	}
	for _, n := range cellRows {
		if pCounts[n] != 2 {
			t.Errorf("under --profile alpha the Cell-level row %q prints %d times, want 2", n, pCounts[n])
		}
	}
}
