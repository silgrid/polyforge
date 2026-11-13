// carries: .github/workflows/pf-ci.yml, tools/locreport/*, docs/contributing.md

package test

// w480_loc_report_ci_test.go: pf CI's loc-report job writes
// tools/locreport's per-package code-line table into every pull request's job
// summary, comparing the pull request's test-merge commit with its first
// parent (the base branch the pull request merges into). The tool's own exit
// status decides the job: it is 0 whatever the numbers (an exceeded
// threshold is a ::warning annotation), so the job takes no
// continue-on-error that would also hide a broken tool.

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

const w480Command = `GOWORK=off go run ./tools/locreport HEAD^1 HEAD >> "$GITHUB_STEP_SUMMARY"`

func TestW480LocReportCIJob(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(e2e344Root(t), ".github", "workflows", w375Workflow))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			If              string `yaml:"if"`
			ContinueOnError *bool  `yaml:"continue-on-error"`
			Steps           []struct {
				Uses            string            `yaml:"uses"`
				With            map[string]string `yaml:"with"`
				Run             string            `yaml:"run"`
				ContinueOnError *bool             `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatalf("parse .github/workflows/%s: %v", w375Workflow, err)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("the workflow's token must stay contents: read only (no PR comment); got %v", wf.Permissions)
	}
	job, ok := wf.Jobs["loc-report"]
	if !ok {
		t.Fatalf(".github/workflows/%s has no loc-report job", w375Workflow)
	}
	if job.If != "github.event_name == 'pull_request'" {
		t.Errorf("loc-report must run on pull requests only; if = %q", job.If)
	}
	if job.ContinueOnError != nil {
		t.Errorf("loc-report must not set continue-on-error")
	}
	var checkout, command bool
	for _, s := range job.Steps {
		if s.ContinueOnError != nil {
			t.Errorf("a loc-report step sets continue-on-error: %+v", s)
		}
		if s.Uses == "actions/checkout@v4" && s.With["fetch-depth"] == "2" {
			checkout = true
		}
		if s.Run == w480Command || s.Run == w480Command+"\n" {
			command = true
		}
	}
	if !checkout {
		t.Errorf("loc-report must check out with fetch-depth: 2 (the test-merge commit and its first parent)")
	}
	if !command {
		t.Errorf("loc-report must run %q as a step of its own", w480Command)
	}
}
