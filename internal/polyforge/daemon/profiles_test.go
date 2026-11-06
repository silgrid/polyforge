package daemon

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestProfilesRouteAnswersTheEffectiveIndex: GET /profiles names the
// default Cell with its effective workspace index and carries no
// credential.
func TestProfilesRouteAnswersTheEffectiveIndex(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.APIKey = "secret-key-w348"; c.Server = "http://Hub.example:8420/" })
	resp, err := td.client.Profiles()
	if err != nil {
		t.Fatalf("GET /profiles: %v", err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].Profile != "default" || !resp.Profiles[0].Valid {
		t.Fatalf("profiles: %+v", resp.Profiles)
	}
	if resp.Profiles[0].Server != "http://hub.example:8420" {
		t.Fatalf("server must be the normalized spelling: %q", resp.Profiles[0].Server)
	}
	ws := td.cfg.Mapping.Workspaces()
	if len(resp.Index) != len(ws) || resp.Index[0].Workspace != ws[0] || resp.Index[0].Profile != "default" {
		t.Fatalf("index %+v, want the mapping %v under default", resp.Index, ws)
	}
	code, body := w14Raw(t, td.sock, "GET", "/profiles")
	if code != 200 || strings.Contains(body, "secret-key-w348") {
		t.Fatalf("GET /profiles: %d %s", code, body)
	}
	var _ = cli.ProfilesResponse{}
}
