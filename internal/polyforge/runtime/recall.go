package runtime

// recall.go: the F6-A lane — the claim-time memory snapshot. The goal's
// v0-era "recall the lessons ledger first" instruction was in-contract
// unexecutable in v1 (the agent holds no aihub credential and no read
// path existed at all); the F6 ruling answers it in two halves, and this
// file is the STATIC half:
//
//   - at claim time the daemon recalls the project's memories — the goal
//     and labels as the query (recallQuery), the
//     aihub memory search face's ranked any-term mode (match=any) as the
//     index — and writes the
//     top-K hits as a static snapshot into the capsule:
//     runtime-state/recalled-memories.json, which the driver renders
//     into every step prompt as non-binding history
//     (driver/spawn.go recalledMemoriesSection, decision 2026-10-16 G3);
//   - the live half is the daemon's step memory face (daemon/stepmem.go:
//     `pf recall` / `pf remember` inside a step, with the daemon's key).
//
// The snapshot is an ENRICHMENT, never a gate: an unreachable aihub, a
// refused search or a zero-hit query all degrade to an empty list plus a
// notice field — nothing here may fail a spawn.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

const (
	// relRecalledMemories is the snapshot's capsule-relative path.
	relRecalledMemories = capsule.RecalledMemoriesJSON
	// defaultRecallTopK is the snapshot's default hit count (F6 ruling:
	// K=10, configurable through runtime.Config.RecallTopK).
	defaultRecallTopK = 10
	// maxRecallTopK bounds a configured K.
	maxRecallTopK = 50
)

// RecalledMemory is one snapshot hit: the memory's identity plus a
// bounded content excerpt (the full content stays behind the aihub
// read face; the UDS recall channel is the live fetch path).
type RecalledMemory struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// recalledSnapshot is the recalled-memories.json shape. Items is never
// null (an empty recall is an empty list + Notice, a shape the consumer
// can distinguish from a torn file).
type recalledSnapshot struct {
	WorkItemID string           `json:"work_item_id"`
	Query      string           `json:"query"`
	TopK       int              `json:"top_k"`
	Items      []RecalledMemory `json:"items"`
	// Notice carries the degradation reason on an empty recall: a
	// search failure, a zero-hit query, a missing task definition.
	// Empty on a populated recall.
	Notice    string `json:"notice,omitempty"`
	CreatedAt string `json:"created_at"`
}

// recallQuery builds the claim query: the goal and the labels, never
// the content. aihub's any-term mode (match=any) extracts and caps the
// terms itself (memory/search_any.go).
func recallQuery(goal string, labels []string) string {
	return strings.TrimSpace(strings.Join(append([]string{goal}, labels...), " "))
}

// recallTopK answers the effective K for the snapshot (configured,
// defaulted, bounded).
func (r *Runtime) recallTopK() int {
	k := r.cfg.RecallTopK
	if k <= 0 {
		k = defaultRecallTopK
	}
	if k > maxRecallTopK {
		k = maxRecallTopK
	}
	return k
}

// writeRecalledMemories materializes the F6-A snapshot. Best-effort on
// every path: a missing seam (nil — slim installs, most unit tests)
// writes nothing at all; every other failure mode lands as an empty
// list plus a notice, never a failed start.
func (r *Runtime) writeRecalledMemories(j *job) {
	if r.cfg.MemoryRecall == nil {
		return
	}
	snap := recalledSnapshot{
		WorkItemID: j.wi,
		TopK:       r.recallTopK(),
		Items:      []RecalledMemory{},
		CreatedAt:  nowStamp(),
	}
	// The query is the claim's own task definition: goal and labels
	// (recallQuery). j.item is the claim answer's
	// snapshot; a missing one (never in the start path) falls back to an
	// aihub lookup, and even THAT failing degrades to a noticed empty
	// recall.
	item := j.item
	if item == nil {
		if fetched, err := r.hub.GetWorkItem(j.wi); err == nil {
			item = fetched
		}
	}
	if item != nil {
		snap.Query = recallQuery(item.Goal, item.Labels)
	}
	switch {
	case strings.TrimSpace(snap.Query) == "":
		snap.Notice = "no task definition to recall against"
	default:
		// The search is scoped to the work item's own project (audit B5): the
		// snapshot must not draw on every project the daemon credential
		// can read. A populated query implies item != nil (the query is
		// built from it); the nil check keeps the seam honest anyway.
		project := ""
		if item != nil {
			project = item.ProjectID
		}
		hits, err := r.cfg.MemoryRecall(snap.Query, project, snap.TopK)
		if err != nil {
			// aihub unreachable or the search refused: an EMPTY list plus
			// the notice — never a failed spawn.
			snap.Notice = "memory search failed: " + err.Error()
		} else {
			for _, h := range hits {
				snap.Items = append(snap.Items, RecalledMemory{
					ID:      h.ID,
					Type:    h.Type,
					Title:   h.Title,
					Excerpt: h.Excerpt,
				})
			}
			if len(snap.Items) == 0 {
				snap.Notice = "no memories matched the claim query"
			}
		}
	}
	blob, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: recalled-memories snapshot marshal failed: %v\n", j.wi, err)
		return
	}
	if err := atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(relRecalledMemories)), blob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: recalled-memories snapshot write failed: %v\n", j.wi, err)
	}
}
