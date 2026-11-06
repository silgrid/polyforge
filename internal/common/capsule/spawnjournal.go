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
	// Language is the agent language the dispatch was
	// told in its prompt (`en`, `zh-CN`); an entry without one predates
	// #501.
	Language string `json:"language,omitempty"`
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

// The agent languages (`[daemon] agent_language`): the
// language a dispatch is told in its prompt and its journal line records
// (SpawnJournalEntry.Language). DefaultLanguage is what an absent key, or
// a line without a language, means. stepcall names them for the daemon.
const (
	LangEN          = "en"
	LangZhCN        = "zh-CN"
	DefaultLanguage = LangEN
)

// IsLanguage reports whether v is one of the agent languages (exact
// spelling).
func IsLanguage(v string) bool { return v == LangEN || v == LangZhCN }

// DispatchLanguage answers the agent language the journal records for the
// dispatch (step, spawnN): the language its hand-off is stamped in. A
// dispatch without a line, a line without a language (it predates #501)
// or with an unknown one, and an unreadable journal answer
// DefaultLanguage. The last line of the dispatch wins.
func DispatchLanguage(capsuleDir, step string, spawnN int64) string {
	entries, _ := ReadSpawnJournal(capsuleDir)
	lang := ""
	for _, e := range entries {
		if e.StepID == step && int64(e.SpawnN) == spawnN {
			lang = e.Language
		}
	}
	if IsLanguage(lang) {
		return lang
	}
	return DefaultLanguage
}
