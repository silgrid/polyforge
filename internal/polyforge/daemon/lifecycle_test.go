package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// TestRemoveKeepsStateAndRestoreChecksIdentity: removing a Cell stops it
// and keeps its state dir; default cannot be removed; a Cell holding work
// cannot be removed; the recorded identity refuses a same-name restore
// over another server with the new-profile route.
func TestRemoveKeepsStateAndRestoreChecksIdentity(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	alpha := th.cell("alpha").d
	alpha.recordIdentity("usr_a")
	dir := alpha.cfg.DataDir
	if id, ok := readIdentity(dir); !ok || id.Server != "http://hub-alpha:1" || id.User != "usr_a" {
		t.Fatalf("identity: %+v %v", id, ok)
	}
	if err := th.removeProfile("default"); err == nil {
		t.Fatal("default must not be removable")
	}
	// a queued submit is held work. The Cell's dispatch tick reads the
	// queue under q.mu, so the test writes it under q.mu too;
	// the entry is due only in an hour, so no tick
	// Takes it while the test holds it queued.
	alpha.q.mu.Lock()
	alpha.q.pending = append(alpha.q.pending, qEntry{WorkItemID: "wi_q", NotBefore: time.Now().Add(time.Hour)})
	alpha.q.mu.Unlock()
	if err := th.removeProfile("alpha"); err == nil || !strings.Contains(err.Error(), "holds work") {
		t.Fatalf("a Cell holding work must not be removed: %v", err)
	}
	alpha.q.mu.Lock()
	alpha.q.pending = nil
	alpha.q.mu.Unlock()
	if err := th.removeProfile("alpha"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the state dir must be kept: %v", err)
	}
	if _, err := th.client("alpha").Status(); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("a removed Cell must refuse its requests: %v", err)
	}
	if why := identityMismatch("alpha", dir, "http://other-hub:1"); !strings.Contains(why, "new profile") {
		t.Fatalf("another server must need a new profile: %q", why)
	}
	if why := identityMismatch("alpha", dir, "http://HUB-ALPHA:1/"); why != "" {
		t.Fatalf("the same server (another spelling) is the same identity: %q", why)
	}
	resp, err := th.client("").Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Profiles {
		if p.Profile == "alpha" && p.State != "removed" {
			t.Fatalf("alpha must read removed: %+v", p)
		}
	}
	var _ = filepath.Join
	var _ = cellcfg.DefaultProfile
}

// TestNamedCellFileWithoutAPIKeyIsReportedNotShadowed: a reload of a
// serving named Cell whose file lost its api_key is refused naming the
// missing key, and GET /profiles says the running Cell is not on its file
// (never a silent run on the removed credential); restoring the key
// clears the notice.
func TestNamedCellFileWithoutAPIKeyIsReportedNotShadowed(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	dir := filepath.Dir(th.cfg.Set.HostPath)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("cli.toml", "server = \"http://hub-default:1\"\napi_key = \"k-default\"\n")
	write("cli-alpha.toml", "server = \"http://hub-alpha:1\"\n")
	if _, err := th.applyProfile("alpha"); err == nil || !strings.Contains(err.Error(), "api_key is not set") {
		t.Fatalf("a reload of a file without api_key must be refused naming the key: %v", err)
	}
	row := th.cell("alpha").d.profileInfo("alpha", "")
	if !strings.Contains(row.Reason, "its file is not applied") || !strings.Contains(row.Reason, "api_key is not set") {
		t.Fatalf("GET /profiles must report the running Cell is off its file: %+v", row)
	}
	ws := filepath.Join(filepath.Dir(dir), "ws-alpha") // the running Cell's mapping (newTestHost)
	write("cli-alpha.toml", "server = \"http://hub-alpha:1\"\napi_key = \"k-alpha\"\n\n[workspace.\""+ws+"\"]\nprojects = [\"proj\"]\n")
	if _, err := th.applyProfile("alpha"); err != nil {
		t.Fatalf("the restored file must reload: %v", err)
	}
	if row := th.cell("alpha").d.profileInfo("alpha", ""); row.Reason != "" {
		t.Fatalf("a restored file clears the notice: %+v", row)
	}
}
