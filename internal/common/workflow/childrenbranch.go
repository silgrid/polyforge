package workflow

// childrenbranch.go: the `children_branch` template — a lane's
// declaration of the branch its work items' sub-tickets work on. The
// loader judges the template at load (childrenBranchDefect); the
// daemon's claim of a sub-ticket substitutes the parent's seq and
// project name into it (ChildrenBranchTarget) and fixes the branch into
// the capsule (runtime-state/target-branch.json, the runtime's
// targetbranch.go) before the task repos are cloned.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The placeholders a children_branch template carries.
const (
	// ChildrenSeqPlaceholder substitutes the parent's seq — the epic's
	// own number, so every sub-ticket of the one parent shares the one
	// branch (polyforge#1 → epic/1). A template must carry it.
	ChildrenSeqPlaceholder = "{seq}"
	// ChildrenProjectPlaceholder substitutes the parent's project name
	// (an aihub project name: ^[a-z0-9][a-z0-9-_]{0,39}$, which carries
	// no character a branch ref refuses). A template may carry it.
	ChildrenProjectPlaceholder = "{project}"
)

// childrenPlaceholder matches one {…} token of a template.
var childrenPlaceholder = regexp.MustCompile(`\{[^{}]*\}`)

// ChildrenBranchTarget answers the branch the template names for a
// parent of project and seq: each {seq} becomes the parent's seq in
// decimal, each {project} its project name. Load validated the template
// the lane declared; a caller holding a template that did not come
// through Load judges the answer itself (ValidBranchName).
func ChildrenBranchTarget(template string, seq int64, project string) string {
	return strings.NewReplacer(
		ChildrenProjectPlaceholder, project,
		ChildrenSeqPlaceholder, strconv.FormatInt(seq, 10),
	).Replace(template)
}

// childrenBranchDefect answers the children_branch defect of value (""
// when it is a template of a legal branch name): a template that
// carries no {seq}, an unknown placeholder, or a template whose
// substituted name is not a legal git branch ref. The substituted
// sample is faithful — a seq is decimal digits and a project name's
// charset adds no character a branch ref refuses — so a template the
// sample passes no real parent can break.
func childrenBranchDefect(value string) string {
	for _, ph := range childrenPlaceholder.FindAllString(value, -1) {
		if ph != ChildrenSeqPlaceholder && ph != ChildrenProjectPlaceholder {
			return fmt.Sprintf("%s is not a placeholder of the template (only {seq} and {project})", ph)
		}
	}
	if !strings.Contains(value, ChildrenSeqPlaceholder) {
		return "the template carries no {seq} (the parent's seq) to substitute"
	}
	if name := ChildrenBranchTarget(value, 1, "proj"); !ValidBranchName(name) {
		return fmt.Sprintf("the substituted %q is not a legal git branch ref name", name)
	}
	return ""
}

// ValidBranchName reports whether name is a branch name git takes, by
// the rules of git-check-ref-format(1) --branch (a branch name needs no
// category prefix, so no slash is demanded): no empty component (a
// leading or trailing slash, a doubled one), no component starting with
// a dot or ending with the sequence .lock, no ASCII control character
// or space, none of ~ ^ : ? * [ \ anywhere, no doubled dot, no @{, no
// leading dash, not HEAD itself, and no trailing dot or slash.
func ValidBranchName(name string) bool {
	if name == "" || name == "HEAD" || strings.HasPrefix(name, "-") ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, "/") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") {
		return false
	}
	for _, cp := range name {
		if cp <= 0x20 || cp == 0x7f { // the ASCII control characters and the space
			return false
		}
		if strings.ContainsRune(`~^:?*[\`, cp) {
			return false
		}
	}
	for _, comp := range strings.Split(name, "/") {
		if comp == "" || strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return false
		}
	}
	return true
}
