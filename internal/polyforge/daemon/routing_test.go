package daemon

import (
	"strings"
	"testing"
)

// TestRoutingNeverFallsBack: an unknown profile and a Cell with no live
// runtime both fail with DAEMON-404 naming the Cell; neither is served by
// another Cell.
func TestRoutingNeverFallsBack(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "broken", invalid: "conflicts with the default Cell: same AIHub server http://x:1"})
	for _, tc := range []struct{ profile, want string }{
		{"ghost", "unknown profile \"ghost\""},
		{"broken", "Cell \"broken\" has no live runtime"},
	} {
		_, err := th.client(tc.profile).ClaimWithModels("wi_x", "normal", nil)
		if err == nil || !strings.Contains(err.Error(), "DAEMON-404") || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("profile %s: %v", tc.profile, err)
		}
	}
	if n := th.cell("default").d.q.LenPending(); n != 0 {
		t.Fatalf("a refused request reached the default Cell's queue (%d pending)", n)
	}
}
