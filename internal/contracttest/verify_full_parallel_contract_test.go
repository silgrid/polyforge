// carries: workflows/_shared/skills/verify.md, docs/concepts.md, docs/reference/commands.md, docs/reference/config.md, docs/contributing.md, workflows/_shared/scripts/verify.py

package test

// verify_full_parallel_contract_test.go (polyforge#253, AC3): the develop
// lane's verification runs its full tier's declared parallel groups the way
// the integrate lane's full gate does (polyforge#244's declaration, one
// container per group, a group's commands in plan order, results in plan
// order) — and a repository that declares no group keeps the serial bar
// byte for byte. The documents that describe the verification bar and the
// group declaration carry the same contract; the config reference's old
// sentence ("the develop lane's verification runs its full tier serially
// and reads neither key") is gone.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestVerifyFullParallel' -count=1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifyFullParallelDocs is the contract each document carries, folded
// (whitespace collapsed) and matched by substring.
var verifyFullParallelDocs = []struct {
	rel   string
	wants []string
}{
	{"workflows/_shared/skills/verify.md", []string{
		"declared groups", "each group in a container of its own", "plan order",
		"A repository that declares no group", "serial bar",
	}},
	{"docs/reference/commands.md", []string{
		"declared groups", "each group in a container of its own", "plan order",
	}},
	{"docs/concepts.md", []string{
		"declared groups", "side by side",
	}},
	{"docs/reference/config.md", []string{
		"the develop lane's verification (`verify.py`)", "full tier", "polyforge#253",
	}},
	{"docs/contributing.md", []string{"polyforge#253", "declared groups"}},
	{"workflows/_shared/scripts/verify.py", []string{
		"declared groups", "each group in a container of its own",
	}},
}

// TestVerifyFullParallelDocsContract pins the documents the change reaches:
// the verify skill, the commands and concepts references, the config
// reference's group row, the contributing guide's declaration section and
// verify.py's own header.
func TestVerifyFullParallelDocsContract(t *testing.T) {
	root := v1Root(t)
	flat := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
		return strings.Join(strings.Fields(string(raw)), " ")
	}
	for _, c := range verifyFullParallelDocs {
		text := flat(c.rel)
		for _, want := range c.wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s must carry %q (polyforge#253: the full tier's declared groups)", c.rel, want)
			}
		}
	}
}

// TestVerifyFullParallelConfigDropsTheSerialClaim pins the stale sentence
// away: the config reference said verification runs its full tier serially
// and reads neither group key; the ticket makes it read both.
func TestVerifyFullParallelConfigDropsTheSerialClaim(t *testing.T) {
	text := strings.Join(strings.Fields(readVerifyFullParallelFile(t, "docs/reference/config.md")), " ")
	if strings.Contains(text, "runs its full tier serially and reads neither key") {
		t.Fatalf("docs/reference/config.md still says verification runs its full tier serially and reads neither group key (polyforge#253)")
	}
	if strings.Contains(text, "Only the integrate lane's full gate (`gate.py`) schedules the groups side by side") {
		t.Fatalf("docs/reference/config.md still says only the integrate lane's gate schedules the groups side by side (polyforge#253)")
	}
}

func readVerifyFullParallelFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s must exist: %v", rel, err)
	}
	return string(raw)
}
