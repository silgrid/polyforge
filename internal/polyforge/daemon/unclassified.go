package daemon

// unclassified.go (polyforge#51): the Host-level list of the error texts
// that fell into the classification FALLBACK — the texts no built-in row
// and no Host-configured additional rule named, the ones the operator
// should turn into a transient_error_patterns / model_error_patterns
// entry. GET /ops/unclassified scans every serving Cell's events, so the
// report is the Host's, cross-Cell.

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// scanUnclassifiedEvents reads one Cell's per-work-item event logs and
// answers the fallback-classified step_failed texts of the window,
// deduplicated by the raw text with a count each (nil map = none). A scan
// error degrades to whatever was read so one damaged file never hides the
// rest; the event log is news, never state (events.go).
func scanUnclassifiedEvents(dataDir string, window time.Duration, now time.Time) map[string]*cli.UnclassifiedItem {
	eventsDir := filepath.Join(dataDir, "events")
	names, err := os.ReadDir(eventsDir)
	if err != nil {
		return nil // no events directory: nothing this Cell ever classified
	}
	var out map[string]*cli.UnclassifiedItem
	for _, n := range names {
		if n.IsDir() || !strings.HasPrefix(n.Name(), "wi-") || !strings.HasSuffix(n.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(eventsDir, n.Name()))
		if err != nil {
			continue // one unreadable file degrades to the rest, never fails the route
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var e protocol.EventEntry
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Kind != "step_failed" || e.At == "" {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, e.At)
			if err != nil || now.Sub(at) > window {
				continue
			}
			var body struct {
				ClassifiedBy string `json:"classified_by"`
				ErrorText    string `json:"error_text"`
			}
			if json.Unmarshal(e.Body, &body) != nil || body.ClassifiedBy != "fallback" || strings.TrimSpace(body.ErrorText) == "" {
				continue
			}
			if out == nil {
				out = map[string]*cli.UnclassifiedItem{}
			}
			if it := out[body.ErrorText]; it != nil {
				it.Count++
				if laterAt(e.At, it.LastAt) {
					it.LastAt = e.At
				}
			} else {
				out[body.ErrorText] = &cli.UnclassifiedItem{Text: body.ErrorText, Count: 1, LastAt: e.At}
			}
		}
		f.Close()
	}
	return out
}

// laterAt answers whether the RFC3339Nano stamp a is later than b, by
// time: the format drops trailing zeros, so "…:30Z" sorts after
// "…:30.1Z" as text though it is earlier. Every stamp here passed the
// scan's own parse.
func laterAt(a, b string) bool {
	ta, _ := time.Parse(time.RFC3339Nano, a)
	tb, _ := time.Parse(time.RFC3339Nano, b)
	return ta.After(tb)
}

// unclassifiedRoute serves GET /ops/unclassified (Host-level, read-only).
// Reach: a human session only — a step or task agent's process tree is
// refused DAEMON-403 (agentRefusal, like GET /profiles): a step agent may
// read /status and /models but never this list (the texts are raw provider
// errors, and the route exists to serve the operator's rule curation).
func (h *Host) unclassifiedRoute(w http.ResponseWriter, r *http.Request) {
	if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	now := time.Now()
	byText := map[string]*cli.UnclassifiedItem{}
	for _, e := range h.sortedCells() {
		// the Cell's daemon is read under the registry lock (a Cell is
		// invalidated under it); the scan's file I/O runs after release
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d == nil {
			continue // an invalid Cell's stale logs are not part of the report
		}
		for text, it := range scanUnclassifiedEvents(h.cellDataDir(e.profile), cli.UnclassifiedWindow, now) {
			if agg := byText[text]; agg != nil {
				agg.Count += it.Count
				if laterAt(it.LastAt, agg.LastAt) {
					agg.LastAt = it.LastAt
				}
			} else {
				byText[text] = it
			}
		}
	}
	items := make([]cli.UnclassifiedItem, 0, len(byText))
	for _, it := range byText {
		items = append(items, *it)
	}
	// the most frequent text first, then the newest
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return laterAt(items[i].LastAt, items[j].LastAt)
	})
	writeJSON(w, http.StatusOK, cli.UnclassifiedReport{
		WindowDays: int(cli.UnclassifiedWindow / (24 * time.Hour)),
		Items:      items,
	})
}
