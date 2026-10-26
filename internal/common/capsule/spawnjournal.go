package capsule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// SpawnJournalFile is the capsule spawn journal under DirRuntimeState:
// one JSON line per step-process derivation, appended by the driver's
// spawn adapter.
const SpawnJournalFile = "spawn-journal.jsonl"

// SpawnJournalEntry is one parsed journal line. Fields a given line does
// not carry stay zero (older lines predate spawn_n / session_handle).
type SpawnJournalEntry struct {
	At            string `json:"at"`
	WorkItem      string `json:"work_item"`
	StepID        string `json:"step_id"`
	SpawnToken    string `json:"spawn_token"`
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	SessionHandle string `json:"session_handle"`
	SpawnN        int    `json:"spawn_n"`
	Resume        bool   `json:"resume"`
	Transcript    string `json:"transcript"`
	// Harness / HarnessSession (polyforge#159): the step harness of the
	// derivation and its harness-native session id (claude's uuid; empty
	// for pi). An entry without a harness predates #159: a pi derivation.
	Harness        string `json:"harness,omitempty"`
	HarnessSession string `json:"harness_session,omitempty"`
}

// ReadSpawnJournal is the one reader of the capsule spawn journal. It is
// loose: a torn or unparsable line is skipped, never fatal. An absent
// journal answers (nil, nil); any other read failure is an error — a
// partially readable ledger must never be silently treated as fresh.
func ReadSpawnJournal(capsuleDir string) ([]SpawnJournalEntry, error) {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, DirRuntimeState, SpawnJournalFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SpawnJournalEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e SpawnJournalEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// SpawnJournalMaxN answers the highest spawn_n the journal records (0
// when absent): the B2 fresh-clone source for the derivation counter.
func SpawnJournalMaxN(capsuleDir string) (int, error) {
	entries, err := ReadSpawnJournal(capsuleDir)
	max := 0
	for _, e := range entries {
		if e.SpawnN > max {
			max = e.SpawnN
		}
	}
	return max, err
}
