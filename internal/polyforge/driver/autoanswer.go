package driver

// autoanswer.go: the daemon's standing authorization for a low-star
// question card (polyforge#239, owner ruling 2026-11-23): when every row
// of a STRICT four-column question table is graded ★ or ★★, nobody
// answered it before T0+timeout, and no protected stop owns the step, the
// daemon appends one reply of its own — a reserved identity, never the
// owner's — adopting each row's recommended option verbatim.
//
// This file owns the mechanical half: the card parser (only the one
// unambiguous shape the template teaches), the reply text (the real
// elapsed wait, never the configured threshold), and the decision of
// whether a step is a protected stop. The commit, the delivery and the
// decision-feed projection live beside their own paths (ask.go, relay.go,
// decisions.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// autoAnswerAuthorizationDate is the date of the owner ruling that
// authorizes the automatic reply (polyforge#239); it is written into the
// reply so the audit trail names the decision it rests on.
const autoAnswerAuthorizationDate = "2026-11-23"

// autoAnswerCardHeader is the one header the template teaches; the card
// must carry it cell for cell.
var autoAnswerCardHeader = []string{"问题", "推荐选项", "理由", "重点关注程度"}

// parseAutoAnswerCard answers the recommended option of every row of text's
// one strict four-column question table, or ok=false when the card is not
// exactly that shape (ok is all-or-nothing: one unsafe or ambiguous row
// refuses the whole card).
func parseAutoAnswerCard(text string) (recommendations []string, ok bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// One table block: consecutive lines that start with `|` (the template's
	// shape). A fenced block is skipped whole — a table inside it is no
	// table — and any second block refuses the card.
	var tables [][]string
	var cur []string
	fence := ""
	flush := func() {
		if len(cur) > 0 {
			tables = append(tables, cur)
			cur = nil
		}
	}
	for _, raw := range strings.Split(text, "\n") {
		t := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			flush()
			fence = t[:3]
			continue
		}
		if strings.HasPrefix(t, "|") {
			cur = append(cur, raw)
			continue
		}
		flush()
	}
	flush()
	if len(tables) != 1 {
		return nil, false
	}
	rows := tables[0]
	if len(rows) < 3 { // header + separator + at least one data row
		return nil, false
	}
	header, ok := splitAutoAnswerRow(rows[0])
	if !ok || len(header) != len(autoAnswerCardHeader) {
		return nil, false
	}
	for i := range header {
		if strings.TrimSpace(header[i]) != autoAnswerCardHeader[i] {
			return nil, false
		}
	}
	sep, ok := splitAutoAnswerRow(rows[1])
	if !ok || len(sep) != len(autoAnswerCardHeader) {
		return nil, false
	}
	for _, c := range sep {
		if !isAutoAnswerSeparator(c) {
			return nil, false
		}
	}
	for _, raw := range rows[2:] {
		cells, ok := splitAutoAnswerRow(raw)
		if !ok || len(cells) != len(autoAnswerCardHeader) {
			return nil, false
		}
		for _, c := range cells {
			if strings.TrimSpace(c) == "" {
				return nil, false
			}
		}
		star := strings.TrimSpace(cells[3])
		if star != "★" && star != "★★" {
			return nil, false
		}
		recommendations = append(recommendations, strings.TrimSpace(cells[1]))
	}
	if len(recommendations) == 0 {
		return nil, false
	}
	return recommendations, true
}

// splitAutoAnswerRow splits one `|`-delimited table line into its cells,
// honouring `\|` (a literal pipe) and inline-code spans (whose `|` never
// splits a cell). ok=false when the line is no table row.
func splitAutoAnswerRow(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "|") {
		return nil, false
	}
	s = s[1:]
	var cells []string
	var b strings.Builder
	code, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			if c == '|' {
				b.WriteByte('|')
			} else {
				b.WriteByte('\\')
				b.WriteByte(c)
			}
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '`':
			code = !code
			b.WriteByte(c)
		case '|':
			if code {
				b.WriteByte(c)
				continue
			}
			cells = append(cells, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	if escaped {
		b.WriteByte('\\')
	}
	if b.Len() > 0 {
		cells = append(cells, b.String())
	}
	return cells, true
}

