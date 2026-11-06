package test

// w375_main_full_bar_ci_test.go: after a merge, CI runs the
// repository's full verification bar on main — the full-bar, full-e2e and
// full-audit jobs of .github/workflows/pf-ci.yml. These tests pin that the
// jobs run exactly the bar the verify-scope engine reports for this
// repository (its full phase: every conf full_command= line, verbatim and
// in order, plus the appended audit gate), that every push to main reaches
// them and keeps its own run, and that no pull request runs them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// w375Workflow is the workflow that carries the post-merge full bar.
const w375Workflow = "pf-ci.yml"

// w375FullJobs are the job ids (and so the check-run names) of the
// post-merge full bar.
var w375FullJobs = []string{"full-bar", "full-e2e", "full-audit"}

// w375MainOnly is the one condition every full job runs under: a push
// (the workflow takes pushes to main only) or a manual dispatch.
const w375MainOnly = "github.event_name == 'push' || github.event_name == 'workflow_dispatch'"

type w375Job struct {
	Name  string `yaml:"name"`
	If    string `yaml:"if"`
	Steps []struct {
		Run string `yaml:"run"`
	} `yaml:"steps"`
}

type w375WorkflowFile struct {
	On struct {
		Push *struct {
			Branches    []string `yaml:"branches"`
			Paths       []string `yaml:"paths"`
			PathsIgnore []string `yaml:"paths-ignore"`
		} `yaml:"push"`
		PullRequest *yaml.Node `yaml:"pull_request"`
	} `yaml:"on"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress string `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]w375Job `yaml:"jobs"`
}

func w375ReadWorkflow(t *testing.T, root, file string) w375WorkflowFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", file))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var wf w375WorkflowFile
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatalf("parse .github/workflows/%s: %v", file, err)
	}
	return wf
}

// w375Lines are a job's shell command lines, in step order: every run:
// line that is neither blank nor a shell comment, trimmed.
func w375Lines(j w375Job) []string {
	var out []string
	for _, s := range j.Steps {
		for _, l := range strings.Split(s.Run, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
	}
	return out
}

type w375Report struct {
	Commands []struct {
		Name  string `json:"name"`
		Phase string `json:"phase"`
		Cmd   string `json:"cmd"`
	} `json:"commands"`
}

// w375FullTier is the engine's full-tier report for this repository at
// HEAD (an empty diff, --full).
func w375FullTier(t *testing.T, root string) w375Report {
	t.Helper()
	out, errb, code := w25e2eRun(t, root, nil, "--base", "HEAD", "--full", "--json")
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --base HEAD --full --json: exit %d\n%s", code, errb)
	}
	var r w375Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not the report: %v\n%s", err, out)
	}
	return r
}

// w375AuditPath is the engine's absolute, quoted path to the audit helper
// shipped beside it; a CI job runs the same helper from its checkout.
var w375AuditPath = regexp.MustCompile(`^sh '[^']*/workflows/_shared/scripts/audit-deps'`)

// TestW375MainFullBarRunsTheConfBar: the full jobs together run every
// full-phase command of the engine's report, each verbatim as a run: line,
// and each job runs its share in the report's order; they run on every
// push to main and on a dispatch, never on a pull request, and a later
// push never cancels an earlier push's run.
func TestW375MainFullBarRunsTheConfBar(t *testing.T) {
	root := e2e344Root(t)
	wf := w375ReadWorkflow(t, root, w375Workflow)

	if wf.On.Push == nil || !slices.Equal(wf.On.Push.Branches, []string{"main"}) {
		t.Fatalf("on.push must take exactly the main branch; got %+v", wf.On.Push)
	}
	if len(wf.On.Push.Paths) != 0 || len(wf.On.Push.PathsIgnore) != 0 {
		t.Fatalf("on.push must not be path-filtered (every merge gets the full bar); got paths %q paths-ignore %q", wf.On.Push.Paths, wf.On.Push.PathsIgnore)
	}
	if wf.On.PullRequest == nil {
		t.Fatalf("on.pull_request is gone; the fast gates still run per pull request")
	}
	if !strings.Contains(wf.Concurrency.Group, "github.sha") || !strings.Contains(wf.Concurrency.CancelInProgress, "pull_request") {
		t.Fatalf("concurrency must group a push by its commit and cancel only pull-request runs; got group %q cancel-in-progress %q", wf.Concurrency.Group, wf.Concurrency.CancelInProgress)
	}

	lines := map[string][]string{}
	for _, id := range w375FullJobs {
		j, ok := wf.Jobs[id]
		if !ok {
			t.Fatalf("job %q is missing from .github/workflows/%s", id, w375Workflow)
		}
		if j.Name != "" {
			t.Fatalf("job %q sets name %q: its check-run must be named by its id", id, j.Name)
		}
		if strings.TrimSpace(j.If) != w375MainOnly {
			t.Fatalf("job %q runs if %q, want %q (never on a pull request)", id, j.If, w375MainOnly)
		}
		lines[id] = w375Lines(j)
	}

	r := w375FullTier(t, root)
	var full []string
	for _, c := range r.Commands {
		if c.Phase != "full" {
			continue
		}
		cmd := c.Cmd
		if c.Name == "audit" {
			if !w375AuditPath.MatchString(cmd) {
				t.Fatalf("the engine's audit command has an unexpected shape: %q", cmd)
			}
			cmd = w375AuditPath.ReplaceAllString(cmd, "sh workflows/_shared/scripts/audit-deps")
		}
		full = append(full, cmd)
	}
	if len(full) == 0 {
		t.Fatalf("the engine reports no full-phase command for this repository")
	}

	last := map[string]int{}
	for i, cmd := range full {
		var in []string
		for _, id := range w375FullJobs {
			if slices.Contains(lines[id], cmd) {
				in = append(in, id)
			}
		}
		if len(in) == 0 {
			t.Fatalf("full-tier command %q is run by none of the jobs %v of .github/workflows/%s", cmd, w375FullJobs, w375Workflow)
		}
		for _, id := range in {
			if p, seen := last[id]; seen && slices.Index(lines[id], cmd) < p {
				t.Fatalf("job %q runs %q before the command preceding it in the full tier (report position %d)", id, cmd, i)
			}
			last[id] = slices.Index(lines[id], cmd)
		}
	}
}
