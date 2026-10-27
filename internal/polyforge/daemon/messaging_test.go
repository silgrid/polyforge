package daemon

// messaging_test.go: live message delivery. A message is printed and its
// DELIVERY cursor advanced once per message within the 15 s target (the
// server cursor bounds a kill -9 replay to the one undelivered message, and
// never clears unread that no human acknowledged), and the loop stops when
// its context ends.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestMessageLoopDeliversEachMessageOnce(t *testing.T) {
	face := &fakeFace{inbox: &api.MachineInbox{Messages: []api.Message{
		{ID: "msg_1", Seq: 1, ConversationID: "conv_1", SenderID: "u2", Text: "first"},
		{ID: "msg_2", Seq: 2, ConversationID: "conv_1", SenderID: "u2", Text: "second"},
	}}}
	d := &Daemon{
		cfg:  Config{MachineFace: face},
		face: machineFaceState{available: true, role: api.MachineRoleInteractive, multiMachine: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.messageLoop(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		face.mu.Lock()
		n := len(face.deliveries)
		face.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages were not delivered within the target: %d deliveries", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("delivery took %v, over the 15 s target", elapsed)
	}
	face.mu.Lock()
	deliveries := append([]string(nil), face.deliveries...)
	face.mu.Unlock()
	if len(deliveries) != 2 || deliveries[0] != "conv_1/1" || deliveries[1] != "conv_1/2" {
		t.Fatalf("each message must be delivered exactly once in order: %v", deliveries)
	}
}

func TestMessageLoopIdleWhenFaceNotWanted(t *testing.T) {
	face := &fakeFace{inbox: &api.MachineInbox{Messages: []api.Message{
		{ID: "msg_1", Seq: 1, ConversationID: "conv_1", SenderID: "u2", Text: "x"},
	}}}
	d := &Daemon{
		cfg:  Config{MachineFace: face},
		face: machineFaceState{available: true, role: api.MachineRoleInteractive, multiMachine: false},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.messageLoop(ctx)
	time.Sleep(80 * time.Millisecond)
	face.mu.Lock()
	n := len(face.deliveries)
	face.mu.Unlock()
	if n != 0 {
		t.Fatalf("a single-machine daemon consumed messages: %d delivered", n)
	}
}
