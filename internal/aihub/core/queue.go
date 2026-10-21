package core

// Ready queue: the executable segment (items) the daemon's auto-pull
// consumes, plus running / paused visibility segments. Ordering inside
// every segment: priority → created_at.

import (
	"database/sql"
	"errors"
	"sort"

	"github.com/silgrid/polyforge/internal/common/api"
)

// ReadyQueue is the response shape of GET /api/v1/projects/{p}/ready-queue.
type ReadyQueue struct {
	Project string   `json:"project"`
	Items   []WIView `json:"items"` // executable: queued, unblocked, not a registration ticket
	Running []WIView `json:"running"`
	Paused  []WIView `json:"paused"`
}

var priorityRank = map[string]int{
	api.PriorityUrgent: 0, api.PriorityHigh: 1, api.PriorityNormal: 2, api.PriorityLow: 3,
}

func queueLess(a, b *WIView) bool {
	ra, rb := priorityRank[a.Priority], priorityRank[b.Priority]
	if ra != rb {
		return ra < rb
	}
	return a.CreatedAt < b.CreatedAt
}

// ReadyQueue builds the queue for a project (name or id). A WI is
// executable when: status queued, waiting_on empty, no unfinished
// `blocks` dependency edge (only a wrapped blocker releases, #161), and every repo its inventory names is still
// one of the project's repos (reference.md §2.1; else its claim would be refused
// INVENTORY_REPO_GONE on every drain wave).
func (m *WIManager) ReadyQueue(project string) (*ReadyQueue, error) {
	var projectID, projectName string
	err := m.db.QueryRow(`SELECT id, name FROM projects WHERE name=?1 OR id=?1`, project).
		Scan(&projectID, &projectName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, Fail(api.ErrCoreNotFound, "", "project %q", project)
		}
		return nil, err
	}
	projectRepos, err := projectReposTx(m.db, projectID)
	if err != nil {
		return nil, err
	}
	inProject := map[string]bool{}
	for _, pr := range projectRepos {
		inProject[pr.Name] = true
	}
	q := &ReadyQueue{
		Project: projectName,
		Items:   []WIView{},
		Running: []WIView{},
		Paused:  []WIView{},
	}
	// One read of every candidate: the WorkItem, its public session block
	// (m4), the unfinished blockers flag (#161) and the frozen inventory
	// (reference.md §2.1).
	rows, err := m.db.Query(wiSelect+`, EXISTS(SELECT 1 FROM wi_dependencies d
		JOIN work_items b ON b.id=d.blocking_wi_id
		WHERE d.blocked_wi_id=w.id AND d.kind='blocks' AND b.status <> 'wrapped')`+wiFrom+`
		WHERE w.project_id=? AND w.status IN ('queued','running','paused') ORDER BY w.rowid`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cand struct {
		view    WIView
		blocked bool
	}
	var cands []cand
	for rows.Next() {
		var c cand
		v, err := scanWorkItem(rows.Scan, &c.blocked)
		if err != nil {
			return nil, err
		}
		c.view = *v
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, c := range cands {
		waiting := c.view.WaitingOn != nil && *c.view.WaitingOn != ""
		switch c.view.Status {
		case api.StatusRunning:
			q.Running = append(q.Running, c.view)
		case api.StatusPaused:
			q.Paused = append(q.Paused, c.view)
		case api.StatusQueued:
			if c.view.BundleName == nil || *c.view.BundleName == "" {
				continue // registration tickets never execute (#17)
			}
			if waiting || c.blocked {
				continue // blocked-visible: not executable
			}
			gone, err := inventoryRepoGone(c.view.inv, inProject)
			if err != nil {
				return nil, err
			}
			if !gone { // reference.md §2.1: a declared repo left the project
				q.Items = append(q.Items, c.view)
			}
		}
	}
	for _, list := range [][]WIView{q.Items, q.Running, q.Paused} {
		sort.SliceStable(list, func(i, j int) bool { return queueLess(&list[i], &list[j]) })
	}
	return q, nil
}
