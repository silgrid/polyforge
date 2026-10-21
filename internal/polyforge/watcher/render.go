package watcher

import (
	"fmt"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// Labels of the derived WI state.
const (
	StateQueued   = "queued/preparing"
	StateRunning  = "running"
	StateNeeds    = "needs input"
	maxFieldWidth = 60
)

// StateLabel derives the one-word state. A WI the daemon holds nothing for
// yet (submitted, not registered with a monitor) is queued/preparing — a
// normal transient, never an error (#98/#101).
func StateLabel(s *protocol.WatchSnapshot) string {
	if sr, ok := statusRules[s.HubStatus]; ok && s.HubStatus != "" {
		return sr.Class // closed by hand: the hub status outranks a stale execution.json
	}
	ex := s.Execution
	if ex == nil {
		return StateQueued
	}
	if sr, ok := statusRules[ex.WorkflowStatus]; ok {
		return sr.Class
	}
	if ex.PendingQuestion != nil && ex.PendingQuestion.ID != "" {
		return StateNeeds
	}
	if ex.WorkflowStatus == "" || ex.WorkflowStatus == "running" {
		return StateRunning
	}
	return clean(ex.WorkflowStatus)
}

// ProgressLine is one short line per WI for the progress state file: the
// step and its age, the last work signal, and the optional command / files /
// tool-call fields — only what the daemon actually reported. Everything is
// mechanical; step-agent text (command, files) is sanitized and truncated,
// and the line goes to a terminal status area, never into an agent context.
func ProgressLine(s *protocol.WatchSnapshot, now time.Time) string {
	var b strings.Builder
	b.WriteString(safeID(api.DisplayName(s.Slug, s.WorkItem)))
	b.WriteString(" ")
	b.WriteString(StateLabel(s))
	if ex := s.Execution; ex != nil && ex.CurrentStep != "" {
		fmt.Fprintf(&b, " step=%s", clean(ex.CurrentStep))
		if st, ok := ex.Steps[ex.CurrentStep]; ok {
			if age, ok := ageSince(st.StartedAt, now); ok {
				fmt.Fprintf(&b, " %s", FormatAge(age))
			}
		}
	}
	if j := s.Job; j != nil && j.LastProgressAgoSec != nil {
		fmt.Fprintf(&b, " signal %s ago", FormatAge(time.Duration(*j.LastProgressAgoSec)*time.Second))
	}
	if p := s.Progress; p != nil {
		if p.Command != "" {
			fmt.Fprintf(&b, " cmd=%q", clean(p.Command))
			if p.CommandAgeSec != nil {
				fmt.Fprintf(&b, " (%s)", FormatAge(time.Duration(*p.CommandAgeSec)*time.Second))
			}
		}
		if len(p.RecentFiles) > 0 {
			files := make([]string, 0, 3)
			for i, f := range p.RecentFiles {
				if i == 3 {
					break
				}
				files = append(files, clean(f))
			}
			fmt.Fprintf(&b, " files=%s", strings.Join(files, ","))
		}
		if p.ToolCalls != nil {
			fmt.Fprintf(&b, " tools=%d", *p.ToolCalls)
		}
	}
	return b.String()
}

// FormatOnce renders the `pf watch <wi> --once` snapshot for a human: the
// state, current step and age, last work signal, the pending question (id
// and text — the one place the question text is shown, on explicit request)
// and the latest events.
func FormatOnce(s *protocol.WatchSnapshot, now time.Time, maxEvents int) string {
	var b strings.Builder
	if s.Slug != "" {
		fmt.Fprintf(&b, "work item:  %s (%s)\n", safeID(s.Slug), safeID(s.WorkItem))
	} else {
		fmt.Fprintf(&b, "work item:  %s\n", safeID(s.WorkItem))
	}
	fmt.Fprintf(&b, "status:     %s\n", StateLabel(s))
	if s.Execution == nil {
		if s.Known || s.Queued {
			b.WriteString("note:       submitted, not yet registered with a monitor (queued/preparing, not an error)\n")
		} else {
			b.WriteString("note:       this daemon holds nothing for the work item yet (queued/preparing, or not submitted here)\n")
		}
	}
	if ex := s.Execution; ex != nil {
		fmt.Fprintf(&b, "workflow:   %s\n", clean(ex.WorkflowStatus))
		if ex.CurrentStep != "" {
			line := clean(ex.CurrentStep)
			if st, ok := ex.Steps[ex.CurrentStep]; ok {
				line += " (" + clean(st.Status)
				if age, ok := ageSince(st.StartedAt, now); ok {
					line += ", running " + FormatAge(age)
				}
				line += ")"
			}
			fmt.Fprintf(&b, "step:       %s\n", line)
		}
	}
	if j := s.Job; j != nil {
		if j.LastProgressAgoSec != nil {
			fmt.Fprintf(&b, "last signal: %s ago\n", FormatAge(time.Duration(*j.LastProgressAgoSec)*time.Second))
		}
	}
	if p := s.Progress; p != nil {
		if p.Command != "" {
			fmt.Fprintf(&b, "command:    %s\n", clean(p.Command))
		}
		if len(p.RecentFiles) > 0 {
			fmt.Fprintf(&b, "files:      %s\n", clean(strings.Join(p.RecentFiles, ", ")))
		}
		if p.ToolCalls != nil {
			fmt.Fprintf(&b, "tool calls: %d\n", *p.ToolCalls)
		}
	}
	if ex := s.Execution; ex != nil && ex.PendingQuestion != nil && ex.PendingQuestion.ID != "" {
		q := ex.PendingQuestion
		fmt.Fprintf(&b, "question:   %s (step %s)\n", clean(q.ID), clean(q.Step))
		fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(cleanMultiline(q.Question), "\n", "\n  "))
	}
	evs := s.Events
	if maxEvents > 0 && len(evs) > maxEvents {
		evs = evs[len(evs)-maxEvents:]
	}
	if len(evs) > 0 {
		b.WriteString("events:\n")
		for _, e := range evs {
			fmt.Fprintf(&b, "  #%d %s %s\n", e.Seq, clean(e.At), clean(e.Kind))
		}
	}
	return b.String()
}

func ageSince(ts string, now time.Time) (time.Duration, bool) {
	if ts == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, false
	}
	if d := now.Sub(t); d > 0 {
		return d, true
	}
	return 0, true
}

// FormatAge renders a compact age: 45s, 4m12s, 2h05m.
func FormatAge(d time.Duration) string {
	s := int64(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
}

// clean drops control characters (no terminal escapes from agent text) and
// truncates.
// cleanMultiline drops terminal control characters from free text (the
// agent-authored question) but keeps its line structure: newlines survive,
// tabs become spaces, every other C0/C1 control (ESC included) is removed.
func cleanMultiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if rs := []rune(out); len(rs) > maxFieldWidth {
		out = string(rs[:maxFieldWidth]) + "…"
	}
	return out
}
