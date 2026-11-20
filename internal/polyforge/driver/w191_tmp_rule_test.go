package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// TestW191TemporaryPathAndBackgroundRule pins the two new consequences of
// the shell boundary in the shared rule and in every prompt that carries it.
func TestW191TemporaryPathAndBackgroundRule(t *testing.T) {
	phrases := []string{
		"background processes do not survive this tool call",
		"never start one in one call and wait for it in a later call",
		"Temporary files belong under `$TMPDIR` or `artifacts/downloads/`, never a fixed `/tmp/` name",
	}
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, phrase := range phrases {
		if !strings.Contains(text, phrase) {
			t.Errorf("shared principles lack %q", phrase)
		}
	}
	for bundle, steps := range principledSteps {
		for _, step := range steps {
			def, err := readShippedDefinition(t, filepath.Join(shippedWorkflowsDir, bundle), step)
			if err != nil {
				t.Fatalf("read %s/%s: %v", bundle, step, err)
			}
			for _, phrase := range phrases {
				if !strings.Contains(def, phrase) {
					t.Errorf("%s/%s prompt lacks %q", bundle, step, phrase)
				}
			}
		}
	}
}