// isAutoAnswerSeparator reports whether c is a Markdown table's delimiter
// cell (`---`, `:--`, `--:` or `:-:`; at least three dashes).
func isAutoAnswerSeparator(c string) bool {
	c = strings.TrimSpace(c)
	c = strings.TrimPrefix(c, ":")
	c = strings.TrimSuffix(c, ":")
	if len(c) < 3 {
		return false
	}
	for i := 0; i < len(c); i++ {
		if c[i] != '-' {
			return false
		}
	}
	return true
}

// autoAnswerReplyText renders the reply the daemon appends for a card whose
// recommendations were adopted after elapsed: the fixed authorization
// sentence (with the REAL wait, never the configured threshold) and one
// numbered line per recommendation, in card order.
func autoAnswerReplyText(recommendations []string, elapsed time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "自动：按推荐（超时 %s，owner %s 授权）", elapsed.Truncate(time.Second), autoAnswerAuthorizationDate)
	for i, rec := range recommendations {
		fmt.Fprintf(&b, "\n%d. %s", i+1, rec)
	}
	return b.String()
}

// autoAnswerProtectedStop reports whether stepID is a protected stop — a
// human gate the owner's ruling 2026-11-23 keeps human-only, or a step of
// a lane this build cannot verify. content is the work item's verbatim
// content (runtime-state/work-item.json).
func autoAnswerProtectedStop(gate bool, content string) bool {
	if autoAnswerConfirmPlan(content) {
		return true // a plan-confirmation ticket keeps every stop human
	}
	return gate // a gate's question is a human stop (review, owner-review, merge, gate FAIL)
}

// autoAnswerConfirmPlan reports whether the ticket carries the
// `confirm_plan: true` marker (a build stop before the first test).
func autoAnswerConfirmPlan(content string) bool {
	return autoAnswerConfirmPlanLine.MatchString(content)
}

// autoAnswerConfirmPlanLine is the ticket's own `confirm_plan: true`
// marker, on a line of its own (a list bullet allowed). A mention inside
// prose is not the marker: the stop is declared, not discussed.
var autoAnswerConfirmPlanLine = regexp.MustCompile(`(?im)^\s*(?:[-*]\s*)?confirm_plan\s*:\s*true\s*$`)

// EvAutoAnswered is the driver event of an automatic reply (polyforge#239):
// `{step, spawn_n, ask_at, question_id, at, by, elapsed, timeout,
// recommendations}` — machine fields only, never the agent's prose. The
// decision feed classifies it as its own information entry and marks the
// question replied (it still needs no human).
const EvAutoAnswered = "auto_answered"

// autoAnswerSettings answers the Cell's switch and timeout as the current
// supervision defaults hold them (a live reload is read at every check).
func (d *Driver) autoAnswerSettings() (enabled bool, timeout time.Duration) {
	lim := d.limits()
	enabled = lim.AskAutoReplyEnabled == nil || *lim.AskAutoReplyEnabled
	timeout = lim.AskAutoReplyTimeout
	if timeout <= 0 {
		timeout = supervise.DefaultAskAutoReplyTimeout
	}
	return enabled, timeout
}

