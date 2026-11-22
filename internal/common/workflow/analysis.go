package workflow

// analysis.go: the one definition of a work item's pre-submit analysis
// section — its heading and its presence — shared by the CLI (`pf
// submit`'s check), the daemon (the auto-pull wave's skip) and `pf
// queue`'s mark (polyforge#27). The analysis the daemon schedules at
// `pf file create` lands in this section, written by the analysis task
// itself through the narrow write path and marked unconfirmed until the
// owner confirms it (polyforge#72); so does a session errand's report,
// and an owner's explicit skip, 「免分析」 with its date.
// Like HasAcceptance, HasAnalysis never judges what the section says.

import (
	"errors"
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
// endsSection reports whether the trimmed line t ends the analysis
// section: a heading of level one or two.
func endsSection(t string) bool {
	return strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ")
}

// SectionEndingLine answers the first line of draft that would end the
// analysis section once written into it (ok=false when none does) — the
// narrow write path refuses such a draft, since the next whole-section
// replace would stop at that line and leave the rest outside the section
// (polyforge#72, review r1#2).
func SectionEndingLine(draft string) (line string, ok bool) {
	for _, l := range strings.Split(draft, "\n") {
		if t := strings.TrimSpace(l); endsSection(t) {
			return t, true
		}
	}
	return "", false
}

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
		if endsSection(t) {
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
// queue display share. 「免分析」 written on the owner's explicit skip
// (with its date) is one such text; a missing section, or
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
			if endsSection(t) {
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
			if endsSection(t) {
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
		return strings.Join(out, "\n"), true
	}
	return content, false
}

// AnalysisArchiveHeading is the section a redo appends after the active
// analysis: the verbatim copies of the sections it replaced, newest last
// (polyforge#233, owner ruling Q1=B).
const AnalysisArchiveHeading = "## 历次分析"

// AnalysisRedoPrefix prefixes the request id a pending redo round carries
// in the auto-analysis marker's token slot, before the task that replaces
// it has written its first draft: `<!-- pf:auto-analysis
// redo_<16 hex> unconfirmed -->` (owner ruling Q3=A).
const AnalysisRedoPrefix = "redo_"

// analysisSpan is one section's line range [start, end) in a ticket body.
type analysisSpan struct{ start, end int }

// analysisArchiveSpan answers the archive section's line range. A heading
// inside an archive entry's <details> block — the old analysis's own
// headings ride there verbatim — never ends it.
func analysisArchiveSpan(lines []string) (analysisSpan, bool) {
	for i, line := range lines {
		if strings.TrimSpace(line) != AnalysisArchiveHeading {
			continue
		}
		inDetails := false
		j := i + 1
		for ; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "<details") {
				inDetails = true
				continue
			}
			if strings.HasPrefix(t, "</details") {
				inDetails = false
				continue
			}
			if !inDetails && endsSection(t) {
				break
			}
		}
		return analysisSpan{start: i, end: j}, true
	}
	return analysisSpan{}, false
}

// activeAnalysisSpan answers the one analysis section outside the archive
// (found=false when there is none). More than one is ambiguous: the redo
// refuses rather than guessing which section it would replace, and an
// archive before the active section is refused too — the archive is always
// appended after it (owner ruling Q1=B).
func activeAnalysisSpan(lines []string, archive analysisSpan, hasArchive bool) (analysisSpan, bool, error) {
	var found []analysisSpan
	for i, line := range lines {
		if hasArchive && i >= archive.start && i < archive.end {
			continue
		}
		if !isAnalysisHeading(line) {
			continue
		}
		j := i + 1
		for j < len(lines) && !endsSection(strings.TrimSpace(lines[j])) {
			j++
		}
		found = append(found, analysisSpan{start: i, end: j})
	}
	switch {
	case len(found) == 0:
		return analysisSpan{}, false, nil
	case len(found) > 1:
		return analysisSpan{}, false, errors.New("the ticket carries more than one active analysis section — its old text has no clear boundary; tidy the ticket before redoing its analysis")
	}
	a := found[0]
	if hasArchive && archive.start < a.start {
		return analysisSpan{}, false, errors.New("the ticket's analysis archive precedes its active analysis section — tidy the ticket before redoing its analysis")
	}
	return a, true, nil
}

