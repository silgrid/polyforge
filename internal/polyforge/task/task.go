// Package task is the one-shot task agent (workbench#53, the
// re-established #40): the pure core (states, spec/record shapes, fixed
// budgets, id minting, candidate resolution), the streamed
// dispatch/usage seam, the local atomic report store and the in-memory
// manager that ties them together.
//
// The task lane is deliberately small: a human spawns one fresh harness
// agent from a free-text prompt against a project's readable source trees,
// it runs under a hard wall/token budget, and it ends in exactly one of
// four terminal states. There is no queue, no resume, no retry, no task
// type and no spawn-level model override. The only capability gate is the
// write-verb guard: the task process carries PF_TASK_ID, so the daemon
// refuses it every write and the CLI refuses it every write verb — the
// lane's one real gate. There is no OS-level isolation (owner ruling
// 2026-10-26, mem_TdI0isaGmTgG): a task agent's filesystem/network trust is
// a session agent's, and platform-level isolation belongs to workbench#10.
package task

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// State is a task's lifecycle state. The four terminal states are
// distinct on every surface that shows a task; StateRunning is the one
// non-terminal state.
type State string

const (
	StateRunning     State = "running"
	StateCompleted   State = "completed"
	StateCapped      State = "capped"
	StateFailed      State = "failed"
	StateInterrupted State = "interrupted"
)

// Terminal reports whether s is one of the four terminal states.
func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateCapped, StateFailed, StateInterrupted:
		return true
	}
	return false
}

// The fixed limits and budgets of the task lane (workbench#40). They are
// code constants: no `.polyforge.yaml` key may change them and no spawn
// flag may raise them above the ceilings.
const (
	// Limit is the fixed concurrency limit of tasks per daemon, OUTSIDE
	// the job slots. A spawn beyond it is refused immediately; nothing is
	// queued.
	Limit = 2

	// DefaultMaxDuration / DefaultMaxTokens are the budgets a spawn
	// without budget flags gets.
	DefaultMaxDuration = 20 * time.Minute
	DefaultMaxTokens   = 300000

	// MaxDuration / MaxTokens are the code-constant ceilings; a spawn
	// above either is refused naming the ceiling.
	MaxDuration = 60 * time.Minute
	MaxTokens   = 1000000

	// ReportCapBytes is the maximum captured assistant text a report
	// keeps; bytes dropped by the cap mark the report truncated.
	ReportCapBytes = 2 << 20

	// SummaryTailChars is the maximum length of the chat/watcher summary
	// (the trailing slice of the report), one line plus the show pointer.
	SummaryTailChars = 2000

	// RetentionMaxEntries and RetentionAge bound the local report store:
	// the newest 50 entries and the last 7 days are kept, the excess is
	// removed oldest-first. Nothing is ever uploaded.
	RetentionMaxEntries = 50
	RetentionAge        = 7 * 24 * time.Hour
)

// ValidateDuration refuses a non-positive wall budget or one above the
// fixed ceiling, naming the ceiling.
func ValidateDuration(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("--max-duration must be positive (got %s)", d)
	}
	if d > MaxDuration {
		return fmt.Errorf("--max-duration %s is above the 60m ceiling", d)
	}
	return nil
}

// ValidateTokens refuses a non-positive token budget or one above the
// fixed ceiling, naming the ceiling.
func ValidateTokens(n int64) error {
	if n <= 0 {
		return fmt.Errorf("--max-tokens must be positive (got %d)", n)
	}
	if n > MaxTokens {
		return fmt.Errorf("--max-tokens %d is above the %d ceiling", n, MaxTokens)
	}
	return nil
}

// ParseDuration parses a spawn's `--max-duration` value as a Go duration
// and validates it against the ceiling.
func ParseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("--max-duration %q is not a duration (e.g. 20m, 1h30m)", s)
	}
	if err := ValidateDuration(d); err != nil {
		return 0, err
	}
	return d, nil
}

