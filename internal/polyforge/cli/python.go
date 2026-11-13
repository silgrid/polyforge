package cli

// python.go: the Python interpreter a workflow's step scripts run under
// (the workflow contract's "Scripts" section, docs/reference/workflow-
// contract.md). One rule, two readers: the daemon resolves
// it once at its start, refuses to start without a usable one and hands it
// to every step as PF_PYTHON (daemon.Main); `pf doctor`'s python3 row
// judges the same rule in its own process (doctor_python.go).
//
// The rule: `[daemon] python` (an absolute path) when set, else `python3`
// on the PATH; its version, asked of the interpreter itself, is at least
// MinPython. Nothing else is tried: a configured interpreter that does not
// answer is refused, never replaced by the PATH's.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MinPython is the oldest interpreter the step scripts support: the shared
// library (workflows/_shared/scripts/pfstep) is written for it, CI runs its
// tests on it, and it is what macOS's command-line tools ship.
var MinPython = [2]int{3, 9}

// MinPythonString is MinPython as people write it ("3.9").
func MinPythonString() string { return fmt.Sprintf("%d.%d", MinPython[0], MinPython[1]) }

// The interpreter's sources, as the refusals and the doctor row name them.
const (
	PythonSourceConfig = "[daemon] python"
	PythonSourcePath   = "python3 on PATH"
)

// PythonInterp is a resolved interpreter.
type PythonInterp struct {
	// Path is the interpreter's absolute path (PF_PYTHON).
	Path string
	// Version is what the interpreter reports ("3.12.3").
	Version string
	// Source is PythonSourceConfig or PythonSourcePath.
	Source string
}

// pythonProbeScript is the version question: -I keeps every PYTHON*
// variable and the user site out of it.
const pythonProbeScript = "import sys; print('%d.%d.%d' % tuple(sys.version_info[:3]))"

// pythonProbeTimeout bounds the version question.
const pythonProbeTimeout = 10 * time.Second

// ProbePython asks the interpreter at path for its version ("3.12.3").
func ProbePython(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-I", "-c", pythonProbeScript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s does not answer its version: %s", path, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ResolvePython answers the step scripts' interpreter under the rule above:
// configured ([daemon] python, "" = unset), else python3 on lookPath's
// PATH, asked for its version by probe. The error names the source, what
// was found and the remedy.
func ResolvePython(configured string, lookPath func(string) (string, error), probe func(string) (string, error)) (PythonInterp, error) {
	in := PythonInterp{Path: configured, Source: PythonSourceConfig}
	remedy := fmt.Sprintf("install Python >= %s, or point [daemon] python at one (`pf config set python <absolute path>`)", MinPythonString())
	if configured == "" {
		in.Source = PythonSourcePath
		p, err := lookPath("python3")
		if err != nil {
			return in, fmt.Errorf("python3 is not on PATH (the workflow step scripts run under it): %s", remedy)
		}
		if abs, aerr := filepath.Abs(p); aerr == nil {
			p = abs
		}
		in.Path = p
	}
	v, err := probe(in.Path)
	if err != nil {
		return in, fmt.Errorf("%s %s: %v: %s", in.Source, in.Path, err, remedy)
	}
	in.Version = v
	major, minor, ok := parsePythonVersion(v)
	if !ok {
		return in, fmt.Errorf("%s %s answered %q, not a Python version: %s", in.Source, in.Path, v, remedy)
	}
	if major < MinPython[0] || (major == MinPython[0] && minor < MinPython[1]) {
		return in, fmt.Errorf("%s %s is Python %s, older than %s (the workflow step scripts need %s or newer): %s",
			in.Source, in.Path, v, MinPythonString(), MinPythonString(), remedy)
	}
	return in, nil
}

// parsePythonVersion reads "<major>.<minor>[.<micro>]".
func parsePythonVersion(v string) (int, int, bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, false
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], true
}
