package task

// store.go: the local, atomic, bounded report store. One task's metadata
// and report is written as one JSON document via the shared atomicfile
// primitive (temp file + fsync + rename), so a reader sees either no
// report or a complete one, never a partial file. The store prunes
// oldest-first to the newest RetentionMaxEntries entries and the last
// RetentionAge. It never uploads anything: reports and summaries stay on
// this machine.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// ReportsDirName is the report store root under the daemon data-dir.
const ReportsDirName = "task-reports"

// Store is the task report store.
type Store struct {
	dir string
}

// NewStore answers the store under <dataDir>/task-reports.
func NewStore(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, ReportsDirName)}
}

// Dir answers the store's root path.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Save writes t atomically at <dir>/<id>.json (0600). The full report and
// its metadata ride the one document.
func (s *Store) Save(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("task report: empty id")
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("task report %s: %w", t.ID, err)
	}
	if err := atomicfile.Write(s.path(t.ID), raw, 0o600); err != nil {
		return fmt.Errorf("task report %s: %w", t.ID, err)
	}
	return nil
}

// Load answers one stored task. ok=false means no report exists (never
// written, or pruned).
func (s *Store) Load(id string) (Task, bool, error) {
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Task{}, false, nil
		}
		return Task{}, false, err
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, false, fmt.Errorf("task report %s: unreadable: %w", id, err)
	}
	return t, true, nil
}

// List answers every stored task; a missing store directory is an empty
// list (no task has ended yet). Temp files and non-JSON entries are
// ignored.
func (s *Store) List() ([]Task, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Task, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, ok, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Prune removes reports beyond the newest RetentionMaxEntries and the last
// RetentionAge, oldest-first. It answers the number removed. A zero
// CreatedAt is treated as the oldest possible time.
func (s *Store) Prune(now time.Time) (int, error) {
	tasks, err := s.List()
	if err != nil {
		return 0, err
	}
	// newest-first: rank determines the count bound.
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	removed := 0
	for i, t := range tasks {
		tooOld := t.CreatedAt.IsZero() || now.Sub(t.CreatedAt) > RetentionAge
		tooMany := i >= RetentionMaxEntries
		if !tooOld && !tooMany {
			continue
		}
		if err := os.Remove(s.path(t.ID)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
