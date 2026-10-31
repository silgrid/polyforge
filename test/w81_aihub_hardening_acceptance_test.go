package test

// w81_aihub_hardening_acceptance_test.go: the e2e acceptance suite for
// (aihub server pressure bounds and push-gate hardening),
// authored against the capsule's artifacts/e2e-scenarios.md BEFORE the
// implementation lands. Every kept scenario is an assertion-level RED on
// the pre-implementation tree and is driven only through the project's
// real e2e surfaces: the REAL aihub binary on loopback (newV1Install),
// real raw HTTP/TCP, real `git push` through the smart-HTTP proxy, and
// the running hub's on-disk bare repos.
//
// Scenario map (document order is artifacts/e2e-scenarios.md):
//   S1 Header deadline cuts off a slow-header connection
//   S2 Oversized request header is rejected
//   S3 Over-cap receive-pack is refused without residue
//   S4 Over-cap upload-pack is refused the same way
//   S5 Blob-check pipeline failure rejects the push
//   S6 Gitlink-check failure rejects the push
//   S7 A scoped admin key does not leak other projects (memory search)
//   S8 A non-admin with readable items never gets an empty page (WI list)
//
// Not re-authored here because the pre-implementation tree already has
// the behaviour (regression protection only; see the scenario document):
// legitimate pushes / 25s inbox long-poll / no global Read+WriteTimeout,
// healthy and existing hook rejections, visibility tiers, non-member
// empty search, and `?project=<non-member>` list filtering. The
// query-count criteria (memory search / WI list) are not observable on a
// black-box e2e surface; the plan pins them with package-level counting
// tests.

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w81GitBodyCap is the spec's 1GiB request-body bound.
const w81GitBodyCap int64 = 1 << 30

// w81NewWI files one claimable execution ticket in project.
func w81NewWI(t *testing.T, inst *v1Install, project, goal string) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: project, Goal: goal, BundleName: strPtr("autopilot"),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	return wi.ID
}

// w81Claim claims the WI and answers the session secret used as the git
// Basic-auth password.
func w81Claim(t *testing.T, inst *v1Install, wi string) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(b)
	if _, err := inst.client.SessionClaim(api.SessionClaimRequest{
		WorkItemID: wi, SessionID: secret, MachineID: "m-w81",
	}); err != nil {
		t.Fatalf("claim %s: %v", wi, err)
	}
	return secret
}

