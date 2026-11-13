package stepcall

// handoff.go: `pf step handoff` — the statuses a dispatch ends with, the
// body's shape (the seven template sections, the re-entry section, the
// length bound) and the section the daemon appends to artifacts/handoff.md:
// a header it stamps from its own records over the body verbatim. The
// checks here are shape only: the daemon never reads the body for a
// verdict, nor the owner's words for a meaning.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// The verbs of `pf step` (a call line's Verb).
const (
	VerbHandoff    = "handoff"
	VerbAsk        = "ask"
	VerbRecall     = "recall"
	VerbMemoryShow = "memory.show"
	VerbRemember   = "remember"
	// VerbHubPrefix prefixes the hub verbs: hub.get, hub.list, hub.file,
	// hub.attrs, hub.wrap-attrs, hub.batch.
	VerbHubPrefix = "hub."
)

// The statuses a dispatch hands off with.
const (
	StatusDone    = "done"
	StatusFail    = "fail"
	StatusBlocked = "blocked"
	StatusCancel  = "cancel"
	StatusWaive   = "waive"
	StatusRewind  = "rewind"
)

// Statuses is the closed status set, in the design's order.
var Statuses = []string{StatusDone, StatusFail, StatusBlocked, StatusCancel, StatusWaive, StatusRewind}

// IsStatus reports whether s is one of Statuses.
func IsStatus(s string) bool {
	for _, x := range Statuses {
		if x == s {
			return true
		}
	}
	return false
}

// NeedsOwner reports whether status carries out an owner's word and so
// must cite the owner's message (--per-owner).
func NeedsOwner(status string) bool {
	return status == StatusCancel || status == StatusWaive || status == StatusRewind
}

// HandoffArgs is `pf step handoff`'s request: the status, the body's text
// (the CLI reads --body @file), the evidence paths (absolute, or relative
// to the capsule root), the rewind target, the stamp of the owner message
// the status carries out, and a one-line reason.
type HandoffArgs struct {
	Status   string   `json:"status"`
	Body     string   `json:"body"`
	Evidence []string `json:"evidence,omitempty"`
	To       string   `json:"to,omitempty"`
	PerOwner string   `json:"per_owner,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// MaxBodyChars bounds a body, in characters (Unicode code points).
const MaxBodyChars = 3000

// headingRe matches a Markdown ATX heading and captures its text.
var headingRe = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.*?)[ \t#]*$`)

