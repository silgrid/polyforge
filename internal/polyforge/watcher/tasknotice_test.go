package watcher

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

type noticeAPI struct {
	*fakeAPI
	pending []protocol.TaskNotice
	acked   []string
}

func (n *noticeAPI) TaskNotices() ([]protocol.TaskNotice, error) { return n.pending, nil }
func (n *noticeAPI) AckTaskNotices(ids []string) error {
	n.acked = append(n.acked, ids...)
	n.pending = nil
	return nil
}

// A task summary is printed once, acked, and (with --wait-decision) ends
// the run like a decision line does.
func TestTaskNoticeIsPrintedAckedAndWakes(t *testing.T) {
	api := &noticeAPI{fakeAPI: &fakeAPI{}, pending: []protocol.TaskNotice{{TaskID: "task_0123456789abcdef", Text: "task task_0123456789abcdef completed: ok — see `pf task show task_0123456789abcdef`"}}}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, api, Options{Out: &out, WaitDecision: true, Sleep: func(context.Context, time.Duration) {}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pf task show task_0123456789abcdef") || len(api.acked) != 1 {
		t.Fatalf("out=%q acked=%v", out.String(), api.acked)
	}
}
