package workflow

// acceptance.go: the one definition of a work item's (or a plan's)
// acceptance section — its heading, its criteria and its text — shared by
// the driver (the judged list, the owner stops) and the CLI (`pf submit`'s
// check) — and whether a lane requires one (its `acceptance` key).

import (
	"fmt"
	"regexp"
	"strings"
)

// The values of a lane's `acceptance` (registry.go): absent is
// AcceptanceRequired.
const (
	AcceptanceRequired = "required"
	AcceptanceNone     = "none"
)

// RequiresAcceptance reports whether a work item on the lane must list an
// acceptance criterion before it runs: true unless the lane declares
// `acceptance: none`, and true for a nil lane (one the caller could not
// load). It is the one place the lane's declaration is judged; a caller
// pairs it with HasAcceptance on the item's content.
func (l *Lane) RequiresAcceptance() bool {
	return l == nil || l.Acceptance != AcceptanceNone
}

var (
	// acceptanceHeadingRe matches an acceptance section's heading, `##
	// Acceptance` or `## 验收`, as a prefix (`## Acceptance criteria`,
	// `## 验收（…）`).
	acceptanceHeadingRe = regexp.MustCompile(`(?i)^##\s+(?:acceptance\b|验收)`)
	// ListItemRe matches a top-level markdown list item (at most one
	// leading space) and captures its text.
	ListItemRe = regexp.MustCompile(`^ ?(?:[-*+]|[0-9]+[.)])\s+(.*)$`)
	// checkboxRe strips a task-list checkbox.
	checkboxRe = regexp.MustCompile(`^\[[ xX]\]\s*`)
	// acTokenRe matches a leading AC<n> token (after emphasis or code
	// marks).
	acTokenRe = regexp.MustCompile(`^[*_` + "`" + `]*(?i:AC)([0-9]+)\b`)
)

// IsAcceptanceHeading reports whether line (trimmed) opens an acceptance
// section: `## Acceptance` or `## 验收`, case-insensitive, prefix match.
func IsAcceptanceHeading(line string) bool {
	return acceptanceHeadingRe.MatchString(strings.TrimSpace(line))
}

// AcceptanceItems answers the criterion ids of the list items under the
// first acceptance heading of doc (found=false when there is none), or —
// whole=true — of every top-level list item of doc (an owner's answer).
// Each id is the item's leading `AC<n>` token, else `AC<k>` by 1-based
// position. Fenced code blocks are skipped; the section ends at the next
// heading of level one or two.
func AcceptanceItems(doc string, whole bool) (ids []string, found bool) {
	in := whole
	found = whole
	fenced := false
	k := 0
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if !whole {
			if IsAcceptanceHeading(trimmed) && !found {
				in, found = true, true
				continue
			}
			if in && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")) {
				in = false
			}
		}
		if !in {
			continue
		}
		m := ListItemRe.FindStringSubmatch(strings.TrimRight(line, " \t\r"))
		if m == nil {
			continue
		}
		k++
		text := checkboxRe.ReplaceAllString(strings.TrimSpace(m[1]), "")
		id := fmt.Sprintf("AC%d", k)
		if t := acTokenRe.FindStringSubmatch(text); t != nil {
			id = "AC" + strings.TrimLeft(t[1], "0")
			if id == "AC" {
				id = "AC0"
			}
		}
		ids = append(ids, id)
	}
	return ids, found
}

// AcceptanceSection answers the text of doc's first acceptance section:
// the lines after its heading up to the next heading of level one or two,
// each line's trailing blanks trimmed, the whole trimmed (found=false when
// there is none).
func AcceptanceSection(doc string) (text string, found bool) {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if !found {
			found = IsAcceptanceHeading(t)
			continue
		}
		if strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") {
			break
		}
		b.WriteString(strings.TrimRight(line, " \t\r") + "\n")
	}
	if !found {
		return "", false
	}
	return strings.TrimSpace(b.String()), true
}

// HasAcceptance reports whether content's acceptance section lists at
// least one criterion — the one presence check `pf submit`, the auto-pull
// wave and the queue display share. It never judges what a criterion says.
func HasAcceptance(content string) bool {
	ids, _ := AcceptanceItems(content, false)
	return len(ids) > 0
}
