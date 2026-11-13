package runtime

// w403_wrap_attrs_test.go ( A2/AC4): the wrapped close carries
// the capsule's hand-off — since workflow contract 4 the call ledger's made
// `wrap-attrs` (c4_wrap_attrs_test.go: develop's landing `integration`) —
// to aihub as the session-end's attrs. A record that cannot be read never
// wraps without it (review finding 4): the close fails closed to blocked,
// its note naming why, so the hand-off is not lost silently.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// The completed workflow whose hand-off cannot be read closes blocked (the
// owner's door), never wrapped without it; a readable one wraps with it.
func TestW403UnreadableHandOffBlocksTheWrap(t *testing.T) {
	for _, tc := range []struct {
		name, status string
	}{{"readable", api.StatusWrapped}, {"unreadable", api.StatusBlocked}} {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub()
			r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			j := seedJob(r, "wi_handoff_"+tc.name)
			initGitCapsule(t, j)
			p := filepath.Join(j.capsuleDir, capsule.ExecutionJSON)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(`{"workflow_status":"completed","steps":{}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.status == api.StatusWrapped {
				ledgerWrites(t, j.capsuleDir, "merge", 7, true, [2]string{"integration", `{"state":"landed","merge_commits":{"app":["m1"]}}`})
			} else if err := os.MkdirAll(filepath.Join(j.capsuleDir, capsule.StepCallsJSONL), 0o755); err != nil {
				// a call ledger that cannot be read (a directory in its place)
				t.Fatal(err)
			}
			j.mu.Lock()
			j.agent = &recordingHandle{}
			j.agentPID = 0
			j.mu.Unlock()
			r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
			msg := recvRT(t, toOB)
			end := hub.lastEnd()
			if end.Status != tc.status || msg.Kind != protocol.RTFinished {
				t.Fatalf("close %q (RT %+v), want %q", end.Status, msg, tc.status)
			}
			switch tc.status {
			case api.StatusWrapped:
				if end.Attrs["integration"] == nil {
					t.Fatalf("the wrap carries the hand-off: %+v", end)
				}
			default:
				if end.Attrs != nil || !strings.Contains(end.Note, "call ledger") || !strings.Contains(end.Note, capsule.StepCallsJSONL) {
					t.Fatalf("the blocked close names the unreadable hand-off and sends no attrs: %+v", end)
				}
			}
		})
	}
}
