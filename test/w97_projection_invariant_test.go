package test

// w97 — refusal cases for the extended identifier invariant. The w97 ticket
// carries the earlier review's blocking findings: the invariant families
// added there had no test that makes project.sh itself refuse, and the
// projection commit-message check still held the old, shorter family list.
//
// The fixture flows live in w97_invariant_scenarios.sh (throwaway git repos,
// PATH-stubbed read-only gh, the working tree's tools/sync scripts); this
// test only runs them through their CLI, one case per subtest:
//
//   content — a residual token left in file content must be refused, the file named;
//   path    — a residual token in a projected path must be refused, the path named;
//   message — a residual token in the tag's release notes must be refused,
//             the message channel named.
//
// Each case also asserts the refusal leaves no projection/<tag> branch and
// does not move the public fixture's main.
import (
	"os/exec"
	"path/filepath"
	"testing"
)

var w97InvariantCases = []struct{ id, title string }{
	{"content", "A residual shorthand in file content refuses the projection"},
	{"path", "A residual shorthand in a projected path refuses the projection"},
	{"message", "A residual shorthand in the release notes refuses the projection message"},
}

func TestW97ProjectionInvariantRefusal(t *testing.T) {
	// cwd during go test is the repo's test/ package dir.
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range w97InvariantCases {
		c := c
		t.Run(c.id, func(t *testing.T) {
			cmd := exec.Command("bash", "w97_invariant_scenarios.sh", repo, c.id)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: red: %v\n%s", c.title, err, out)
			}
			t.Logf("%s: %s", c.title, out)
		})
	}
}