// recordAwaitingStart appends the daemon-only awaiting line of (step, n)'s
// ask when the live dispatch FIRST observes it: that stamp is T0. A
// restart's plan pass only reads it (announceParkedAsk), so a wait older
// than this change has no trustworthy T0 and is never auto-answered.
func (d *Driver) recordAwaitingStart(stepID string, n int64, ask *capsule.AskLine) {
	if d.wi.CapsuleDir == "" || ask == nil {
		return
	}
	d.askWriteMu.Lock()
	defer d.askWriteMu.Unlock()
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return
	}
	for _, l := range lines {
		if l.Kind == capsule.AskKindAwaiting && l.Step == stepID && l.Spawn == n && l.AskAt == ask.At {
			return // a restart never moves T0
		}
	}
	if err := capsule.AppendAsk(d.wi.CapsuleDir, capsule.AskLine{
		At: d.stamp(), Step: stepID, Spawn: n, Kind: capsule.AskKindAwaiting, AskAt: ask.At,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: awaiting line not appended (no automatic reply for ask %s): %v\n", d.wi.ID, ask.At, err)
	}
}

// autoAnswer commits the automatic reply for ask when the card is safe, the
// wait is due and no one answered first. It re-reads and re-checks the
// record under askWriteMu, so the owner's relay and this commit are one
// serialized read-check-append: whoever appends first wins, and the loser
// writes nothing extra. true = the reply was appended (and the event
// emitted).
func (d *Driver) autoAnswer(ask *capsule.AskLine) bool {
	if ask == nil || ask.Kind != capsule.AskKindAsk || d.wi.CapsuleDir == "" {
		return false
	}
	if d.autoAnswerProtected(ask.Step) {
		return false
	}
	enabled, timeout := d.autoAnswerSettings()
	if !enabled {
		return false
	}
	now := d.now()
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return false
	}
	t0, ok := capsule.AwaitingSince(lines, ask.Step, ask.Spawn, ask.At, now)
	if !ok || now.Before(t0.Add(timeout)) {
		return false
	}
	recommendations, ok := parseAutoAnswerCard(ask.Text)
	if !ok {
		return false
	}
	elapsed := now.Sub(t0)
	// The commit is one serialized read-check-append against the owner's
	// relay: whoever appends first wins, and the loser writes nothing extra.
	d.askWriteMu.Lock()
	defer d.askWriteMu.Unlock()
	lines, err = capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return false
	}
	if open := capsule.OpenAsk(lines, ask.Step, ask.Spawn); open == nil || open.At != ask.At {
		return false
	}
	if _, ok := capsule.AwaitingSince(lines, ask.Step, ask.Spawn, ask.At, now); !ok {
		return false
	}
	reply := capsule.AskLine{
		At: now.UTC().Format(time.RFC3339Nano), Step: ask.Step, Spawn: ask.Spawn,
		Kind: capsule.AskKindReply, AskAt: ask.At, Text: autoAnswerReplyText(recommendations, elapsed), By: capsule.AutoAnswerBy,
	}
	if err := capsule.AppendAsk(d.wi.CapsuleDir, reply); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: automatic reply not appended (the question keeps waiting): %v\n", d.wi.ID, err)
		return false
	}
	d.emit(EvAutoAnswered, map[string]any{
		"step": ask.Step, "spawn_n": ask.Spawn, "ask_at": ask.At, "question_id": ask.At, "at": reply.At,
		"by": capsule.AutoAnswerBy, "elapsed": elapsed.Truncate(time.Second).String(), "timeout": timeout.String(),
		"recommendations": recommendations,
	})
	return true
}

// autoAnswerProtected answers autoAnswerProtectedStop for the current lane
// and work item. A work item whose content cannot be read has no
// trustworthy confirm_plan fact: no automatic reply.
func (d *Driver) autoAnswerProtected(stepID string) bool {
	content := ""
	if d.wi.CapsuleDir != "" {
		raw, err := os.ReadFile(filepath.Join(d.wi.CapsuleDir, filepath.FromSlash(capsule.WorkItemJSON)))
		if err != nil {
			return true
		}
		var doc capsule.WorkItemDoc
		if err := json.Unmarshal(raw, &doc); err != nil || doc.Content == nil {
			return true
		}
		content = *doc.Content
	}
	gate := false
	if d.workflow != nil {
		gate = d.workflow.IsGate(stepID)
	}
	return autoAnswerProtectedStop(gate, content)
}
