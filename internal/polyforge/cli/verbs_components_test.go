// carries: internal/polyforge/cli/verbs_components.go,
//
//	internal/polyforge/cli/runtime_layout.go
package cli

// verbs_components_test.go — polyforge#190 (84-inplace-upgrade), u5:
// `pf components`'s own face. The verb is judged against the ticket's
// settled shape: inside the image it drives the real transaction
// through the daemon's two Host-level routes (the drain and the
// restart ask), answers check/status from the volume's own records,
// lays the cancel marker and refuses on a host naming install.sh. The
// transactions themselves are u2's and u3's (their own files); this
// file pins the wiring, never the transaction's rules again.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/components"
)

// componentsTree writes one complete version directory (the five
// component categories, the manifest of the exported contract) at
// dir, the way the components package's own fixtures do.
func componentsTree(t *testing.T, dir, version, minBase string) {
	t.Helper()
	w := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	w(components.PathPF, "#!/bin/sh\n", 0o755)
	w("package.json", fmt.Sprintf(`{"name": "polyforge", "version": %q}`+"\n", version), 0o644)
	w("skills/polyforge/SKILL.md", "# the routing skill\n", 0o644)
	w("workflows/develop/README.md", "# the develop lane\n", 0o644)
	w(filepath.Join(components.PathPiWeb, "package.json"), `{"name": "@silgrid/pi-web", "version": "0.2.4"}`+"\n", 0o644)
	w(filepath.Join(components.PathPiWeb, "app/page.js"), "export default function Page() {}\n", 0o644)
	w(filepath.Join(components.PathHarnessPi, "package.json"), `{"name": "polyforge-harness-pi", "private": true}`+"\n", 0o644)
	w(filepath.Join(components.PathHarnessClaude, "package.json"), `{"name": "polyforge-harness-claude", "private": true}`+"\n", 0o644)
	m := &components.Manifest{
		SchemaVersion: components.SchemaVersion,
		Version:       version,
		Commit:        "0123abcd4567",
		Platform:      "linux/amd64",
		ChannelRepo:   "silgrid/polyforge",
		MinBase:       minBase,
	}
	for _, name := range components.ComponentNames {
		path, _ := components.PathOfComponent(name)
		c := components.Component{Name: name, Path: path, Version: version}
		if name == components.ComponentPiWeb {
			c.Version = "0.2.4"
		}
		m.Components = append(m.Components, c)
	}
	if err := m.FillDigests(dir); err != nil {
		t.Fatalf("FillDigests: %v", err)
	}
	if err := components.WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
}

// setWorkstationVars points the CLI's three layout seams at the
// fixture: the seed root (with its manifest — the one fact the image
// judgment reads), the components volume and the bootstrap pf. The
// cleanup restores production's values.
func setWorkstationVars(t *testing.T, root string) {
	t.Helper()
	seedRoot := t.TempDir()
	componentsTree(t, filepath.Join(seedRoot, "releases", "v0.2.4-dev.1"), "v0.2.4-dev.1", "v0.2.4-dev.1")
	base := filepath.Join(t.TempDir(), "pf")
	if err := os.WriteFile(base, []byte("#!/bin/sh\necho v0.2.4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSeed, oldRoot, oldBase := workstationSeedRoot, componentsVolumeRoot, workstationBasePf
	workstationSeedRoot, componentsVolumeRoot, workstationBasePf = seedRoot, root, base
	t.Cleanup(func() {
		workstationSeedRoot, componentsVolumeRoot, workstationBasePf = oldSeed, oldRoot, oldBase
	})
}

// componentsRoot answers one components volume with current v0.2.4.
func componentsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	componentsTree(t, filepath.Join(root, components.ReleasesDirname, "v0.2.4"), "v0.2.4", "v0.2.4")
	if err := os.Symlink(filepath.Join(components.ReleasesDirname, "v0.2.4"), filepath.Join(root, components.CurrentLinkname)); err != nil {
		t.Fatal(err)
	}
	return root
}

// channelStub stands the channel repository's latest-release lookup up.
func channelStub(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/silgrid/polyforge/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, tag)
	})
	srv := httptest.NewServer(mux)
	components.SetChannelBasesForTest(srv.URL, srv.URL)
	t.Cleanup(func() {
		srv.Close()
		components.SetChannelBasesForTest("https://api.github.com", "https://github.com")
	})
	return srv
}

// daemonForComponents answers the two Host-level routes the verb drives.
func daemonForComponents(t *testing.T) (*daemonFixture, map[string]string) {
	t.Helper()
	d := newDaemonFixture(t)
	d.answer("POST", "/ops/drain", `{"upgrading":true,"forced":false,"detail":"the upgrade drain is set and the Host is idle: upgrade now"}`)
	d.answer("POST", "/ops/restart", `{"restarting_for_upgrade":true}`)
	return d, map[string]string{"PF_DAEMON_SOCKET": d.sock}
}

