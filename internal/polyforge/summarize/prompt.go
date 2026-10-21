package summarize

// prompt.go: the summarize prompt (assumption A12's picks, the owner's
// "~" bounds accepted as the spec's constants) — ONE short plain-text
// line, no markdown, on what the WI is doing right now, answered in the
// claim-time narration language (polyforge#243: the root
// summarize.language, 中文 by default — inserted verbatim) with code
// identifiers, file paths and commands kept as written. Built from a bounded
// tail of the current dispatch's live transcript — at most tailRecords
// records and tailBytes bytes (the old bounds), oldest first — through
// the session-jsonl package's one reader. The held line is the answer
// whitespace-collapsed to one line and hard-capped at lineMaxChars
// characters.

import (
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
)

// The prompt's bounds (the old constants).
const (
	// tailRecords bounds the prompt's transcript tail by records.
	tailRecords = 200
	// tailBytes bounds the tail by bytes: the NEWEST bytes under the
	// cap, oldest records drop first (the old byte-cap fix — a
	// record-only bound once fed multi-hundred-KB prompts and saturated
	// the dispatch's 5-minute timeout).
	tailBytes = 32 * 1024
	// lineMaxChars hard-caps the held summary line: the answer
	// whitespace-collapsed to one line, cut at 200 characters.
	lineMaxChars = 200
)

// BuildPrompt renders the one-line summarize ask over the transcript
// tail at path, answered in language: the ask, then the tail's raw
// records oldest first. Only
// a read failure is an error (the caller lands it raw like any other
// failure); torn and malformed lines are data, never errors.
func BuildPrompt(path, language string) (string, error) {
	recs, err := sessionjsonl.Tail(path, tailRecords)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(recs))
	total := 0
	for _, r := range recs {
		lines = append(lines, r.Raw)
		total += len(r.Raw) + 1
		for total > tailBytes && len(lines) > 1 {
			total -= len(lines[0]) + 1
			lines = lines[1:]
		}
	}
	var b strings.Builder
	b.WriteString("Summarize in ONE short line what this coding-agent work item is doing right now: concrete current activity, plain text, no markdown. Answer in " + language + ". Keep code identifiers, file paths and commands as written. Transcript tail (oldest first):\n")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// HoldLine answers the line the lane holds for one dispatch answer: the
// answer whitespace-collapsed to ONE line (no markdown, no newlines)
// and hard-capped at lineMaxChars characters.
func HoldLine(out string) string {
	line := strings.Join(strings.Fields(out), " ")
	if r := []rune(line); len(r) > lineMaxChars {
		return string(r[:lineMaxChars])
	}
	return line
}
