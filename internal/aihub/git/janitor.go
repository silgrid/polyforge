package git

// Janitor: the periodic reconciler pass over the bare-repo tree
// (reference.md §8 定期 gc/repack). A simple janitor: every repo whose
// objects have grown past a threshold is gc'd; repositories disappear
// only with the data dir (no auto-deletion of WI chains).

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Janitor runs the periodic maintenance pass.
type Janitor struct {
	Manager *Manager
	Period  time.Duration
	stop    chan struct{}
}

// NewJanitor builds a janitor with the given period.
func NewJanitor(m *Manager, period time.Duration) *Janitor {
	return &Janitor{Manager: m, Period: period, stop: make(chan struct{})}
}

// GCOnce runs one maintenance pass over every bare repo.
func (j *Janitor) GCOnce() error {
	entries, err := os.ReadDir(j.Manager.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".git") {
			continue
		}
		dir := filepath.Join(j.Manager.Root, e.Name())
		if _, err := git(dir, nil, "gc", "--quiet", "--auto"); err != nil {
			// gc is best-effort maintenance: a failing repo logs and the
			// pass continues (pushes still work; the next pass retries).
			log.Printf("git janitor: gc %s: %v", e.Name(), err)
		}
	}
	return nil
}

// Start launches the periodic loop; Stop terminates it.
func (j *Janitor) Start() {
	go func() {
		t := time.NewTicker(j.Period)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := j.GCOnce(); err != nil {
					log.Printf("git janitor: %v", err)
				}
			case <-j.stop:
				return
			}
		}
	}()
}

func (j *Janitor) Stop() { close(j.stop) }
