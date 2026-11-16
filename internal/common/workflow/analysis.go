package workflow

// analysis.go: the one definition of a work item's pre-submit analysis
// section — its heading and its presence — shared by the CLI (`pf
// submit`'s check), the daemon (the auto-pull wave's skip) and `pf
// queue`'s mark (polyforge#27). The analysis a session runs before
// submitting (the read-only `pf task spawn` errand; the routing skill's
// submit fragment) lands in this section, and so does an owner's
// explicit skip, written by the session as 「免分析」 with its date.
// Like HasAcceptance, HasAnalysis never judges what the section says.

import (
	"regexp"
	"strconv"
	"strings"
)

// analysisHeadingRe matches an analysis section's heading, `##
// Analysis` or `## 分析`, as a prefix (`## Analysis notes`, `##
// 分析（…）`), the same shape acceptanceHeadingRe gives the acceptance
// section.
var analysisHeadingRe = regexp.MustCompile(`(?i)^##\s+(?:analysis\b|分析)`)

// isAnalysisHeading reports whether line (trimmed) opens an analysis
// section: `## Analysis` or `## 分析`, case-insensitive, prefix match.
// Un-exported (review r1#8): no consumer outside the package needs it.
func isAnalysisHeading(line string) bool {
	return analysisHeadingRe.MatchString(strings.TrimSpace(line))
}

// analysisSection answers the text of doc's first analysis section:
// the lines after its heading up to the next heading of level one or
// two, the whole trimmed (found=false when there is none) — the shape
// AcceptanceSection gives the acceptance section.
func analysisSection(doc string) (text string, found bool) {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if !found {
			found = isAnalysisHeading(t)
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

// HasAnalysis reports whether content's analysis section carries text
// — the one presence check `pf submit`, the auto-pull wave and the
// queue display share. 「免分析」 written by the session on the owner's
// explicit skip (with its date) is one such text; a missing section, or
// a heading with no content, is not.
func HasAnalysis(content string) bool {
	text, found := analysisSection(content)
	return found && text != ""
}

// AutoAnalysisMarker answers the machine-readable unconfirmed marker of
// the analysis a daemon-scheduled task wrote (polyforge#72, ticket ruling
// 9): `<!-- pf:auto-analysis <task id> unconfirmed -->`, the one line the
// section's confirm command (`pf file analysis confirm`) strips; a section
// still carrying it is not claimable by the auto-pull wave (ruling 9).
func AutoAnalysisMarker(taskID string) string {
	return "<!-- pf:auto-analysis " + taskID + " unconfirmed -->"
}

// autoAnalysisMarkerRe matches the unconfirmed marker as one whole line.
var autoAnalysisMarkerRe = regexp.MustCompile(`^<!--\s*pf:auto-analysis\s+\S+\s+unconfirmed\s*-->`)

// AnalysisUnconfirmed reports whether content's analysis section still
// carries an unconfirmed auto-analysis marker — the one gate the
// auto-pull wave skips on and `pf queue` marks「分析待确认」. The marker
// anywhere else in the ticket is not the section's (a comment quote, a
// heading elsewhere): only the section's own lines count.
func AnalysisUnconfirmed(content string) bool {
	text, found := analysisSection(content)
	if !found {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		if autoAnalysisMarkerRe.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// ReplaceAnalysisSection answers doc with its first analysis section's
// text replaced by body — the daemon's narrow write path's one edit
// (AC4: only this section changes, every other section's bytes survive).
// found answers whether the section existed; a doc without one gains a
// new `## 分析` section at the end carrying body (the fresh-ticket shape:
// the analysis scheduler only fires on tickets without the section).
func ReplaceAnalysisSection(doc, body string) (string, bool) {
	body = strings.TrimSpace(body)
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !isAnalysisHeading(strings.TrimSpace(line)) {
			continue
		}
		j := i + 1
		for j < len(lines) {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") {
				break
			}
			j++
		}
		out := make([]string, 0, len(lines)+len(body)+2)
		out = append(out, lines[:i+1]...)
		if body != "" {
			out = append(out, "")
			out = append(out, strings.Split(body, "\n")...)
		}
		if j < len(lines) {
			out = append(out, "")
			out = append(out, lines[j:]...)
		}
		return strings.Join(out, "\n"), true
	}
	base := strings.TrimRight(doc, "\n")
	if base != "" {
		base += "\n"
	}
	return base + "\n## 分析\n\n" + body + "\n", false
}

// analysisItemHeadingRe matches a top-level item heading of the 7-item
// report the analysis prompt template fixes (`### 1. 改动范围`, ruling 17);
// analysisItemPlainRe the same number at column zero (`1. 改动范围`).
var (
	analysisItemHeadingRe = regexp.MustCompile(`^#{1,6}\s*([1-7])[.、]\s*\S`)
	analysisItemPlainRe   = regexp.MustCompile(`^([1-7])[.、]\s*\S`)
)

// AnalysisItemCount answers the highest top-level item number (1–7)
// content's analysis section carries — the「已写到第几项」of the failure
// comment a stopped analysis task leaves (AC5). Indented or out-of-range
// numbers are not top-level items and never count.
func AnalysisItemCount(content string) int {
	text, found := analysisSection(content)
	if !found {
		return 0
	}
	best := 0
	for _, line := range strings.Split(text, "\n") {
		for _, re := range []*regexp.Regexp{analysisItemHeadingRe, analysisItemPlainRe} {
			if m := re.FindStringSubmatch(line); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n > best {
					best = n
				}
				break
			}
		}
	}
	return best
}

// ConfirmAnalysisSection answers content with every unconfirmed
// auto-analysis marker line removed from its analysis section (changed =
// one was there) — the one edit `pf file analysis confirm` makes. Every
// other byte of the section (the draft itself) survives.
func ConfirmAnalysisSection(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !isAnalysisHeading(strings.TrimSpace(line)) {
			continue
		}
		j := i + 1
		for j < len(lines) {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") {
				break
			}
			j++
		}
		out := make([]string, 0, len(lines))
		out = append(out, lines[:i+1]...)
		changed := false
		for _, l := range lines[i+1 : j] {
			if autoAnalysisMarkerRe.MatchString(strings.TrimSpace(l)) {
				changed = true
				continue
			}
			out = append(out, l)
		}
		out = append(out, lines[j:]...)
		if !changed {
			return content, false
		}
		// collapse the blank run the removed marker line left behind
		return strings.Join(out, "\n"), true
	}
	return content, false
}
