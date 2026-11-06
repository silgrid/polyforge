package observe

// a chain push the hub refused for its blob limit is
// recorded as push_failed naming the unpushed range and the refused
// paths, beside the error text that names them too.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

func TestW391PushFailedNamesTheOversizePathAndRange(t *testing.T) {
	h := newFakeHandle("att_1")
	refusal := &gitops.OversizeRefusal{
		Limit: 16777216,
		Range: "1111111111111111111111111111111111111111..2222222222222222222222222222222222222222",
		Blobs: []gitops.RefusedBlob{{Path: "artifacts/ci/trace.zip", Blob: "3333333333333333333333333333333333333333", Size: 30347165, Commit: "4444444444444444444444444444444444444444"}},
		Push:  &api.Error{Code: api.ErrGitInternal, Detail: "remote: aihub: blob exceeds the 16777216-byte limit"},
	}
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
			return "", false, gitops.PushTimings{}, refusal
		}
	})
	m.writeExec(t, 1, "code")
	soon(t, 3*time.Second, "a push_failed event", func() bool { return len(eventsOf(m, EvPushFailed)) >= 1 })
	var body struct {
		Error    string               `json:"error"`
		Range    string               `json:"range"`
		Oversize []gitops.RefusedBlob `json:"oversize"`
	}
	if err := json.Unmarshal(eventsOf(m, EvPushFailed)[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Range != refusal.Range || len(body.Oversize) != 1 || body.Oversize[0] != refusal.Blobs[0] {
		t.Fatalf("push_failed must carry the range and the refused path: %+v", body)
	}
	if !strings.Contains(body.Error, "artifacts/ci/trace.zip") || !strings.Contains(body.Error, refusal.Range) {
		t.Fatalf("the error text must name the path and the range: %q", body.Error)
	}
}
