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

// IsAnalysisHeading reports whether line (trimmed) opens an analysis
// section: `## Analysis` or `## 分析`, case-insensitive, prefix match.
func IsAnalysisHeading(line string) bool {
	return analysisHeadingRe.MatchString(strings.TrimSpace(line))
}

// HasAnalysis reports whether content's analysis section carries text
// — the one presence check `pf submit`, the auto-pull wave and the
// queue display share. 「免分析」 written by the session on the owner's
// explicit skip (with its date) is one such text; a missing section, or
// a heading with no content, is not.
func HasAnalysis(content string) bool {
	return false
}
