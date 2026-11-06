package main

// harness.go: what the scripted agent reads of its capsule and the
// environment its scripted commands run with.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// gate reports whether step is a gate of the capsule's pinned lane.
func (d *driveEnv) gate(step string) bool {
	root := d.capsuleRoot()
	raw, err := os.ReadFile(filepath.Join(root, "capsule.json"))
	if err != nil {
		return false
	}
	var idn struct {
		BundleName string `json:"bundle_name"`
	}
	if json.Unmarshal(raw, &idn) != nil || idn.BundleName == "" {
		return false
	}
	wf, err := driver.LoadWorkflow(filepath.Join(root, "workflows", idn.BundleName))
	return err == nil && wf.IsGate(step)
}

// harnessBinDir answers this binary's dir (the harness's bin dir).
func harnessBinDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("fakepi cannot locate itself: %v", err)
	}
	return filepath.Dir(exe), nil
}

// harnessPf answers the harness's pf (the binary under test beside this
// one), else a bare "pf" when this binary cannot locate itself.
func harnessPf() string {
	dir, err := harnessBinDir()
	if err != nil {
		return "pf"
	}
	return filepath.Join(dir, "pf")
}

// harnessEnv is the environment a scripted command runs with: the
// harness's bin dir first on PATH, so a bare `pf` in a script is always the
// binary under test, never the machine's installed CLI.
func harnessEnv() []string {
	dir, err := harnessBinDir()
	if err != nil {
		return os.Environ()
	}
	return append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
