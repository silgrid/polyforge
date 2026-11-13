package cli

// doctor_host_requires_test.go: the host prerequisites row — every
// `host_requires=` a mapped clone's verify-scope.conf declares is probed
// on the host (this tree's engine lists them, a fake probe answers), and
// the row is on the doctor ladder and in the doctor verb's table; and this
// repository's own declaration is probed on this host for real.

import (
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hostReqEngine answers this tree's verify-scope engine.
func hostReqEngine(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the engine is not in this tree: %v", err)
	}
	return p
}

// hostReqClone makes <ws>/.repo/<name> a committed git clone whose
// .polyforge/verify-scope.conf holds conf (no conf file when conf is "").
func hostReqClone(t *testing.T, ws, name, conf string) {
	t.Helper()
	dir := filepath.Join(ws, ".repo", name)
	if err := os.MkdirAll(filepath.Join(dir, ".polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if conf != "" {
		if err := os.WriteFile(filepath.Join(dir, ".polyforge", "verify-scope.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestDoctorHostRequiresRow(t *testing.T) {
	engine := hostReqEngine(t)
	stepPath := os.Getenv("PATH")

	t.Run("pass", func(t *testing.T) {
		ws := filepath.Join(t.TempDir(), "ws")
		hostReqClone(t, ws, "demo", "host_requires=fake-tool|printf 'fake-tool 9.9.9\\n'|get-fake-tool --now\n")
		c := hostRequiresRow([]string{ws}, engine, stepPath, 10*time.Second)
		t.Logf("detail: %s", c.Detail)
		if c.Name != docChHostRequires || c.Status != "pass" {
			t.Fatalf("got %q/%q, want %q/pass: %s", c.Name, c.Status, docChHostRequires, c.Detail)
		}
		for _, want := range []string{"`demo`", "fake-tool", "fake-tool 9.9.9"} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("pass detail lacks %q: %s", want, c.Detail)
			}
		}
	})

	t.Run("fail", func(t *testing.T) {
		ws := filepath.Join(t.TempDir(), "ws")
		hostReqClone(t, ws, "demo", "host_requires=fake-tool|echo nope; exit 3|get-fake-tool --now\n")
		c := hostRequiresRow([]string{ws}, engine, stepPath, 10*time.Second)
		t.Logf("detail: %s", c.Detail)
		if c.Status != "fail" {
			t.Fatalf("status %q, want fail: %s", c.Status, c.Detail)
		}
		for _, want := range []string{"`demo`", "fake-tool", "exited 3", "nope", "get-fake-tool --now"} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("fail detail lacks %q: %s", want, c.Detail)
			}
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ws := filepath.Join(t.TempDir(), "ws")
		hostReqClone(t, ws, "demo", "host_requires=fake-tool|sleep 5|get-fake-tool --now\n")
		start := time.Now()
		c := hostRequiresRow([]string{ws}, engine, stepPath, 300*time.Millisecond)
		elapsed := time.Since(start)
		t.Logf("detail (%s): %s", elapsed, c.Detail)
		if c.Status != "fail" {
			t.Fatalf("status %q, want fail: %s", c.Status, c.Detail)
		}
		for _, want := range []string{"`demo`", "fake-tool", "timed out after 300ms", "get-fake-tool --now"} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("timeout detail lacks %q: %s", want, c.Detail)
			}
		}
		if elapsed >= 2*time.Second {
			t.Errorf("the row returned after %s: a timed-out probe must not hold it (bound 2s)", elapsed)
		}
	})

	t.Run("none", func(t *testing.T) {
		ws := filepath.Join(t.TempDir(), "ws")
		hostReqClone(t, ws, "demo", "# declares no host prerequisite\n")
		hostReqClone(t, ws, "plain", "")
		c := hostRequiresRow([]string{ws}, engine, stepPath, 10*time.Second)
		t.Logf("detail: %s", c.Detail)
		if c.Name != docChHostRequires || c.Status != "pass" {
			t.Fatalf("got %q/%q, want %q/pass: %s", c.Name, c.Status, docChHostRequires, c.Detail)
		}
		if !strings.Contains(c.Detail, "host_requires=") {
			t.Errorf("none detail does not say no repo declares `host_requires=`: %s", c.Detail)
		}
	})

	t.Run("registered", func(t *testing.T) {
		idx, audit := -1, -1
		for i, d := range doctorChecks {
			switch d.name {
			case docChHostRequires:
				idx = i
				if d.need != needMapping {
					t.Errorf("%q needs %d, want needMapping", d.name, d.need)
				}
			case docChAuditTools:
				audit = i
			}
		}
		if idx < 0 || idx != audit+1 {
			t.Fatalf("%q must be registered in doctorChecks right after %q (at %d, %q at %d)", docChHostRequires, docChAuditTools, idx, docChAuditTools, audit)
		}
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", "polyforge", "verbs", "doctor.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "| `"+docChHostRequires+"` |") {
			t.Errorf("skills/polyforge/verbs/doctor.md has no `%s` row", docChHostRequires)
		}
	})
}

