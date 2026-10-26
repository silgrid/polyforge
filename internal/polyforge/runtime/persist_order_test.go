package runtime

// persist_order_test.go — state.json never ends on an older snapshot than
// the newest one taken: persist numbers its snapshots under the job-table
// lock and a write older than the one on disk is skipped.

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"
)

// persistedIDs answers the work items state.json records.
func persistedIDs(t *testing.T, r *Runtime) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, rec := range loadRecords(t, r) {
		out[rec.WorkItemID] = true
	}
	return out
}

// A persist held up between its snapshot and its write does not land
// last: the persist that snapshotted after it writes first, and the
// delayed one then skips its older snapshot.
func TestPersistOlderSnapshotNeverLandsLast(t *testing.T) {
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapped := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { persistSnapped = nil })
	persistSnapped = func(gen uint64) {
		if gen == 1 {
			close(snapped)
			<-release
		}
	}
	seedJob(r, "wi_po_a")
	older := make(chan error, 1)
	go func() { older <- r.persist() }()
	<-snapped
	seedJob(r, "wi_po_b")
	if err := r.persist(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := recvErr(t, older, "the delayed persist"); err != nil {
		t.Fatal(err)
	}
	if got := persistedIDs(t, r); !got["wi_po_a"] || !got["wi_po_b"] {
		t.Fatalf("state.json = %v, want both jobs (the older snapshot landed last)", got)
	}
}

// Concurrent mutate-then-persist callers with jittered writes: whatever
// the interleaving, the file ends on the final job set, and every caller
// returns only once a snapshot carrying its change is on disk.
func TestPersistConcurrentCallersEndOnNewestSnapshot(t *testing.T) {
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := writeStateFn
	t.Cleanup(func() { writeStateFn = orig })
	var rmu sync.Mutex
	rng := rand.New(rand.NewSource(1))
	writeStateFn = func(path string, data []byte, perm os.FileMode) error {
		rmu.Lock()
		d := time.Duration(rng.Intn(3)) * time.Millisecond
		rmu.Unlock()
		time.Sleep(d)
		return orig(path, data, perm)
	}
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wi := fmt.Sprintf("wi_po_%02d", i)
			seedJob(r, wi)
			if err := r.persist(); err != nil {
				t.Errorf("persist: %v", err)
				return
			}
			st, err := loadState(r.statePath)
			if err != nil {
				t.Errorf("load: %v", err)
				return
			}
			found := false
			for _, rec := range st.Jobs {
				found = found || rec.WorkItemID == wi
			}
			if !found {
				t.Errorf("persist of %s returned before a snapshot carrying it landed", wi)
			}
		}(i)
	}
	wg.Wait()
	if got := persistedIDs(t, r); len(got) != n {
		t.Fatalf("state.json ends with %d jobs, want %d", len(got), n)
	}
}