// ParseTokens parses a spawn's `--max-tokens` value as a positive integer
// and validates it against the ceiling.
func ParseTokens(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--max-tokens %q is not an integer", s)
	}
	if err := ValidateTokens(n); err != nil {
		return 0, err
	}
	return n, nil
}

// IDPrefix is the task-id family prefix; ids are `task_` + 16 lowercase
// hex characters, mirroring `chat_`.
const IDPrefix = "task_"

// NewID mints a fresh task id.
func NewID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("task id: %w", err)
	}
	return IDPrefix + hex.EncodeToString(b[:]), nil
}

// ValidID reports whether id has the `task_` + 16 lowercase hex shape.
func ValidID(id string) bool {
	if !strings.HasPrefix(id, IDPrefix) {
		return false
	}
	rest := id[len(IDPrefix):]
	if len(rest) != 16 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Spec is one spawn request: the facts fixed at spawn time. The candidate
// list is resolved live from the workspace yaml (never a spawn-level
// override) and rotates in order inside the spec's one shared budget.
type Spec struct {
	Project    string   `json:"project"`
	Prompt     string   `json:"prompt,omitempty"`
	OriginChat string   `json:"origin_chat,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// MaxDuration is a time.Duration; the JSON name states the unit
	// (nanoseconds), which is what encoding/json emits for it.
	MaxDuration time.Duration `json:"max_duration_ns"`
	MaxTokens   int64         `json:"max_tokens"`
}

// Task is one task record: its spec plus the observed outcome. Report is
// the captured assistant text (up to ReportCapBytes); Truncated is true
// when the cap or a budget cut bytes off. Error carries the harness's raw
// failure wording for a failed task and is empty otherwise.
type Task struct {
	ID        string `json:"id"`
	Spec      `json:"spec"`
	State     State     `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Candidate string    `json:"candidate,omitempty"`
	// WallConsumed is a time.Duration; its JSON name states the unit
	// (nanoseconds), matching MaxDuration.
	WallConsumed   time.Duration `json:"wall_consumed_ns"`
	TokensConsumed int64         `json:"tokens_consumed"`
	Report         string        `json:"report,omitempty"`
	Truncated      bool          `json:"truncated,omitempty"`
	Error          string        `json:"error,omitempty"`
	// Delivered is true once the origin chat acked the terminal summary.
	Delivered bool `json:"delivered,omitempty"`
}

// Clone answers a copy of t (no shared slices) suitable for handing out
// outside the manager's lock.
func (t Task) Clone() Task {
	c := t
	c.Candidates = append([]string(nil), t.Candidates...)
	return c
}

// Age answers how long ago the task was created, relative to now.
func (t Task) Age(now time.Time) time.Duration {
	if t.CreatedAt.IsZero() || now.Before(t.CreatedAt) {
		return 0
	}
	return now.Sub(t.CreatedAt)
}

// Summary answers the chat/watcher summary of a terminal task: the trailing
// ≤SummaryTailChars of the report on one line plus the `pf task show <id>`
// pointer. A truncated report is marked as such. It is never sent to aihub
// or memory.
func (t Task) Summary() string {
	body := strings.TrimSpace(t.Report)
	if len(body) > SummaryTailChars {
		body = tailRunes(body, SummaryTailChars)
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.TrimSpace(body)
	if body == "" {
		body = fmt.Sprintf("task %s %s", t.ID, t.State)
	}
	if t.Truncated {
		body += " [truncated]"
	}
	return fmt.Sprintf("task %s %s: %s — see `pf task show %s`", t.ID, t.State, body, t.ID)
}

// tailRunes answers the trailing n bytes of s cut on a rune boundary: a
// multi-byte UTF-8 character straddling the cut would otherwise become
// invalid (polyforge#40 review N2).
func tailRunes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	tail := s[len(s)-n:]
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 {
		tail = tail[1:]
	}
	return tail
}
