package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestVersionReportsProtocolCapabilities: `pf --json version` carries the
// multi-Cell protocol capabilities, answered locally (a broken config and no
// daemon change nothing) so a consumer gates on capability, not a version
// comparison.
func TestVersionReportsProtocolCapabilities(t *testing.T) {
	for _, argv := range [][]string{{"--json", "version"}, {"--json", "--version"}, {"--json", "--config", filepath.Join(t.TempDir(), "missing.toml"), "version"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(argv, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("pf %v: exit %d (%s)", argv, code, stderr.String())
		}
		var doc struct {
			Version      string   `json:"version"`
			Capabilities []string `json:"capabilities"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			t.Fatalf("pf %v: %v (%q)", argv, err, stdout.String())
		}
		want := map[string]bool{"cells-v1": true, "config-host-cell-v1": true, "cell-lifecycle-v1": true, "cell-migration-v1": true}
		for _, c := range doc.Capabilities {
			delete(want, c)
		}
		if doc.Version == "" || len(want) != 0 {
			t.Fatalf("pf %v: version %q, missing capabilities %v", argv, doc.Version, want)
		}
	}
}
