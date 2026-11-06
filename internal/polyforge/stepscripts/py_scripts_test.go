package stepscripts

// py_scripts_test.go: the step scripts that succeeded pf-sync, pf-merge,
// pf-revert and pf-run (workflows/_shared/scripts/<name>.py)
// driven by this package's black-box tests as a skill runs them — python3
// on the script with the step environment — and the shapes their result
// details decode into.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The scripts, as their result files name themselves.
const (
	scriptSync   = "sync.py"
	scriptMerge  = "merge.py"
	scriptRevert = "revert.py"
	scriptRun    = "run.py"
)

// scriptsDir answers the shared scripts directory of this tree.
func scriptsDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// invokePy runs the script with args under env (the step environment: the
// list is the process's whole environment), answering the exit code and
// the two streams; a result file it wrote must satisfy the published
// schema (w473_result_schema_test.go).
func invokePy(t *testing.T, script string, args, env []string) (int, string, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	c := exec.Command(py, append([]string{"-B", filepath.Join(scriptsDir(t), script)}, args...)...)
	c.Env = env
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	exit := 0
	if err := c.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s: %v", script, err)
		}
		exit = ee.ExitCode()
	}
	checkWrittenResult(t, args)
	return exit, out.String(), errb.String()
}

// fakeDockerEnv puts the fake docker client (the scripts' tests' own,
// tests/fakedocker.sh) first on the PATH of env, its state under a fresh
// directory it answers.
func fakeDockerEnv(t *testing.T, env []string) ([]string, string) {
	t.Helper()
	bin, state := t.TempDir(), t.TempDir()
	raw, err := os.ReadFile(filepath.Join(scriptsDir(t), "tests", "fakedocker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), raw, 0o755); err != nil {
		t.Fatal(err)
	}
	return append(withPath(env, bin), "FAKE_DOCKER_STATE="+state, "FAKE_DOCKER_PATH="+os.Getenv("PATH")), state
}

// short shortens a commit name for a message.
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ---- sync.py's details

const syncRecordName = "syncing.json"

type syncRepo struct {
	Name            string            `json:"name"`
	Branch          string            `json:"branch,omitempty"`
	Origin          string            `json:"origin,omitempty"`
	Before          string            `json:"before"`
	After           string            `json:"after,omitempty"`
	DefaultBranch   string            `json:"default_branch,omitempty"`
	DefaultHead     string            `json:"default_head,omitempty"`
	FastForwarded   bool              `json:"fast_forwarded,omitempty"`
	Mechanical      []string          `json:"mechanical"`
	Conflicts       []string          `json:"conflicts"`
	Resolved        []string          `json:"resolved"`
	Notes           []string          `json:"notes,omitempty"`
	Incoming        []string          `json:"incoming,omitempty"`
	Discarded       []syncDiscarded   `json:"discarded,omitempty"`
	SelfHealed      bool              `json:"self_healed,omitempty"`
	MechanicalBlobs map[string]string `json:"mechanical_blobs,omitempty"`
	Aborted         bool              `json:"aborted,omitempty"`
}

type syncDiscarded struct {
	Commit string `json:"commit"`
	Ref    string `json:"ref"`
}

type syncRecord struct {
	Record  string      `json:"record"`
	Round   string      `json:"round,omitempty"`
	Aborted bool        `json:"aborted,omitempty"`
	Repos   []*syncRepo `json:"repos"`
}

func (r *syncRecord) repo(name string) *syncRepo {
	for _, x := range r.Repos {
		if x.Name == name {
			return x
		}
	}
	return nil
}

// ---- merge.py's details

const (
	HowMerged          = "merged"
	HowMergedElsewhere = "merged_elsewhere"
)

type publishFile struct {
	Repos []publishEntry `json:"repos"`
}

type publishEntry struct {
	Name          string `json:"name"`
	SkippedReason string `json:"skipped_reason,omitempty"`
	Branch        string `json:"branch"`
	PRURL         string `json:"pr_url"`
	Head          string `json:"head"`
}

type mergeRepo struct {
	Name        string `json:"name"`
	PRURL       string `json:"pr_url"`
	Branch      string `json:"branch"`
	Head        string `json:"head"`
	Published   string `json:"published,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	How         string `json:"how,omitempty"`
	BranchSide  string `json:"branch_side,omitempty"`
	ByThisRun   bool   `json:"by_this_run,omitempty"`
	Why         string `json:"why,omitempty"`
}

type mergeDetails struct {
	Publish      string              `json:"publish"`
	Verified     string              `json:"verified"`
	Repos        []*mergeRepo        `json:"repos"`
	MergeCommits map[string][]string `json:"merge_commits"`
}

// ---- revert.py's details

const revertPlanName = "revert-plan.json"

type batchItem struct {
	ID           string              `json:"id"`
	Slug         string              `json:"slug"`
	MergeCommits map[string][]string `json:"merge_commits"`
	FixTickets   []string            `json:"fix_tickets"`
}

type revertRepo struct {
	Name         string   `json:"name"`
	MergeCommits []string `json:"merge_commits"`
	RevertPR     string   `json:"revert_pr,omitempty"`
	RevertMerge  string   `json:"revert_merge,omitempty"`
}

type revertEntry struct {
	WI    string        `json:"wi"`
	Name  string        `json:"name"`
	Group []string      `json:"group"`
	Repos []*revertRepo `json:"repos"`
	Done  bool          `json:"done,omitempty"`
	Why   string        `json:"why,omitempty"`
}

type revertDetails struct {
	Batch   string            `json:"batch"`
	Plan    string            `json:"plan"`
	Reverts []*revertEntry    `json:"reverts"`
	Heads   map[string]string `json:"heads"`
	Gated   []string          `json:"gated"`
}

// ---- run.py's details

type runEntry struct {
	Repo       string `json:"repo"`
	Line       string `json:"line"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	NotStarted bool   `json:"not_started,omitempty"`
	Log        string `json:"log,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
	Image      string `json:"image,omitempty"`
	Container  string `json:"container,omitempty"`
}

type runDetails struct {
	Plan     string     `json:"plan,omitempty"`
	Deadline string     `json:"deadline"`
	Runs     []runEntry `json:"runs"`
	NotRun   []planLine `json:"not_run,omitempty"`
}

func (e *revertEntry) repo(name string) *revertRepo {
	for _, r := range e.Repos {
		if r.Name == name {
			return r
		}
	}
	return nil
}
