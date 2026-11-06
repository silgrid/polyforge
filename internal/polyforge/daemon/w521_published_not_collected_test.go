package daemon

// w521_published_not_collected_test.go: a develop work
// item whose merge step is skipped ends published — its publish step
// registers the record published.py writes (`{state: published, repos:
// [{repo, pr, head, light_verified_head}]}`, the verified heads, no forge
// touched) as its attrs integration — and the integrate lane's collect
// never takes it: integrate.py's pool read, through the step's hub on the
// real aihub fixture (c4_integrate_bridge_test.go), lists landed records
// only.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w521Git runs git in dir, isolated from the host's configuration.
func w521Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	c.Dir = dir
	env := []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	c.Env = env
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w521Commit commits a file in the repo dir and answers the new HEAD.
func w521Commit(t *testing.T, dir, rel, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w521Git(t, dir, "add", "-A")
	w521Git(t, dir, "commit", "-q", "-m", rel)
	return w521Git(t, dir, "rev-parse", "HEAD")
}

// w521WriteJSON writes v as JSON at the capsule-relative path rel.
func w521WriteJSON(t *testing.T, capsuleDir, rel string, v any) {
	t.Helper()
	p := filepath.Join(capsuleDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// w521Published runs published.py from the capsule root the way the
// publish skill's line does, answering its exit code and stderr.
func w521Published(t *testing.T, capsuleDir string) (int, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	scripts, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(py, "-B", filepath.Join(scripts, "published.py"),
		"--publish", "artifacts/publish.json", "--verified", "artifacts/verify/result.json",
		"--record-out", "artifacts/publish/integration.json", "--json", "--out", "artifacts/publish/record.json")
	c.Dir = capsuleDir
	var errb bytes.Buffer
	c.Stderr = &errb
	exit := 0
	if err := c.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("published.py: %v", err)
		}
		exit = ee.ExitCode()
	}
	return exit, errb.String()
}

