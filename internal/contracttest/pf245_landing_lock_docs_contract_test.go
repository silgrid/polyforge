package test

// pf245_landing_lock_docs_contract_test.go (polyforge#245 r1#2, AC5): the
// lane and user documents describe the merge serialization the code ships
// — the server landing lease (polyforge#247/#248): a lane's declared
// `landing_lock` interval (develop and epic hold it across sync → merge),
// the single-dispatch lease micro's merge and the integration lane's
// revert hold, and the `pf step lock-check` before every real merge —
// never the deleted in-process per-repo merge lock of the daemon.

import (
	"strings"
	"testing"
)

// TestPF245LandingLockLaneDocs pins each document review r1#2 named: none
// keeps the old in-process wording, each states the server lease.
func TestPF245LandingLockLaneDocs(t *testing.T) {
	stale := []string{
		"per-repo merge lock",
		"daemon's merge lock",
		"repo's merge lock",
	}
	for _, c := range []struct {
		file string
		want []string
	}{
		{"workflows/develop/README.md", []string{"`landing_lock: {enter: sync, exit: merge}`", "server landing lease", "`pf step lock-check`"}},
		{"workflows/epic/README.md", []string{"`landing_lock: {enter: sync, exit: merge}`", "server landing lease", "`pf step lock-check`"}},
		{"workflows/_shared/steps/merge.md", []string{"server landing lease", "`pf step lock-check`"}},
		{"docs/bundles.md", []string{"server landing lease", "`landing_lock`"}},
		{"docs/user-guide.md", []string{"server landing lease"}},
		{"workflows/micro/README.md", []string{"single-dispatch server landing lease", "`pf step lock-check`"}},
		{"workflows/integrate/README.md", []string{"single-dispatch server landing lease", "`pf step lock-check`"}},
	} {
		body := w68Flat(t, c.file)
		for _, s := range stale {
			if strings.Contains(body, s) {
				t.Errorf("%s still describes the deleted in-process lock: %q", c.file, s)
			}
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s must state %q", c.file, w)
			}
		}
	}
}
