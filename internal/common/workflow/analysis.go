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
// the analysis a daemon-scheduled task wrote (polyforge#72, ticket ruling 9):
// `<!-- pf:auto-analysis <task id> unconfirmed -->`, one line the
// section's confirm command strips. Stub (build u1, red first).
func AutoAnalysisMarker(taskID string) string { return "" }

// AnalysisUnconfirmed reports whether content's analysis section still
// carries an unconfirmed auto-analysis marker. Stub (build u1, red first).
func AnalysisUnconfirmed(content string) bool { return false }

// ReplaceAnalysisSection answers doc with its first analysis section's
// text replaced by body (found = the section existed; absent = a new
// `## 分析` section appended at the end). Stub (build u1, red first).
func ReplaceAnalysisSection(doc, body string) (string, bool) { return doc, false }

// AnalysisItemCount answers the highest top-level item number (1–7)
// content's analysis section carries — the「已写到第几项」of a failure
// comment. Stub (build u1, red first).
func AnalysisItemCount(content string) int { return 0 }