// w81HookFailInstall boots a real aihub whose PATH starts with a git
// wrapper that fails the named hook command (failSubstring) whenever the
// caller is the bash pre-receive hook the proxy execs. The wrapper only
// acts in hook context (parent process bash AND the proxy's
// GIT_PF_MAX_BLOB marker), so receive-pack and the test's own git stay
// healthy: a red/green result is about the hook, not about a broken git.
func w81HookFailInstall(t *testing.T, failSubstring string) (*v1Install, string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("no real git: %v", err)
	}
	execPath, err := exec.Command(realGit, "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.log")
	// A shim exec-path: every real git helper as a symlink, plus a `git`
	// shim. git http-backend prepends its exec path to PATH, so a PATH-only
	// wrapper is never reached from inside the CGI; pointing GIT_EXEC_PATH
	// here puts the shim where the hook's `git ...` calls resolve it.
	ents, err := os.ReadDir(strings.TrimSpace(string(execPath)))
	if err != nil {
		t.Fatalf("read git exec path: %v", err)
	}
	for _, e := range ents {
		if e.Name() == "git" {
			continue
		}
		if err := os.Symlink(filepath.Join(strings.TrimSpace(string(execPath)), e.Name()), filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("shim %s: %v", e.Name(), err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
parent=
if [ -r /proc/$PPID/comm ]; then parent=$(cat /proc/$PPID/comm); fi
echo "invoke parent=$parent marker=${GIT_PF_MAX_BLOB:-none} args=$*" >> %q
case "$*" in
*"%s"*)
  if [ "$parent" = "bash" ] && [ -n "${GIT_PF_MAX_BLOB:-}" ]; then
    echo "FAIL %s" >> %q
    echo "w81 injected hook git failure: %s" >&2
    exit 1
  fi ;;
esac
exec %q "$@"
`, trace, failSubstring, failSubstring, trace, failSubstring, realGit)
	wrapper := filepath.Join(dir, "git")
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, oldExec := os.Getenv("PATH"), os.Getenv("GIT_EXEC_PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("GIT_EXEC_PATH", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
		if oldExec == "" {
			_ = os.Unsetenv("GIT_EXEC_PATH")
		} else {
			_ = os.Setenv("GIT_EXEC_PATH", oldExec)
		}
	})
	return newV1Install(t), trace
}

// w81BareState is the failure diagnostic of the on-disk bare repo.
func w81BareState(t *testing.T, inst *v1Install, wi string) string {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", inst.bareRepo(wi), "for-each-ref", "--format=%(refname) %(objectname)")
	out, _ := cmd.CombinedOutput()
	return fmt.Sprintf("bare=%s refs=%q", inst.bareRepo(wi), strings.TrimSpace(string(out)))
}

// w81Push pushes one commit to the WI's aihub remote and answers git's
// combined output and error.
func w81Push(t *testing.T, inst *v1Install, wi, secret string) (string, error) {
	t.Helper()
	dir := shortTmp(t, "w81push")
	gitRun(t, "", "init", "-q", "-b", "main", dir)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("w81\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "w81 acceptance push")
	askpass := filepath.Join(dir, "askpass.sh")
	script := "#!/bin/sh\ncase \"$1\" in Username*) printf '%s\\n' '" + wi + "';; *) printf '%s\\n' '" + secret + "';; esac\n"
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "push", inst.url+"/git/"+wi+".git", "HEAD:refs/heads/main")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_ASKPASS="+askpass, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// w81DeclaredOversizeGitPost sends a smart-HTTP POST declaring a body
// above the 1GiB cap and deliberately never sends that body. It answers
// the HTTP status (0 = no response) and the raw response bytes.
func w81DeclaredOversizeGitPost(t *testing.T, inst *v1Install, wi, secret, endpoint, service string, wait time.Duration) (int, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(inst.port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial aihub: %v", err)
	}
	defer conn.Close()
	cred := base64.StdEncoding.EncodeToString([]byte(wi + ":" + secret))
	req := fmt.Sprintf("POST /git/%s.git/%s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nAuthorization: Basic %s\r\nContent-Type: application/x-git-%s-request\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		wi, endpoint, inst.port, cred, service, w81GitBodyCap+1)
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write request headers: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	raw, rerr := io.ReadAll(io.LimitReader(conn, 8192))
	status := 0
	if len(raw) > 0 {
		if line, _ := bufio.NewReader(strings.NewReader(string(raw))).ReadString('\n'); strings.HasPrefix(line, "HTTP/") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				status, _ = strconv.Atoi(f[1])
			}
		}
	}
	if rerr != nil && status == 0 {
		return 0, rerr.Error()
	}
	return status, string(raw)
}

// TestW81S1SlowHeaderDeadline: a header dribbled slowly is cut off by the
// server's header-read deadline; the request never reaches a handler.
func TestW81S1SlowHeaderDeadline(t *testing.T) {
	inst := newV1Install(t)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(inst.port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial aihub: %v", err)
	}
	defer conn.Close()
	// Never finish the header block: without ReadHeaderTimeout the server
	// holds this connection open indefinitely.
	if _, err := io.WriteString(conn, "GET /api/v1/projects HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Slow: "); err != nil {
		t.Fatalf("write partial header: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 1)
	_, rerr := conn.Read(buf)
	// EOF (or a reset) inside the header-read window is the spec'd close.
	if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the slow-header connection was still open after 15s: the server has no ReadHeaderTimeout (want it closed at the 10s bound)")
	}
	if rerr == nil {
		// Any early bytes (e.g. a 408) are still a server-side close.
		return
	}
	if !strings.Contains(rerr.Error(), "EOF") && !strings.Contains(rerr.Error(), "reset") && !strings.Contains(rerr.Error(), "closed") {
		t.Fatalf("slow-header connection ended unexpectedly: %v", rerr)
	}
}

// TestW81S2OversizedHeaderRejected: a >64KiB request header is answered
// 4xx, not served by a handler.
func TestW81S2OversizedHeaderRejected(t *testing.T) {
	inst := newV1Install(t)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(inst.port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial aihub: %v", err)
	}
	defer conn.Close()
	pad := strings.Repeat("a", 70*1024)
	req := "GET /api/v1/projects HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer " + inst.adminKey +
		"\r\nX-Pad: " + pad + "\r\nConnection: close\r\n\r\n"
	_, _ = io.WriteString(conn, req)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, _ := io.ReadAll(io.LimitReader(conn, 4096))
	text := string(raw)
	if !strings.HasPrefix(text, "HTTP/") {
		t.Fatalf("no HTTP answer to a 70KiB request header (want a 4xx): %q", text)
	}
	fields := strings.Fields(strings.SplitN(text, "\r\n", 2)[0])
	code, _ := strconv.Atoi(fields[1])
	if code < 400 || code > 499 {
		t.Fatalf("oversized request header answered %d, want 4xx (MaxHeaderBytes not bounding it)", code)
	}
}

// TestW81S3OverCapReceivePackRefused: a receive-pack declaring >1GiB is
// answered 4xx before its body is streamed, with refs unchanged.
func TestW81S3OverCapReceivePackRefused(t *testing.T) {
	inst := newV1Install(t)
	wi := w81NewWI(t, inst, v1Project, "w81 s3: over-cap receive-pack")
	secret := w81Claim(t, inst, wi)
	status, body := w81DeclaredOversizeGitPost(t, inst, wi, secret, "git-receive-pack", "receive-pack", 8*time.Second)
	if status < 400 || status > 499 {
		t.Fatalf("over-cap receive-pack answered %d (%q), want 4xx before the 1GiB body is read", status, body)
	}
	if n := inst.bareCommitCount(t, wi); n != 0 {
		t.Fatalf("the refused over-cap push moved refs (main has %d commits), want them unchanged", n)
	}
	if inc, _ := filepath.Glob(filepath.Join(inst.bareRepo(wi), "objects", "incoming-*")); len(inc) != 0 {
		t.Fatalf("the refused over-cap push left quarantine residue: %v", inc)
	}
}

// TestW81S4OverCapUploadPackRefused: the same cap applies to upload-pack.
func TestW81S4OverCapUploadPackRefused(t *testing.T) {
	inst := newV1Install(t)
	wi := w81NewWI(t, inst, v1Project, "w81 s4: over-cap upload-pack")
	secret := w81Claim(t, inst, wi)
	status, body := w81DeclaredOversizeGitPost(t, inst, wi, secret, "git-upload-pack", "upload-pack", 8*time.Second)
	if status < 400 || status > 499 {
		t.Fatalf("over-cap upload-pack answered %d (%q), want 4xx before the 1GiB body is read", status, body)
	}
	if inc, _ := filepath.Glob(filepath.Join(inst.bareRepo(wi), "objects", "incoming-*")); len(inc) != 0 {
		t.Fatalf("the refused over-cap upload-pack left residue: %v", inc)
	}
}

// TestW81S5BlobCheckFailureRejects: when the blob-check pipeline's
// cat-file fails, the push must be rejected and refs left untouched. The
// old hook (set -u, no pipefail) reads an empty badblob and accepts it.
func TestW81S5BlobCheckFailureRejects(t *testing.T) {
	inst, trace := w81HookFailInstall(t, "cat-file --batch-check")
	wi := w81NewWI(t, inst, v1Project, "w81 s5: blob pipeline failure")
	secret := w81Claim(t, inst, wi)
	out, err := w81Push(t, inst, wi, secret)
	if tr := v1ReadString(trace); !strings.Contains(tr, "FAIL cat-file --batch-check") {
		t.Fatalf("the hook wrapper never failed (the test would be red for the wrong reason); wrapper trace:\n%s", tr)
	}
	if err == nil {
		t.Fatalf("the push was accepted although the blob-check pipeline failed (refs: %d commits):\n%s\n%s",
			inst.bareCommitCount(t, wi), out, w81BareState(t, inst, wi))
	}
	if n := inst.bareCommitCount(t, wi); n != 0 {
		t.Fatalf("the rejected push moved refs (main has %d commits), want them unchanged:\n%s\n%s", n, out, w81BareState(t, inst, wi))
	}
}

// TestW81S6GitlinkCheckFailureRejects: when the gitlink check's ls-tree
// fails, the push must be rejected and refs left untouched. The old hook
// treats the failed grep as "no gitlink" and accepts the push.
func TestW81S6GitlinkCheckFailureRejects(t *testing.T) {
	inst, trace := w81HookFailInstall(t, "ls-tree -r")
	wi := w81NewWI(t, inst, v1Project, "w81 s6: gitlink check failure")
	secret := w81Claim(t, inst, wi)
	out, err := w81Push(t, inst, wi, secret)
	if tr := v1ReadString(trace); !strings.Contains(tr, "FAIL ls-tree -r") {
		t.Fatalf("the hook wrapper never failed (the test would be red for the wrong reason); wrapper trace:\n%s", tr)
	}
	if err == nil {
		t.Fatalf("the push was accepted although the gitlink check's ls-tree failed (refs: %d commits):\n%s\n%s",
			inst.bareCommitCount(t, wi), out, w81BareState(t, inst, wi))
	}
	if n := inst.bareCommitCount(t, wi); n != 0 {
		t.Fatalf("the rejected push moved refs (main has %d commits), want them unchanged:\n%s\n%s", n, out, w81BareState(t, inst, wi))
	}
}

// w81Memory creates one project-visible memory and answers its id.
func w81Memory(t *testing.T, inst *v1Install, project, content string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
		"project": project, "type": "fact.note", "visibility": "project",
		"title": "w81 marker", "content": content,
	}, &out); err != nil || out.ID == "" {
		t.Fatalf("create memory in %s: %v (%+v)", project, err, out)
	}
	return out.ID
}

// TestW81S7ScopedAdminSearchDoesNotLeak: a project-scoped admin key sees
// only its scope's memories. The old search path returns every project's
// matches to an admin (membership is skipped and the key scope is never
// applied), so this is the spec's behaviour correction.
func TestW81S7ScopedAdminSearchDoesNotLeak(t *testing.T) {
	inst := newV1Install(t)
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: "w81other", Description: "w81 scope boundary",
	}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	mine := w81Memory(t, inst, v1Project, "w81leak: memory inside the scoped project")
	foreign := w81Memory(t, inst, "w81other", "w81leak: memory outside the scoped project")

	var p struct {
		ID string `json:"id"`
	}
	rawProject, err := inst.client.GetProject(v1Project)
	if err != nil || json.Unmarshal(rawProject, &p) != nil || p.ID == "" {
		t.Fatalf("get project %s: %v (%s)", v1Project, err, rawProject)
	}
	var key struct {
		Plain string `json:"plain"`
	}
	scope := p.ID
	rawKey, err := inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{
		UserID: "usr_root", Name: "w81-scoped-admin", ProjectID: &scope,
	})
	if err != nil || json.Unmarshal(rawKey, &key) != nil || key.Plain == "" {
		t.Fatalf("mint scoped admin key: %v (%s)", err, rawKey)
	}
	scoped := aihubclient.New(inst.url, key.Plain)
	rawSearch, err := scoped.SearchMemories("", "w81leak", "", nil, 50, "", "", "")
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	var ans struct {
		Items []struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawSearch, &ans); err != nil {
		t.Fatalf("search answer: %v (%s)", err, rawSearch)
	}
	if len(ans.Items) != 1 || ans.Items[0].ID != mine || ans.Items[0].ProjectID != p.ID {
		t.Fatalf("scoped admin search = %+v, want exactly the in-scope memory %s (the out-of-scope match is %s): %s",
			ans.Items, mine, foreign, rawSearch)
	}
}

// TestW81S8ReadableListPageNotEmpty: a non-admin member with readable WIs
// still gets them when newer unreadable WIs fill the unfiltered page. The
// old list truncates first and filters row by row afterwards, so it
// answers an empty page here.
func TestW81S8ReadableListPageNotEmpty(t *testing.T) {
	inst := newV1Install(t)
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: "w81other", Description: "w81 foreign project",
	}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	readable := w81NewWI(t, inst, v1Project, "w81 s8: the member's readable work item")
	for i := 0; i < 3; i++ {
		w81NewWI(t, inst, "w81other", fmt.Sprintf("w81 s8: newer unreadable item %d", i))
	}

	var user struct {
		ID string `json:"id"`
	}
	rawUser, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: "w81 member", Email: "w81@example.invalid", UserType: "human", Role: "writer",
	})
	if err != nil || json.Unmarshal(rawUser, &user) != nil || user.ID == "" {
		t.Fatalf("create member user: %v (%s)", err, rawUser)
	}
	members := []aihubclient.ProjectMember{{UserID: user.ID, Role: "viewer"}}
	if _, err := inst.client.PatchProject(v1Project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	var key struct {
		Plain string `json:"plain"`
	}
	rawKey, err := inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{
		UserID: user.ID, Name: "w81-member",
	})
	if err != nil || json.Unmarshal(rawKey, &key) != nil || key.Plain == "" {
		t.Fatalf("mint member key: %v (%s)", err, rawKey)
	}
	member := aihubclient.New(inst.url, key.Plain)
	rawList, err := member.ListWorkItemsRaw("", "", 2)
	if err != nil {
		t.Fatalf("member list: %v", err)
	}
	var ans struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rawList, &ans); err != nil {
		t.Fatalf("list answer: %v (%s)", err, rawList)
	}
	if len(ans.Items) == 0 {
		t.Fatalf("?limit=2 answered an empty page although the member can read %s (newer unreadable items filled the page): %s",
			readable, rawList)
	}
	found := false
	for _, it := range ans.Items {
		if it.ID == readable {
			found = true
		}
	}
	if !found || ans.Total != len(ans.Items) {
		t.Fatalf("member page = %+v (total %d), want it to carry %s with total == len(items): %s",
			ans.Items, ans.Total, readable, rawList)
	}
}
