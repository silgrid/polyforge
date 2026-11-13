package stepscripts

// scripts_test.go: running the workflow's step scripts
// (workflows/_shared/scripts/<name>.py) from these black-box tests, as a
// step's skill runs them — `python3 <script> <args>` under the step
// environment — with the machine's two outside faces faked on PATH: `pf`
// (its `pf --json step hub list/get`, answered from a fakeHubReads) and
// `docker` (every "container" command run here on the host, every call
// logged, so a test reads the containers a script started and removed).

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// scriptOf answers the workflow script a name invoke is given runs (the
// scripts' result files name them by file name), else "".
func scriptOf(name string) string {
	switch name {
	case NameFacts, NameIntegrate, NameGate:
		return name
	}
	return ""
}

// runScript runs script with args under env (PATH prefixed with the
// given directories), answering the exit code and the two streams.
func runScript(t *testing.T, script string, args, env []string, pathDirs ...string) (int, string, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	c := exec.Command(py, append([]string{filepath.Join(scriptsDir(t), script)}, args...)...)
	c.Env = withPath(env, pathDirs...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err = c.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String(), errb.String()
	case errors.As(err, &ee):
		return ee.ExitCode(), out.String(), errb.String()
	}
	t.Fatalf("%s: %v\n%s", script, err, errb.String())
	return 0, "", ""
}

// invokeScript runs the script that took over the step command name, as
// invoke runs a command: under env, `pf step hub` answered by hub's fake
// pf and the containers by rt's fake docker (their calls recorded on them),
// the result file it wrote checked against the published schema.
func invokeScript(t *testing.T, name string, args, env []string, rt *fakeRuntime, hub *fakeHubReads) (int, string, string) {
	t.Helper()
	var dirs []string
	dockerDir, pfDir := "", ""
	if rt != nil {
		dockerDir = rt.dockerDir(t)
		dirs = append(dirs, dockerDir)
	}
	if hub != nil {
		pfDir = hub.pfDir(t)
		dirs = append(dirs, pfDir)
	}
	if name == NameGate {
		// the gate's isolation, as every caller writes it
		args = append(append([]string{}, gateIsolation...), args...)
	}
	exit, stdout, stderr := runScript(t, scriptOf(name), args, env, dirs...)
	if rt != nil {
		rt.absorbDockerLog(t, dockerDir)
	}
	if hub != nil {
		hub.readCalls(pfDir)
	}
	checkWrittenResult(t, args)
	return exit, stdout, stderr
}

// withPath answers env with dirs put first on its PATH.
func withPath(env []string, dirs ...string) []string {
	if len(dirs) == 0 {
		return env
	}
	pre := strings.Join(dirs, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	seen := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			kv, seen = "PATH="+pre+string(os.PathListSeparator)+kv[len("PATH="):], true
		}
		out = append(out, kv)
	}
	if !seen {
		out = append(out, "PATH="+pre)
	}
	return out
}

// fakePF is `pf --json step hub list/get` answered from h's state at the
// time of the call's start (written beside the script), each call
// recorded.
const fakePF = `#!/usr/bin/env python3
import json, os, sys
state_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "hub.json")
with open(state_path) as f:
    state = json.load(f)
args = sys.argv[1:]
if args[:3] != ["--json", "step", "hub"]:
    sys.stderr.write("fake pf: only --json step hub, got %r\n" % args)
    sys.exit(2)
a = args[3:]
with open(state_path + ".calls", "a") as f:
    f.write(" ".join(a) + "\n")
fail = state.get("fail")
if fail:
    if fail.get("code"):
        sys.stderr.write(json.dumps(fail) + "\n")
    else:
        sys.stderr.write(fail.get("detail", "") + "\n")
    sys.exit(1)
if a[0] == "list":
    flags = {}
    i = 1
    while i + 1 < len(a):
        flags[a[i]] = a[i + 1]
        i += 2
    lst = state["batches"] if flags.get("--bundle") else state["items"]
    print(json.dumps({"items": lst}))
    sys.exit(0)
if a[0] == "get":
    for it in state["items"] + state["batches"] + state["tickets"]:
        if it.get("id") == a[1] or it.get("slug") == a[1]:
            print(json.dumps(it))
            sys.exit(0)
    sys.stderr.write(json.dumps({"code": "DAEMON-404", "detail": "get: work item %s not found in project p" % a[1]}) + "\n")
    sys.exit(1)
sys.stderr.write("no verb %s\n" % a[0])
sys.exit(1)
`