// TestComponentsVerbRefusesOnAHost pins the entry point's boundary: a
// host (no seed manifest) is refused with the pointer to the installer
// that owns it, before anything else runs.
func TestComponentsVerbRefusesOnAHost(t *testing.T) {
	hostSeed := t.TempDir() // a seed root with no manifest: the host judgment
	old := workstationSeedRoot
	workstationSeedRoot = hostSeed
	t.Cleanup(func() { workstationSeedRoot = old })

	for _, sub := range []string{"check", "status", "upgrade", "rollback", "cancel"} {
		code, _, stderr := runPf(t, nil, "components", sub)
		if code == 0 {
			t.Fatalf("components %s on a host must refuse", sub)
		}
		if !strings.Contains(stderr, "install.sh") || !strings.Contains(stderr, "workstation") {
			t.Fatalf("components %s's refusal must name the workstation image and the host installer: %q", sub, stderr)
		}
	}
}

// TestComponentsVerbCheckAnswersCurrentAndLatest pins check: the
// current version from the volume, the channel's latest release, and
// the upgradable judgement between them — one JSON shape, one text
// shape.
func TestComponentsVerbCheckAnswersCurrentAndLatest(t *testing.T) {
	root := componentsRoot(t)
	setWorkstationVars(t, root)
	channelStub(t, "v0.2.5")
	d, env := daemonForComponents(t)
	_ = d

	code, out, stderr := runPf(t, env, "--json", "components", "check")
	if code != 0 {
		t.Fatalf("components check: exit %d (%s)", code, stderr)
	}
	var got struct {
		Current     string `json:"current"`
		Latest      string `json:"latest"`
		ChannelRepo string `json:"channelRepo"`
		Upgradable  bool   `json:"upgradable"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("components check --json answered no JSON: %v (%s)", err, out)
	}
	if got.Current != "v0.2.4" || got.Latest != "v0.2.5" || got.ChannelRepo != "silgrid/polyforge" || !got.Upgradable {
		t.Fatalf("components check --json answered %+v", got)
	}
	// the plain text names both versions
	code, out, stderr = runPf(t, env, "components", "check")
	if code != 0 {
		t.Fatalf("components check: exit %d (%s)", code, stderr)
	}
	if !strings.Contains(out, "v0.2.4") || !strings.Contains(out, "v0.2.5") {
		t.Fatalf("components check must name the current and the latest: %q", out)
	}
}

// TestComponentsVerbUpgradeThroughTheFace pins the wiring: the verb
// drives the real transaction — the seed staged, the drain posted
// Host-level (no profile), the switch landed, the restart asked, the
// volume's status recorded — and answers with the result.
func TestComponentsVerbUpgradeThroughTheFace(t *testing.T) {
	root := componentsRoot(t)
	setWorkstationVars(t, root)
	seedDir := filepath.Join(t.TempDir(), "v0.2.5")
	componentsTree(t, seedDir, "v0.2.5", "v0.2.4")
	d, env := daemonForComponents(t)

	code, out, stderr := runPf(t, env, "components", "upgrade", "--version", "v0.2.5", "--seed-dir", seedDir)
	if code != 0 {
		t.Fatalf("components upgrade: exit %d (%s)\n%s", code, stderr, out)
	}
	if !strings.Contains(out, "v0.2.5") {
		t.Fatalf("the upgrade's answer must name the version it landed: %q", out)
	}
	target, err := components.CurrentTarget(root)
	if err != nil || target != filepath.Join(components.ReleasesDirname, "v0.2.5") {
		t.Fatalf("current must point at v0.2.5, got %s (%v)", target, err)
	}
	// the drain and the restart were both asked, Host-level
	seen := map[string]capturedRequest{}
	for _, rec := range d.rec.all() {
		seen[rec.Method+" "+rec.Path] = rec
	}
	drainRec, ok := seen["POST /ops/drain"]
	if !ok {
		t.Fatal("the upgrade never posted the drain")
	}
	if strings.TrimSpace(drainRec.Body) != `{"upgrade":true,"force":false}` {
		t.Fatalf("the drain post was %s, want the unforced upgrade drain", drainRec.Body)
	}
	if drainRec.Header.Get("X-PF-Profile") != "" {
		t.Fatal("the drain post carried a profile — the components verb is Host-level")
	}
	if _, ok := seen["POST /ops/restart"]; !ok {
		t.Fatal("the switch was never followed by the restart ask")
	}
	// the volume's status record says done
	s, err := components.ReadUpgradeStatus(root)
	if err != nil || s == nil || s.State != components.UpgradeStateDone || s.To != "v0.2.5" {
		t.Fatalf("the volume's status after the upgrade: %+v (%v)", s, err)
	}
}

// TestComponentsVerbRollbackSwapsBackThroughTheFace pins the rollback's
// wiring: after an upgrade, the verb swaps back through the same two
// routes and the volume's record names the rollback.
func TestComponentsVerbRollbackSwapsBackThroughTheFace(t *testing.T) {
	root := componentsRoot(t)
	setWorkstationVars(t, root)
	seedDir := filepath.Join(t.TempDir(), "v0.2.5")
	componentsTree(t, seedDir, "v0.2.5", "v0.2.4")
	d, env := daemonForComponents(t)

	if code, _, stderr := runPf(t, env, "components", "upgrade", "--version", "v0.2.5", "--seed-dir", seedDir); code != 0 {
		t.Fatalf("the upgrade before the rollback: exit %d (%s)", code, stderr)
	}
	nBefore := len(d.rec.all())
	code, out, stderr := runPf(t, env, "components", "rollback", "--force")
	if code != 0 {
		t.Fatalf("components rollback: exit %d (%s)\n%s", code, stderr, out)
	}
	target, err := components.CurrentTarget(root)
	if err != nil || target != filepath.Join(components.ReleasesDirname, "v0.2.4") {
		t.Fatalf("the rollback must put current back on v0.2.4, got %s (%v)", target, err)
	}
	posts := len(d.rec.all()) - nBefore
	if posts < 2 {
		t.Fatalf("the rollback must drive the drain and the restart again, got %d posts", posts)
	}
	s, err := components.ReadUpgradeStatus(root)
	if err != nil || s == nil || s.State != components.UpgradeStateDone || s.Operation != components.OperationRollback || s.To != "v0.2.4" {
		t.Fatalf("the volume's status after the rollback: %+v (%v)", s, err)
	}
}

// TestComponentsVerbCancelAndStatus pins the two light subcommands:
// cancel lays the volume's marker (the run's wait consumes it), and
// status answers the volume's own records — current, previous and the
// recorded run.
func TestComponentsVerbCancelAndStatus(t *testing.T) {
	root := componentsRoot(t)
	setWorkstationVars(t, root)
	d, env := daemonForComponents(t)

	// a waiting run: cancel lays the volume's marker — the waiting
	// transaction consumes it and asks for the daemon restart itself.
	if err := components.WriteUpgradeStatus(root, &components.UpgradeStatus{
		Operation: components.OperationUpgrade, State: components.UpgradeStateWaiting,
		Phase: "drain", From: "v0.2.4", To: "v0.2.5",
	}); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runPf(t, env, "components", "cancel")
	if code != 0 {
		t.Fatalf("components cancel: exit %d (%s)", code, stderr)
	}
	if !components.UpgradeCancelRequested(root) {
		t.Fatal("cancel of a waiting run must lay the volume's marker")
	}
	for _, rec := range d.rec.all() {
		if rec.Path == "/ops/restart" {
			t.Fatal("cancel of a waiting run must not restart the daemon itself — the transaction does")
		}
	}

	// no waiting run: the marker a crashed run might have left is
	// cleared and the daemon is asked to restart — the documented
	// recovery for a drain nothing stands down.
	code, _, stderr = runPf(t, env, "components", "cancel")
	if code != 0 {
		t.Fatalf("components cancel without a waiting run: exit %d (%s)", code, stderr)
	}
	if components.UpgradeCancelRequested(root) {
		t.Fatal("cancel without a waiting run must clear a stale marker")
	}
	seen := map[string]bool{}
	for _, rec := range d.rec.all() {
		seen[rec.Method+" "+rec.Path] = true
	}
	if !seen["POST /ops/restart"] {
		t.Fatal("cancel without a waiting run must ask the daemon to restart (the drain ends with it)")
	}

	code, out, stderr := runPf(t, env, "--json", "components", "status")
	if code != 0 {
		t.Fatalf("components status: exit %d (%s)", code, stderr)
	}
	var got struct {
		Current  string                    `json:"current"`
		Previous *string                   `json:"previous"`
		Status   *components.UpgradeStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("components status --json answered no JSON: %v (%s)", err, out)
	}
	if got.Current != "v0.2.4" || got.Previous != nil {
		t.Fatalf("components status --json answered current=%q previous=%v", got.Current, got.Previous)
	}
	if got.Status == nil || got.Status.State != components.UpgradeStateWaiting {
		t.Fatalf("status must answer the volume's recorded run: %+v", got.Status)
	}
}

// TestComponentsVerbFaceParsesBusyCells pins the busy answer's reading:
// a 409's own words name the Cells that hold work, and the waiting
// record shows exactly them.
func TestComponentsVerbFaceParsesBusyCells(t *testing.T) {
	detail := "the upgrade drain is set, but Cell(s) default, alpha still hold work (jobs of any state, queued submits or in-flight admissions) — new work is refused from now on"
	got := busyCellsOf(detail)
	if len(got) != 2 || got[0] != "default" || got[1] != "alpha" {
		t.Fatalf("the busy cells of a 409 must be read from its own words, got %v", got)
	}
	if busy := busyCellsOf("the upgrade drain is set and the Host is idle: upgrade now"); busy != nil {
		t.Fatalf("an idle answer names no busy cell, got %v", busy)
	}
}