// headingNames answers the text of every ATX heading of body.
func headingNames(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if m := headingRe.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

// headingIs reports whether a heading's text names section: the name
// alone, or the name followed by a separator and more ("结论：通过").
func headingIs(text, section string) bool {
	rest, ok := strings.CutPrefix(text, section)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return strings.ContainsRune(" \t：:（(-—·|", r)
}

// CheckBody checks a body's shape against lang's headings (the language
// the dispatch was told; absent or unknown is DefaultLanguage): valid
// UTF-8, at most MaxBodyChars, a heading for every BodySectionsOf entry,
// and on a re-entry one for ReentrySectionOf. It answers every defect at
// once.
func CheckBody(body string, reentry bool, lang string) error {
	sections, reentrySection := BodySectionsOf(lang), ReentrySectionOf(lang)
	if !utf8.ValidString(body) {
		return errors.New("the body is not valid UTF-8")
	}
	var defects []string
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("the body is empty: it carries the template's sections (%s)", strings.Join(sections, " / "))
	}
	if n := utf8.RuneCountInString(body); n > MaxBodyChars {
		defects = append(defects, fmt.Sprintf("the body is %d characters, over the %d bound — put the detail into files and name their paths", n, MaxBodyChars))
	}
	names := headingNames(body)
	want := append([]string{}, sections...)
	if reentry {
		want = append(want, reentrySection)
	}
	var missing []string
	for _, s := range want {
		found := false
		for _, h := range names {
			if headingIs(h, s) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		msg := fmt.Sprintf("the body lacks the section heading(s) %s", strings.Join(missing, ", "))
		if reentry && contains(missing, reentrySection) {
			msg += fmt.Sprintf(" (this step handed off before: a re-run carries %q, answering the previous round point by point)", reentrySection)
		}
		defects = append(defects, msg)
	}
	if len(defects) > 0 {
		return errors.New(strings.Join(defects, "; "))
	}
	return nil
}

// RepoChange is one task repo's span over a hand-off: the commit it
// started from ("" = unknown), the commit it ended at and the diff stat
// between them.
type RepoChange struct {
	Repo  string
	Start string
	End   string
	Stat  string
}

// Header is what the daemon stamps on a section: its own records, never
// the step's words (but the owner's, verbatim).
type Header struct {
	// Call is the seq of the hand-off's call line.
	Call      int64
	Step      string
	Round     int64
	Spawn     int64
	StartedAt string
	EndedAt   string
	Duration  string
	Model     string
	Status    string
	To        string
	Reason    string
	// Events are the step's events of this round (re-dispatches, model
	// switches), one line each.
	Events []string
	// OwnerMessages are the owner's messages to the step in this round.
	OwnerMessages []capsule.AskLine
	// PerOwner is the owner message the status carries out (nil = none).
	PerOwner *capsule.AskLine
	Repos    []RepoChange
	Evidence []string
	// Language is the dispatch's agent language (its spawn-journal line,
	// capsule.DispatchLanguage): the labels the header is stamped in.
	Language string
}

// RenderSection answers the section the daemon appends: the stamped
// header, in the labels of the header's language, then the body verbatim.
func RenderSection(h Header, body string) string {
	l := LabelsOf(h.Language)
	var b strings.Builder
	status := h.Status
	if h.Status == StatusRewind && h.To != "" {
		status += " → " + h.To
	}
	fmt.Fprintf(&b, "\n---\n\n## "+l.SectionTitle+"\n\n", h.Step, h.Round, h.Spawn, status)
	fmt.Fprintf(&b, "<!-- "+l.StampNote+" -->\n\n", h.Call)
	fmt.Fprintf(&b, "| %s | %s |\n|---|---|\n", l.Item, l.Value)
	row := func(k, v string) {
		if v == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", k, strings.ReplaceAll(v, "|", `\|`))
	}
	row(l.Step, h.Step)
	row(l.Round, fmt.Sprint(h.Round))
	row(l.Dispatch, fmt.Sprint(h.Spawn))
	row(l.Started, h.StartedAt)
	row(l.Ended, h.EndedAt)
	row(l.Duration, h.Duration)
	row(l.Model, h.Model)
	row(l.Status, status)
	if h.Reason != "" {
		row(l.Reason, h.Reason)
	}
	var ev []string
	for _, p := range h.Evidence {
		ev = append(ev, "`"+p+"`")
	}
	row(l.Evidence, strings.Join(ev, ", "))
	fmt.Fprintf(&b, "\n**%s**\n\n", l.RepoCommits)
	if len(h.Repos) == 0 {
		fmt.Fprintf(&b, "- %s\n", l.NoRepo)
	}
	for _, r := range h.Repos {
		start := r.Start
		if start == "" {
			start = l.StartUnknown
		}
		fmt.Fprintf(&b, "- `%s`: `%s` → `%s`"+l.RepoStat+"\n", r.Repo, start, r.End, r.Stat)
	}
	if len(h.Events) > 0 {
		fmt.Fprintf(&b, "\n**%s**\n\n", l.RoundEvents)
		for _, e := range h.Events {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}
	fmt.Fprintf(&b, "\n**%s**\n\n", l.OwnerMessages)
	if len(h.OwnerMessages) == 0 {
		fmt.Fprintf(&b, "- %s\n", l.None)
	}
	for _, m := range h.OwnerMessages {
		writeQuoted(&b, ownerLabel(m), m.Text)
	}
	if h.PerOwner != nil {
		fmt.Fprintf(&b, "\n**%s**\n\n", l.PerOwner)
		writeQuoted(&b, ownerLabel(*h.PerOwner), h.PerOwner.Text)
	}
	fmt.Fprintf(&b, "\n### %s\n\n", l.Body)
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// ownerLabel names one owner message: its stamp, its kind and its writer.
func ownerLabel(m capsule.AskLine) string {
	label := m.At + " · " + m.Kind
	if m.By != "" {
		label += " · " + m.By
	}
	return label
}

// writeQuoted writes one verbatim text under a label, fenced so no line
// of it is read as Markdown (the fence is longer than any tilde run in it).
func writeQuoted(b *strings.Builder, label, text string) {
	fence := "~~~"
	for strings.Contains(text, fence) {
		fence += "~"
	}
	fmt.Fprintf(b, "- %s\n\n%s\n%s\n%s\n\n", label, fence, strings.TrimRight(text, "\n"), fence)
}

// HandoffPath answers a capsule's hand-off document path.
func HandoffPath(capsuleDir string) string { return filepath.Join(capsuleDir, capsule.HandoffMD) }

// AppendHandoff appends one section to the capsule's hand-off document
// (created on the first append, artifacts/ with it) and fsyncs it.
func AppendHandoff(capsuleDir, section string) error {
	path := HandoffPath(capsuleDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(section); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
