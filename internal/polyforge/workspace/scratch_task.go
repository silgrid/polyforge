// scratch_task.go: the scratch directories one background task writes
// into (polyforge#121, owner ruling Q3): the daemon creates them before
// the dispatch — scratch/tasks/<task id>/ for the task's other
// disposable output, and the task's own raw batch
// scratch/raw/<UTC time>-<task id>-<unique suffix>/ for raw data — and
// hands them to the harness both ways: as the environment variables
// EnvScratchDir / EnvScratchRawDir and as the same absolute paths named
// in the dispatched prompt (the task package's # Scratch section). The
// writer is told where to write at dispatch time, by the one who created
// the directories; nothing is left to after-the-fact tidying.

package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnvScratchDir / EnvScratchRawDir are the environment variables a
// dispatched task's harness runs with: its own scratch directory and its
// raw batch. The daemon sets them from its own determined values; the
// inherited environment never carries them (the task runner strips every
// inherited PF_* name).
const (
	EnvScratchDir    = "PF_SCRATCH_DIR"
	EnvScratchRawDir = "PF_SCRATCH_RAW_DIR"
)

// rawBatchName answers one raw batch's directory name: a UTC timestamp,
// the writing run's id and a unique suffix — readable, sortable and
// whole-directory deletable (the naming rule the notes file's layout
// section carries).
func rawBatchName(sourceID string, now time.Time, suffix string) string {
	return now.UTC().Format("20060102T150405Z") + "-" + sourceID + "-" + suffix
}

// randSuffix answers a short unique suffix (lowercase hex).
func randSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TaskScratch creates the scratch directories of the background task
// taskID in the workspace ws and answers both absolute paths:
// <ws>/scratch/tasks/<taskID>/ and
// <ws>/scratch/raw/<UTC time>-<taskID>-<unique suffix>/. scratch/ and its
// fixed categories are created as needed (Init's convention, the one
// ensureScratch applies); a scratch or a category that exists as
// anything but a plain directory is refused, never followed, never moved
// away — and with them the dispatch is refused. taskID must be one safe
// path segment (no separator, no "." or "..").
func TaskScratch(ws, taskID string, now time.Time) (taskDir, rawDir string, err error) {
	if taskID == "" || taskID == "." || taskID == ".." || strings.ContainsAny(taskID, `/\`) {
		return "", "", fmt.Errorf("task scratch: %q is not one safe path segment", taskID)
	}
	scratch, _, err := ensureScratch(ws)
	if err != nil {
		return "", "", err
	}
	taskDir = filepath.Join(scratch, ScratchTasksDir, taskID)
	if st, e := os.Lstat(taskDir); e == nil && !st.IsDir() {
		return "", "", fmt.Errorf("%s exists and is not a plain directory (a symlink or a file)", taskDir)
	} else if e != nil && !os.IsNotExist(e) {
		return "", "", e
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return "", "", err
	}
	suffix, err := randSuffix()
	if err != nil {
		return "", "", err
	}
	rawDir = filepath.Join(scratch, ScratchRawDir, rawBatchName(taskID, now, suffix))
	if err := os.Mkdir(rawDir, 0o755); err != nil {
		return "", "", err
	}
	return taskDir, rawDir, nil
}
