package daemon

// w418_status_version_test.go: GET /status names the
// running daemon's own build version, so a caller (Pi Web's install entry)
// tells a daemon still running the old binary after an install swapped it.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/version"
)

func TestW418StatusReportsDaemonVersion(t *testing.T) {
	td := newTestDaemon(t, nil)
	if got := td.statusSnapshot(false).Version; got == "" || got != version.Version {
		t.Fatalf("status version = %q, want the build's %q", got, version.Version)
	}
}
