package driver

// barruns.go: the deliver step's daemon-attested bar-run record
//. Inside a deliver step `pf bar-run -- '<test_bar line>'`
// runs one carried test_bar command and the daemon appends what happened
// — the command as one argv element (the carried line byte-identically, so
// barKey sees exactly the test_bar text), the repo, its cwd, the exit
// status, the duration, the repo HEAD the daemon read itself and the
// calling spawn's ordinal — to one append-only file per work item under the
// daemon data dir:
//
//	<data-dir>/bar-runs/<work-item-id>.jsonl
//
// The file outlives the spawn (a dead spawn's completed runs survive), a
// daemon restart and every re-dispatch of the same deliver run. The driver
// reads it when it judges a deliver result (deliver_facts.go: a run counts
// only at the pushed head, exit 0, under the unchanged window/key rules)
// and clears it only at the existing rewind/re-entry boundary
// (rewindReentryOrder). Another work item's file is never read. A torn or
// malformed line is skipped on read. No lock is held around any of this
// I/O: appends are single O_APPEND writes of one line.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BarRun is one recorded run of a carried test_bar command.
type BarRun struct {
	Repo       string   `json:"repo"`
	SpawnN     int64    `json:"spawn_n"`
	Argv       []string `json:"argv"`
	Cwd        string   `json:"cwd"`
	Exit       int      `json:"exit"`
	DurationMS int64    `json:"duration_ms"`
	Head       string   `json:"head"`
	At         string   `json:"at"`
}

// barRunsDir is the record's directory under the daemon data dir.
const barRunsDir = "bar-runs"

// BarRunsPath answers the work item's record file; "" when dataDir is
// empty (the record is disabled) or the id cannot name one file.
func BarRunsPath(dataDir, wi string) string {
	if dataDir == "" || wi == "" || wi != filepath.Base(wi) || strings.HasPrefix(wi, ".") {
		return ""
	}
	return filepath.Join(dataDir, barRunsDir, wi+".jsonl")
}

// AppendBarRun appends one entry to the work item's record. A previous
// write torn before its newline is terminated first, so the new entry
// stays one readable line.
func AppendBarRun(dataDir, wi string, r BarRun) error {
	path := BarRunsPath(dataDir, wi)
	if path == "" {
		return fmt.Errorf("driver: no bar-run record for work item %q (data dir %q)", wi, dataDir)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if st, err := f.Stat(); err == nil && st.Size() > 0 && !endsWithNewline(path, st.Size()) {
		line = append([]byte("\n"), line...)
	}
	_, err = f.Write(append(line, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// endsWithNewline reports whether the file's last byte is a newline.
func endsWithNewline(path string, size int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, size-1); err != nil {
		return true
	}
	return b[0] == '\n'
}

// ReadBarRuns answers the work item's recorded entries in file order; a
// missing file is no entry, a torn or malformed line is skipped.
func ReadBarRuns(dataDir, wi string) ([]BarRun, error) {
	path := BarRunsPath(dataDir, wi)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BarRun
	rd := bufio.NewReader(bytes.NewReader(raw))
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var r BarRun
			if json.Unmarshal(bytes.TrimSpace(line), &r) == nil && len(r.Argv) > 0 && r.Repo != "" {
				out = append(out, r)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// ClearBarRuns removes the work item's record (a missing one is fine).
func ClearBarRuns(dataDir, wi string) error {
	path := BarRunsPath(dataDir, wi)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
