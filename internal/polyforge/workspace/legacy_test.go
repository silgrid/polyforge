package workspace

import (
	"strings"
	"testing"
)

const legacyFixture = `# kept: workspace-owned step settings
summarize:
  models: ["pi:a"]
  language: "Chinese"

# memory_policy: what the workflow's last step puts into the work item's
# experience.wrap summary memory. record = worth remembering, skip = never
# in it. A project entry's lists are appended to default's:
#   <project>: { record: [...], skip: [...] }
memory_policy:
  default:
    record:
      - a
# kept: comment above steps
steps:
  e2e:
    fix: { models: ["pi:s"] }
# kept: trailing comment
`

// TestLegacySectionsSpansAndRemoval pins the one reader of the retired
// sections: keys and lines, the spans (the init comment block goes with
// memory_policy, the comment before the next key stays), and a removal
// that keeps every other byte.
func TestLegacySectionsSpansAndRemoval(t *testing.T) {
	secs, err := LegacySections([]byte(legacyFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || secs[0].Key != "summarize" || secs[0].Line != 2 || secs[1].Key != "memory_policy" || secs[1].Line != 10 {
		t.Fatalf("sections = %+v", secs)
	}
	if secs[1].Start != 5 || secs[1].End != 13 {
		t.Fatalf("memory_policy span = [%d,%d), want [5,13)", secs[1].Start, secs[1].End)
	}
	got := string(RemoveLegacySections([]byte(legacyFixture), secs))
	want := "# kept: workspace-owned step settings\n\n# kept: comment above steps\nsteps:\n  e2e:\n    fix: { models: [\"pi:s\"] }\n# kept: trailing comment\n"
	if got != want {
		t.Fatalf("removal:\n%q\nwant\n%q", got, want)
	}
	if again, _ := LegacySections([]byte(got)); len(again) != 0 {
		t.Fatalf("the cleaned file still carries %+v", again)
	}
	reason := LegacyReason(secs)
	for _, needle := range []string{"line 2", "line 10", "`summarize:`", "`memory_policy:`", "pf config migrate"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("reason %q lacks %q", reason, needle)
		}
	}
	if none, err := LegacySections([]byte("steps:\n  e2e: {}\n")); err != nil || len(none) != 0 || LegacyReason(none) != "" {
		t.Fatalf("a steps-only yaml carries no legacy section: %+v %v", none, err)
	}
}
