package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// TestBlockedWorkflowNoteCarriesBlockedReason: when the driver parks the
// workflow blocked on its own authority it records blocked_reason in
// execution.json; the blocked close's hub note (and RTFinished's detail)
// carries that reason, bounded, so the owner sees why without opening the
// capsule. A blocked workflow with no recorded reason keeps the plain note.
func TestBlockedWorkflowNoteCarriesBlockedReason(t *testing.T) {
	const plain = "workflow blocked: the owner holds the next move (resume / cancel)"
	long := "host fault: 模型服务连续超时 " + strings.Repeat("x", 2*blockedReasonNoteMax)
	for _, tc := range []struct {
		name, reason string
		want         string // the note's required content beyond the plain note ("" = exactly the plain note)
	}{
		{"with_reason", "  gate verification failed 3 times at its bound  ", "; blocked reason: gate verification failed 3 times at its bound"},
		{"bounded", long, "; blocked reason: host fault: 模型服务连续超时 xxx"},
		{"no_reason", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub()
			r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			j := seedJob(r, "wi_blocked_note_"+tc.name)
			initGitCapsule(t, j)
			execDir := filepath.Join(j.capsuleDir, "runtime-state")
			if err := os.MkdirAll(execDir, 0o755); err != nil {
				t.Fatal(err)
			}
			blob, _ := json.Marshal(map[string]string{
				"workflow_status": capsule.WorkflowStatusBlocked,
				"blocked_reason":  tc.reason,
			})
			if err := os.WriteFile(filepath.Join(execDir, "execution.json"), blob, 0o644); err != nil {
				t.Fatal(err)
			}
			j.mu.Lock()
			j.agent = &recordingHandle{} // the returned driver's handle (pid-less shape)
			j.agentPID = 0
			j.mu.Unlock()

			r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
			msg := recvRT(t, toOB)
			if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
				t.Fatalf("a blocked workflow must close blocked, got %+v", msg)
			}
			end := hub.lastEnd()
			if end.Status != api.StatusBlocked {
				t.Fatalf("SessionEnd status %q, want blocked", end.Status)
			}
			if tc.want == "" {
				if end.Note != plain {
					t.Fatalf("no blocked_reason: the note must stay %q, got %q", plain, end.Note)
				}
				return
			}
			if !strings.HasPrefix(end.Note, plain+tc.want) {
				t.Fatalf("the note must carry the blocked reason:\n got %q\nwant prefix %q", end.Note, plain+tc.want)
			}
			if msg.Detail != end.Note {
				t.Fatalf("RTFinished detail %q differs from the hub note %q", msg.Detail, end.Note)
			}
			if !utf8.ValidString(end.Note) {
				t.Fatalf("the bounded note is not valid UTF-8: %q", end.Note)
			}
			if n := utf8.RuneCountInString(end.Note); n > utf8.RuneCountInString(plain+"; blocked reason: ")+blockedReasonNoteMax+1 {
				t.Fatalf("the note is not bounded: %d runes", n)
			}
		})
	}
}
