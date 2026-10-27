// Package scopetier is the one definition of the verification-scope opt-in
// marker and its line predicate, shared by the driver's tier judgment
// (workbench#19) and the CLI's file-create placement guard (workbench#50).
package scopetier

import "strings"

// Marker is the exact trimmed work-item content line that opts a work
// item into the scoped verification tier.
const Marker = "verification: scoped"

// OptedIn reports whether the content carries the opt-in marker: a line
// anywhere in the content whose trimmed form is exactly Marker
// (case-sensitive, no substring match).
func OptedIn(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == Marker {
			return true
		}
	}
	return false
}
