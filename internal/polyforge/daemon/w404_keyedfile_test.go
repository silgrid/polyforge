package daemon

// w404_keyedfile_test.go: the integration lane's hub
// writes' crash windows as the pending `pf step hub` writes make them
// (ported from the v1 driver's integrate tests, deleted with the daemon's
// integrate actions — workflow contract v2 change 17): a ticket whose
// filing answer was lost is found under its key, and an attrs write whose
// answer was lost is no failure when repeated.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// TestW404AFiledTicketIsFoundUnderItsKey (v1
// TestW404CrashWindowsFileEachTicketOnce): the hub filed a fix ticket but
// its answer was lost; the replayed pending write looks the key up before
// filing and answers the ticket already filed — never a second one.
func TestW404AFiledTicketIsFoundUnderItsKey(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
				map[string]any{"id": "wi_other", "slug": "p#100", "content": "another ticket\n"},
				map[string]any{"id": "wi_f", "slug": "p#101", "content": "the fix\n\n" + ticketKeyLine("wi_batch/full-gate/s1/fix_tickets/0") + "\n"},
			}})
		case "POST":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			c, _ := body["content"].(string)
			posted = append(posted, c)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wi_new", "slug": "p#102"})
		}
	}))
	t.Cleanup(hub.Close)
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k"; c.Hub = aihubclient.New(hub.URL, "k") })
	h := stepHubSink{td.Daemon}
	ref, err := h.File(context.Background(), stepcall.Op{Verb: stepcall.HubFile, Lane: "develop", Key: "wi_batch/full-gate/s1/fix_tickets/0", Project: "p", Goal: "fix it", Content: "the fix"})
	if err != nil || ref != "p#101" || len(posted) != 0 {
		t.Fatalf("the ticket filed before is answered, nothing filed again: %q %v posted %q", ref, err, posted)
	}
	ref, err = h.File(context.Background(), stepcall.Op{Verb: stepcall.HubFile, Lane: "develop", Key: "wi_batch/full-gate/s1/fix_tickets/1", Project: "p", Goal: "fix that", Content: "the other fix"})
	if err != nil || ref != "p#102" || len(posted) != 1 || !strings.Contains(posted[0], ticketKeyLine("wi_batch/full-gate/s1/fix_tickets/1")) {
		t.Fatalf("a new key is filed once, carrying its key line: %q %v posted %q", ref, err, posted)
	}
}

// TestW404ARepeatedAttrsWriteIsNoFailure (v1
// TestW404LostRoundMoveIsNotCountedTwice, ported to the generic attrs
// write of workflow contract 1): a write whose answer was lost is made
// again on the replay; aihub's fence refuses it (the record moved
// already), and the key holding the value written makes the refusal no
// failure — while a write whose value the key does not hold stays
// refused, and so does any refusal but the fence's.
func TestW404ARepeatedAttrsWriteIsNoFailure(t *testing.T) {
	var mu sync.Mutex
	rec := map[string]any{"state": "landed", "repos": []any{"app"}}
	writes := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "PATCH" && r.URL.Path == "/api/v1/work-items/p#7/attrs":
			var u api.AttrsWriteRequest
			_ = json.NewDecoder(r.Body).Decode(&u)
			if u.Key != "integration" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": api.ErrCoreBadInput, "detail": "bad key"})
				return
			}
			if want := string(u.If["integration.state"]); want != "" && want != `"`+rec["state"].(string)+`"` {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": api.ErrCoreConflict, "detail": "attrs.integration.state is not " + want})
				return
			}
			writes++
			rec = map[string]any{}
			_ = json.Unmarshal(u.Value, &rec)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wi_s", "slug": "p#7", "attrs": map[string]any{"integration": rec}})
		case r.Method == "GET" && r.URL.Path == "/api/v1/work-items/p#7":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wi_s", "slug": "p#7", "attrs": map[string]any{"integration": rec}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(hub.Close)
	c := aihubclient.New(hub.URL, "k")
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetMachineIdentity("m_test", key)
	td := newTestDaemon(t, func(cfg *Config) { cfg.Server, cfg.APIKey = hub.URL, "k"; cfg.Hub = c })
	sink := stepHubSink{td.Daemon}
	op := func(value string) stepcall.Op {
		return stepcall.Op{Verb: stepcall.HubAttrs, Target: "p#7", Name: "integration", Value: json.RawMessage(value), If: map[string]any{"integration.state": "landed"}}
	}
	merged := `{"state":"merged","repos":["app"],"batch":"p#50"}`
	if err := sink.Attrs(context.Background(), op(merged)); err != nil || writes != 1 {
		t.Fatalf("the write: %v (writes %d)", err, writes)
	}
	if err := sink.Attrs(context.Background(), op(merged)); err != nil || writes != 1 {
		t.Fatalf("the repeated write is no failure and writes nothing again: %v (writes %d)", err, writes)
	}
	if err := sink.Attrs(context.Background(), op(`{"state":"reverted","repos":["app"]}`)); err == nil || !strings.Contains(err.Error(), "CORE-409") {
		t.Fatalf("a write the key does not hold stays refused: %v", err)
	}
	bad := op(merged)
	bad.Name = "other"
	if err := sink.Attrs(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "CORE-400") {
		t.Fatalf("a refusal other than the fence's stays a failure: %v", err)
	}
}