// pfDir writes the fake pf answering from h's current state and answers
// its directory (for PATH); readCalls appends the calls it recorded to
// h.calls.
func (h *fakeHubReads) pfDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	state := map[string]any{"items": orEmpty(h.items), "batches": orEmpty(h.batches), "tickets": orEmpty(h.tickets)}
	if h.fail != nil {
		var ae *api.Error
		if errors.As(h.fail, &ae) {
			state["fail"] = map[string]string{"code": ae.Code, "detail": ae.Detail}
		} else {
			state["fail"] = map[string]string{"detail": h.fail.Error()}
		}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pf"), []byte(fakePF), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// readCalls appends the calls the fake pf in dir recorded to h.calls.
func (h *fakeHubReads) readCalls(dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "hub.json.calls"))
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			h.calls = append(h.calls, l)
		}
	}
}

func orEmpty(xs []api.WorkItem) []api.WorkItem {
	if xs == nil {
		return []api.WorkItem{}
	}
	return xs
}

// fakeDocker is a docker client whose "containers" run every command here
// on the host — in the same directory, under the image's PATH and the
// environment the call adds — each call logged (FAKE_DOCKER_LOG beside
// it); FAKE_DOCKER_DOWN beside it makes the engine unavailable.
const fakeDocker = `#!/usr/bin/env python3
import json, os, shutil, subprocess, sys
here = os.path.dirname(os.path.abspath(__file__))
args = sys.argv[1:]
with open(os.path.join(here, "docker.log"), "a") as f:
    f.write(json.dumps(args) + "\n")
if os.path.exists(os.path.join(here, "down")):
    sys.stderr.write("Cannot connect to the Docker daemon (fake: down)\n")
    sys.exit(1)
cmd = args[0] if args else ""
def img(tag):
    return os.path.join(here, "img-" + tag.replace("/", "_").replace(":", "_"))
if cmd == "version":
    print("27.0.0-fake"); sys.exit(0)
if cmd == "image" and args[1] == "ls":
    tag = [a for a in args if a.startswith("reference=")][0][len("reference="):]
    if os.path.exists(img(tag)):
        print("sha256:fake")
    sys.exit(0)
if cmd == "image" and args[1] == "inspect":
    print(json.dumps(["PATH=" + os.environ.get("PATH", "/usr/bin:/bin")])); sys.exit(0)
if cmd == "build":
    sys.stdin.read()
    open(img(args[args.index("--tag") + 1]), "w").close()
    print("Step 1/1 : FROM fake"); sys.exit(0)
if cmd == "run":
    n = len([l for l in open(os.path.join(here, "docker.log")) if l.startswith('["run"')])
    print("fakecid%d" % n); sys.exit(0)
if cmd == "exec":
    i, workdir, env = 1, None, {}
    while i < len(args) and args[i].startswith("--"):
        if args[i] == "--workdir":
            workdir = args[i + 1]
        elif args[i] == "--env":
            k, _, v = args[i + 1].partition("=")
            env[k] = v
        i += 2
    argv = args[i + 1:]
    if workdir is None and argv[:2] == ["sh", "-c"]:
        script = argv[2]
        if "kill -9" in script:
            for p in argv[4:]:
                shutil.rmtree(p, ignore_errors=True)
            sys.exit(0)
        if "keepalive" in script:
            sys.exit(0)
    full = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": os.environ.get("HOME", "/")}
    full.update(env)
    sys.exit(subprocess.run(argv, cwd=workdir, env=full).returncode)
if cmd in ("rm", "ps", "volume"):
    sys.exit(0)
sys.stderr.write("fake docker: no %s\n" % cmd); sys.exit(1)
`

// dockerDir writes the fake docker client (down when rt's engine is
// unavailable) and answers its directory (for PATH).
func (f *fakeRuntime) dockerDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if f.unavailable != nil {
		if err := os.WriteFile(filepath.Join(dir, "down"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// absorbDockerLog records the containers the fake docker in dir started
// (their labels and bind mounts) and removed, as the in-process runtime
// records them.
func (f *fakeRuntime) absorbDockerLog(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "docker.log"))
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var args []string
		if json.Unmarshal([]byte(l), &args) != nil || len(args) == 0 {
			continue
		}
		switch args[0] {
		case "run":
			spec := containerSpec{Labels: map[string]string{}}
			for i := 0; i+1 < len(args); i++ {
				switch args[i] {
				case "--label":
					k, v, _ := strings.Cut(args[i+1], "=")
					spec.Labels[k] = v
				case "--mount":
					for _, part := range strings.Split(args[i+1], ",") {
						if p, ok := strings.CutPrefix(part, "source="); ok && strings.HasPrefix(args[i+1], "type=bind") {
							spec.Binds = append(spec.Binds, bind{Path: p})
						}
					}
				case "--name":
					spec.Name = args[i+1]
				}
			}
			f.starts = append(f.starts, spec)
		case "rm":
			f.removes = append(f.removes, args[len(args)-1])
		}
	}
}
