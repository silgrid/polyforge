package supervisor

// container_test.go (polyforge#160, Q2 C): the "under the container
// supervisor" judgment — the marker variable AND a container marker
// file, never either alone. Every probe runs against test-owned marker
// files (the package variable is the seam), so the machine the test
// happens to run in — a dev or verification container with a real
// /.dockerenv — cannot flip a case.

import (
	"os"
	"path/filepath"
	"testing"
)

// containerEnv points the container probe at files the test owns and
// restores it after.
func containerEnv(t *testing.T, markerFile string) []string {
	t.Helper()
	old := ContainerMarkerFiles
	if markerFile == "" {
		ContainerMarkerFiles = nil
	} else {
		ContainerMarkerFiles = []string{markerFile}
	}
	t.Cleanup(func() { ContainerMarkerFiles = old })
	if markerFile != "" {
		if err := os.WriteFile(markerFile, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ContainerMarkerFiles
}

// TestSuperviseContainerDetection: the four combinations of Q2 C —
func TestSuperviseContainerDetection(t *testing.T) {
	dir := t.TempDir()
	markerFile := filepath.Join(dir, ".containerenv")

	// both: under the container supervisor
	containerEnv(t, markerFile)
	if !ContainerSupervisedEnv([]string{"A=1", MarkerEnv + "=" + MarkerContainerValue}) {
		t.Fatal("the marker variable plus the container file must say container-supervised")
	}
	// the variable alone (a host shell that inherited it): NOT supervised
	ContainerMarkerFiles = nil
	if ContainerSupervisedEnv([]string{"A=1", MarkerEnv + "=" + MarkerContainerValue}) {
		t.Fatal("the marker variable alone must never say container-supervised (a host can inherit it)")
	}
	// the container file alone (any container: dev, verification): NOT
	// supervised — this test process runs inside one when it does
	ContainerMarkerFiles = []string{markerFile}
	if ContainerSupervisedEnv([]string{"A=1"}) {
		t.Fatal("the container file alone must never say container-supervised")
	}
	if ContainerSupervisedEnv(nil) {
		t.Fatal("an empty environment can never say container-supervised")
	}
	// both, but the marker names the tmux loop, not the container
	// supervisor: NOT container-supervised
	if ContainerSupervisedEnv([]string{MarkerEnv + "=tmux:" + SessionName}) {
		t.Fatal("a tmux: marker value is not the container supervisor")
	}

	// InContainer answers by the marker files alone
	ContainerMarkerFiles = []string{markerFile}
	if !InContainer() {
		t.Fatal("an existing marker file must say in-container")
	}
	ContainerMarkerFiles = []string{filepath.Join(dir, "absent")}
	if InContainer() {
		t.Fatal("no existing marker file must say not-in-container")
	}
}