// archiveEntry formats one verbatim copy under a <details> fold. The raw
// text is copied byte for byte inside a fence longer than any backtick run
// it carries; only a final newline is added when the raw text had none.
func archiveEntry(requestID, stamp, raw string) string {
	fence := strings.Repeat("`", max(4, longestBacktickRun(raw)+1))
	body := raw
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "<!-- pf:analysis-archive " + requestID + " begin -->\n" +
		"<details>\n<summary>旧分析 · " + stamp + " · " + requestID + "</summary>\n\n" +
		fence + "markdown\n" + body + fence + "\n</details>\n" +
		"<!-- pf:analysis-archive " + requestID + " end -->"
}

func longestBacktickRun(s string) int {
	best, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > best {
				best = run
			}
		} else {
			run = 0
		}
	}
	return best
}

func needsBlankBefore(lines []string, next string) bool {
	if len(lines) == 0 || strings.TrimSpace(next) == "" {
		return false
	}
	return strings.TrimSpace(lines[len(lines)-1]) != ""
}

// PrepareAnalysisRedo archives content's active analysis section verbatim
// into AnalysisArchiveHeading and prefixes the active section with a
// pending redo marker — one content value, so one aihub CAS carries both.
// A repeated request id changes nothing (the archive entry's begin marker
// is the dedupe key). A ticket without an analysis section gains one that
// carries the marker alone; a ticket the analysis (a hand-written report, a
// dated 免分析 included) is archived like any other before it is replaced.
func PrepareAnalysisRedo(content, requestID, stamp string) (string, error) {
	if strings.TrimSpace(requestID) == "" {
		return content, errors.New("analysis redo: the request id is required")
	}
	begin := "<!-- pf:analysis-archive " + requestID + " begin -->"
	if strings.Contains(content, begin) {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	archive, hasArchive := analysisArchiveSpan(lines)
	active, found, err := activeAnalysisSpan(lines, archive, hasArchive)
	if err != nil {
		return content, err
	}
	marker := AutoAnalysisMarker(requestID)
	if !found {
		base := strings.TrimRight(content, "\n")
		if base != "" {
			base += "\n"
		}
		return base + "\n## 分析\n\n" + marker + "\n", nil
	}
	raw := strings.Join(lines[active.start:active.end], "\n")
	entry := archiveEntry(requestID, stamp, raw)

	out := make([]string, 0, len(lines)+16)
	out = append(out, lines[:active.start+1]...)
	out = append(out, marker)
	body := lines[active.start+1 : active.end]
	if len(body) > 0 {
		out = append(out, "")
		out = append(out, body...)
	}
	if hasArchive {
		out = append(out, lines[active.end:archive.start]...)
		out = append(out, lines[archive.start:archive.end]...)
		if needsBlankBefore(out, entry) {
			out = append(out, "")
		}
		out = append(out, strings.Split(entry, "\n")...)
		rest := lines[archive.end:]
		if needsBlankBefore(out, firstLine(rest)) {
			out = append(out, "")
		}
		out = append(out, rest...)
	} else {
		if needsBlankBefore(out, AnalysisArchiveHeading) {
			out = append(out, "")
		}
		out = append(out, AnalysisArchiveHeading, "")
		out = append(out, strings.Split(entry, "\n")...)
		rest := lines[active.end:]
		if needsBlankBefore(out, firstLine(rest)) {
			out = append(out, "")
		}
		out = append(out, rest...)
	}
	return strings.Join(out, "\n"), nil
}

func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// autoAnalysisMarkerTokenRe captures the marker's token (the task id, or a
// pending redo request id before the task's first write).
var autoAnalysisMarkerTokenRe = regexp.MustCompile(`^<!--\s*pf:auto-analysis\s+(\S+)\s+unconfirmed\s*-->`)

// AnalysisRedoRequestID answers the pending redo request id the analysis
// section's unconfirmed marker carries (ok=false when the marker is absent
// or names a task id: the redo round has been taken over by its task).
func AnalysisRedoRequestID(content string) (string, bool) {
	text, found := analysisSection(content)
	if !found {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		m := autoAnalysisMarkerTokenRe.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil && strings.HasPrefix(m[1], AnalysisRedoPrefix) {
			return m[1], true
		}
	}
	return "", false
}
