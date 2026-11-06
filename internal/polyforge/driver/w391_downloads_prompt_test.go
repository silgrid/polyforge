package driver

// every step prompt tells the agent where raw downloads
// go — artifacts/downloads/, which the capsule chain never commits —
// that artifacts/ keeps conclusions and small excerpts, and that a file
// over the chain's blob limit is left out and listed in
// artifacts/oversize.json. The capsule-root paragraph carries it, so it
// reaches every step of every bundle (the shared principles reach only
// the principled steps, under a pinned byte budget).

import (
	"strings"
	"testing"
)

func TestW391PromptNamesTheDownloadsDirectory(t *testing.T) {
	root := "/ws/capsules/demo-391"
	for _, step := range []string{"build", "verification", "review"} {
		prompt := assembleStepPrompt(promptParts{
			WIID: "wi_x", StepID: step, SpawnN: 1, CapsuleRoot: root,
			Definition: "Do the step.",
		})
		flat := strings.Join(strings.Fields(prompt), " ")
		for _, want := range []string{
			"CI artifacts, traces, screenshots and video bundles",
			"go under `artifacts/downloads/`",
			"the capsule chain never commits",
			"`artifacts/` keeps conclusions and small excerpts",
			"over 16 MiB",
			"`artifacts/oversize.json`",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s: the prompt must carry %q", step, want)
			}
		}
		if strings.Index(prompt, "artifacts/downloads/") > strings.Index(prompt, "# Step definition") {
			t.Errorf("%s: the downloads rule belongs with the capsule-root binding, before the step definition", step)
		}
	}
}