// w521ResultOf reads the result file published.py wrote, validated
// against the published pf-step-result/1 schema.
func w521ResultOf(t *testing.T, capsuleDir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capsuleDir, "artifacts", "publish", "record.json"))
	if err != nil {
		t.Fatalf("published.py wrote no result file: %v", err)
	}
	schemaPath := filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "schema", "pf-step-result-1.json")
	src, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	comp := jsonschema.NewCompiler()
	if err := comp.AddResource("pf-step-result-1.json", bytes.NewReader(src)); err != nil {
		t.Fatal(err)
	}
	sch, err := comp.Compile("pf-step-result-1.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(v); err != nil {
		t.Fatalf("published.py's result file does not satisfy pf-step-result/1: %v\n%s", err, raw)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

type w521PublishedRepo struct {
	Repo              string `json:"repo"`
	PR                string `json:"pr"`
	Head              string `json:"head"`
	LightVerifiedHead string `json:"light_verified_head"`
}

type w521PublishedRecord struct {
	State string              `json:"state"`
	Repos []w521PublishedRepo `json:"repos"`
}

// TestW521PublishedRecordIsNotCollected: published.py writes the
// published record of a two-repo capsule whose published heads are the
// verified ones — field by field, nothing else — and refuses (exit 1, no
// record, a stale one removed) when a published head is not the verified
// one; that record, as a wrapped work item's attrs integration beside a
// landed one on the aihub fixture, stays out of integrate.py's pool read
// through the step's hub.
func TestW521PublishedRecordIsNotCollected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	capsuleDir := t.TempDir()
	const branch = "polyforge/wi"
	heads := map[string]string{}
	for _, r := range []string{"alpha", "beta"} {
		dir := filepath.Join(capsuleDir, "repos", r)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		w521Git(t, dir, "init", "-q", "-b", branch)
		w521Commit(t, dir, "README", r+"\n")
		heads[r] = w521Commit(t, dir, "feature.txt", r+" feature\n")
	}
	prOf := func(r string) string { return "https://forge.invalid/demo/" + r + "/pull/7" }
	publish := map[string]any{"repos": []any{
		map[string]any{"name": "beta", "branch": branch, "pr_url": prOf("beta"), "head": heads["beta"]},
		map[string]any{"name": "alpha", "branch": "refs/heads/" + branch, "pr_url": prOf("alpha"), "head": heads["alpha"]},
		map[string]any{"name": "docs", "skipped_reason": "no change"},
	}}
	w521WriteJSON(t, capsuleDir, "artifacts/publish.json", publish)
	verified := map[string]any{"schema": "pf-step-result/1", "command": "verify.py", "verdict": "pass", "exit_code": 0, "heads": heads}
	w521WriteJSON(t, capsuleDir, "artifacts/verify/result.json", verified)

	// 1. the record: per published repo (name order) its PR and the
	// verified head, as head and light-verified head
	exit, errb := w521Published(t, capsuleDir)
	if exit != 0 {
		t.Fatalf("published.py on published heads that are the verified ones: exit %d, want 0\n%s", exit, errb)
	}
	recPath := filepath.Join(capsuleDir, "artifacts", "publish", "integration.json")
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("published.py wrote no record: %v", err)
	}
	var rec w521PublishedRecord
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("the published record has a field it does not declare: %v\n%s", err, raw)
	}
	want := w521PublishedRecord{State: "published", Repos: []w521PublishedRepo{
		{Repo: "alpha", PR: prOf("alpha"), Head: heads["alpha"], LightVerifiedHead: heads["alpha"]},
		{Repo: "beta", PR: prOf("beta"), Head: heads["beta"], LightVerifiedHead: heads["beta"]},
	}}
	if !reflect.DeepEqual(rec, want) {
		t.Fatalf("the published record:\n got %+v\nwant %+v", rec, want)
	}
	res := w521ResultOf(t, capsuleDir)
	if res["command"] != "published.py" || res["verdict"] != "pass" {
		t.Fatalf("the result: command %v verdict %v", res["command"], res["verdict"])
	}
	if h, _ := res["heads"].(map[string]any); h["alpha"] != heads["alpha"] || h["beta"] != heads["beta"] {
		t.Fatalf("the result's heads are the capsule's: %v", res["heads"])
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}

	// 2. a published head that is not the verified one: exit 1, the
	// stale record removed, nothing written
	moved := w521Commit(t, filepath.Join(capsuleDir, "repos", "beta"), "later.txt", "later\n")
	publish["repos"].([]any)[0].(map[string]any)["head"] = moved
	w521WriteJSON(t, capsuleDir, "artifacts/publish.json", publish)
	exit, errb = w521Published(t, capsuleDir)
	if exit != 1 {
		t.Fatalf("published.py on a published head that is not the verified one: exit %d, want 1\n%s", exit, errb)
	}
	if _, err := os.Stat(recPath); !os.IsNotExist(err) {
		t.Fatalf("a refused run leaves no record: %v", err)
	}
	if res := w521ResultOf(t, capsuleDir); res["verdict"] != "fail" || !strings.Contains(res["summary"].(string), "beta") {
		t.Fatalf("the refusal names the repo: verdict %v summary %v", res["verdict"], res["summary"])
	}

	// 3. collect's pool read on the aihub fixture: the published item
	// stays out, the landed one is in
	item := func(id, slug string, integ any) map[string]any {
		return map[string]any{"id": id, "slug": slug, "seq": 1, "project": "proj", "status": "wrapped", "priority": "normal",
			"bundle_name": "develop", "updated_at": "2026-11-12T08:00:00Z", "attrs": map[string]any{"integration": integ}}
	}
	landed := map[string]any{"state": "landed", "repos": []any{map[string]any{"repo": "alpha", "pr": prOf("alpha"), "head": "h-l", "merge_commit": "m-l"}},
		"merge_commits": map[string]any{"alpha": []any{"m-l"}}}
	h, srv := newC4Aihub(t, []map[string]any{
		item("wi_landed", "proj#1", landed),
		item("wi_published", "proj#2", record),
		{"id": "wi_batch", "slug": "proj#56", "seq": 6, "project": "proj", "status": "running", "bundle_name": "integrate", "attrs": map[string]any{}},
	})
	if it := h.find("wi_published"); it == nil || it["status"] != "wrapped" || attrAt(it, "integration.state") != "published" {
		t.Fatalf("the fixture holds the published item wrapped: %v", it)
	}
	f := c4BatchFixture(t, srv)
	f.as("collect", 1)
	f.td.stepHubAllow = func(stepJob) []string {
		return []string{stepcall.HubGet, stepcall.HubList, stepcall.HubBatch, stepcall.HubFile, stepcall.HubAttrs}
	}
	inStep(t, f)
	var pool struct {
		Items []struct {
			ID          string         `json:"id"`
			Integration map[string]any `json:"integration"`
		} `json:"items"`
	}
	if err := json.Unmarshal(scriptPool(t, "integrate"), &pool); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range pool.Items {
		ids = append(ids, it.ID)
		if it.Integration["state"] != "landed" {
			t.Fatalf("collect's pool took a %v record: %v", it.Integration["state"], it)
		}
	}
	if !reflect.DeepEqual(ids, []string{"wi_landed"}) {
		t.Fatalf("collect's pool is the landed item only, never the published one: %v", ids)
	}
}
