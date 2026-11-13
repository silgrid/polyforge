package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestResolveDaemonID(t *testing.T) {
	dir := t.TempDir()
	id, err := ResolveDaemonID(dir, "m-host", false)
	if err != nil || id != "m-host" {
		t.Fatalf("first start: %q %v", id, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "daemon-id")); strings.TrimSpace(string(raw)) != "m-host" {
		t.Fatalf("daemon-id file = %q", raw)
	}
	// the stored id wins over a different hostname default
	if id, err = ResolveDaemonID(dir, "m-renamed", false); err != nil || id != "m-host" {
		t.Fatalf("stored must win: %q %v", id, err)
	}
	// an explicit matching value is fine
	if id, err = ResolveDaemonID(dir, "m-host", true); err != nil || id != "m-host" {
		t.Fatalf("explicit match: %q %v", id, err)
	}
	// an explicit differing value refuses
	if _, err = ResolveDaemonID(dir, "m-other", true); err == nil || !strings.Contains(err.Error(), "refusing to switch identity") {
		t.Fatalf("explicit mismatch must refuse, got %v", err)
	}
}

func TestReqOriginKinds(t *testing.T) {
	var none *reqOrigin
	if o := none.origin(); o.Kind != runtime.OriginChat || o.Owner != "" {
		t.Errorf("nil (manual, no owner): %+v", o)
	}
	if o := (&reqOrigin{Owner: "u"}).origin(); o.Kind != runtime.OriginChat || o.Owner != "u" {
		t.Errorf("manual with owner: %+v", o)
	}
}

// TestCellIdentityIsPerCell: a named Cell records its own daemon-id under
// cells/<profile>, seeded by [daemon] machine_id; a differing configured
// value later refuses that Cell's assembly (never a silent switch), and the
// default Cell's identity is untouched.
func TestCellIdentityIsPerCell(t *testing.T) {
	root := t.TempDir()
	in := func(profile, machineID string) cellBuildInput {
		return cellBuildInput{profile: profile, hostRoot: root, file: cli.Config{Server: "http://hub-" + profile + ":1", Mapping: &mapping.Plan{}, Daemon: cli.DaemonSettings{MachineID: machineID}}}
	}
	cfg, err := cellConfig(Config{}, in("alpha", "m-alpha-1"))
	if err != nil || cfg.MachineID != "m-alpha-1" || cfg.DataDir != filepath.Join(root, "cells", "alpha") {
		t.Fatalf("alpha: %q %q %v", cfg.MachineID, cfg.DataDir, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "cells", "alpha", "daemon-id")); strings.TrimSpace(string(raw)) != "m-alpha-1" {
		t.Fatalf("cells/alpha/daemon-id: %q", raw)
	}
	if _, err := cellConfig(Config{}, in("alpha", "m-alpha-2")); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("a differing machine_id must refuse the Cell: %v", err)
	}
	def, err := cellConfig(Config{}, in("default", ""))
	if err != nil || def.DataDir != filepath.Join(root, "cells", "default") || def.MachineID == "m-alpha-1" {
		t.Fatalf("default: %q %q %v", def.MachineID, def.DataDir, err)
	}
	// unset: the file wins on every later assembly
	again, err := cellConfig(Config{}, in("alpha", ""))
	if err != nil || again.MachineID != "m-alpha-1" {
		t.Fatalf("the recorded id must win: %q %v", again.MachineID, err)
	}
}

// TestHostCellsHoldSeparateMachineKeys: each Cell creates its own machine
// key in its own state directory and reports its heartbeat outcome.
func TestHostCellsHoldSeparateMachineKeys(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	def, alpha := th.cell("default").d, th.cell("alpha").d
	kd, err1 := os.ReadFile(filepath.Join(def.cfg.DataDir, "machine-key"))
	ka, err2 := os.ReadFile(filepath.Join(alpha.cfg.DataDir, "machine-key"))
	if err1 != nil || err2 != nil || string(kd) == string(ka) {
		t.Fatalf("machine keys must be separate per Cell: %v %v equal=%v", err1, err2, string(kd) == string(ka))
	}
	alpha.noteHeartbeat("usr_a", nil)
	th.hubs["default"].setHeartbeatErr(nil)
	resp, err := th.client("").Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Profiles {
		if p.Profile == "alpha" && (p.MachineID != "m-alpha" || p.Heartbeat != "ok") {
			t.Fatalf("alpha's identity row: %+v", p)
		}
	}
}