// TestDoctorHostRequiresOwnConf: this repository's own conf declares the
// type-check fast gate's tool as a host prerequisite, its range the one pf
// CI and the verification image install, and the row probes it on this
// host for real (the PATH as is, the production timeout): it passes with
// the tool's version where the probe succeeds and fails with the install
// hint where it does not — both branches assert, neither skips.
func TestDoctorHostRequiresOwnConf(t *testing.T) {
	const (
		probe = "python3 -m mypy --version"
		rng   = `"mypy>=1.20,<2"`
		image = "mypy==1.20.2"
	)
	root := filepath.Join("..", "..", "..")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	conf := read(filepath.Join(".polyforge", "verify-scope.conf"))
	if ci := read(filepath.Join(".github", "workflows", "pf-ci.yml")); !strings.Contains(ci, rng) {
		t.Errorf(".github/workflows/pf-ci.yml no longer installs %s: the declaration's range must follow CI's", rng)
	}
	if img := read(filepath.Join(".polyforge", "Dockerfile.verify")); !strings.Contains(img, image) {
		t.Errorf(".polyforge/Dockerfile.verify no longer installs %s: the declaration's range must hold the image's pin", image)
	}

	engine := hostReqEngine(t)
	ws := filepath.Join(t.TempDir(), "ws")
	hostReqClone(t, ws, "self", conf)
	clone := filepath.Join(ws, ".repo", "self")
	cmd := exec.Command(engine, "--json", "--base", "HEAD")
	cmd.Dir = clone
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("engine --json --base HEAD in a clone carrying this repo's conf: %v\n%s", err, out)
	}
	var rep struct {
		HostRequires []hostRequire `json:"host_requires"`
		Diagnostics  []string      `json:"diagnostics"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("engine report does not decode: %v\n%s", err, out)
	}
	var decl *hostRequire
	for i, h := range rep.HostRequires {
		if h.Probe == probe {
			decl = &rep.HostRequires[i]
		}
	}
	if decl == nil {
		t.Fatalf("this repo's .polyforge/verify-scope.conf declares no host_requires= whose probe is %q (host_requires: %+v; diagnostics: %q)", probe, rep.HostRequires, rep.Diagnostics)
	}
	if !strings.Contains(decl.Hint, rng) {
		t.Errorf("the declaration's install hint %q does not carry CI's range %s", decl.Hint, rng)
	}

	// The doctor runs in the operator's own environment, where a
	// prerequisite installed per user (pip's --user site) is importable;
	// TestMain points HOME at a scratch directory, which would hide it. The
	// probe and the row see the account's home from the password database
	// for the rest of this test — the probe only reads it.
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		t.Setenv("HOME", u.HomeDir)
	}

	// The host's own answer, the same way the row asks it.
	pc := exec.Command("bash", "-c", probe)
	pc.Dir = clone
	pout, perr := pc.CombinedOutput()
	first := ""
	for _, ln := range strings.Split(string(pout), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			first = s
			break
		}
	}

	c := hostRequiresRow([]string{ws}, engine, os.Getenv("PATH"), hostRequiresProbeTimeout)
	t.Logf("probe %q on this host: err=%v, first line %q", probe, perr, first)
	t.Logf("row %q status %s; detail: %s", c.Name, c.Status, c.Detail)
	if perr == nil {
		if c.Status != "pass" {
			t.Fatalf("the probe succeeds on this host, the row is %q, want pass: %s", c.Status, c.Detail)
		}
		for _, want := range []string{"`self`", decl.Name, first} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("pass detail lacks %q: %s", want, c.Detail)
			}
		}
		return
	}
	if c.Status != "fail" {
		t.Fatalf("the probe fails on this host (%v), the row is %q, want fail: %s", perr, c.Status, c.Detail)
	}
	for _, want := range []string{"`self`", decl.Name, decl.Hint} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("fail detail lacks %q: %s", want, c.Detail)
		}
	}
}
