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

import "time"

// EnvScratchDir / EnvScratchRawDir are the environment variables a
// dispatched task's harness runs with: its own scratch directory and its
// raw batch. The daemon sets them from its own determined values; the
// inherited environment never carries them (the task runner strips every
// inherited PF_* name).
const (
	EnvScratchDir    = "PF_SCRATCH_DIR"
	EnvScratchRawDir = "PF_SCRATCH_RAW_DIR"
)

func TaskScratch(ws, taskID string, now time.Time) (taskDir, rawDir string, err error) {
	// signature prelude (polyforge#121 u2): the body lands with the tests.
	return "", "", nil
}
