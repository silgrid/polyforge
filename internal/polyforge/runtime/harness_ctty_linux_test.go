package runtime

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// harnessCttyHelperEnv switches the test binary into the role of a daemon
// whose controlling terminal is a pty (TestStepHarnessHasNoControllingTerminal).
const harnessCttyHelperEnv = "PF_TEST_HARNESS_CTTY_HELPER"

// ttyNr answers field 7 of /proc/<pid>/stat: the process's controlling
// terminal (0 = none).
func ttyNr(pid int) (int, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	s := string(raw)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(f) < 5 {
		return 0, fmt.Errorf("short stat %q", s)
	}
	return strconv.Atoi(f[4]) // state ppid pgrp session tty_nr
}

// openPTY opens a pty pair: the master and its slave, neither becoming the
// caller's controlling terminal.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open a pty: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock the pty: %v", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pty number: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the pty slave: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return m, s
}

// TestStepHarnessHasNoControllingTerminal: a daemon running on a terminal
// (tmux's pty) starts a step harness that holds no controlling terminal, so
// nothing the step runs can open /dev/tty and wait on the daemon's terminal.
func TestStepHarnessHasNoControllingTerminal(t *testing.T) {
	master, slave := openPTY(t)
	go func() { _, _ = io.Copy(io.Discard, master) }()
	resR, resW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer resR.Close()
	h := exec.Command(os.Args[0], "-test.run=^TestStepHarnessCttyHelper$", "-test.count=1", "-test.timeout=5m")
	h.Env = append(os.Environ(), harnessCttyHelperEnv+"=1")
	h.Stdin, h.Stdout, h.Stderr = slave, slave, slave
	h.ExtraFiles = []*os.File{resW}
	// a new session whose controlling terminal is the pty (fd 0)
	h.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	resW.Close()
	defer func() { _ = h.Process.Kill(); _ = h.Wait() }()
	if err := resR.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, rerr := bufio.NewReader(resR).ReadString('\n')
	var daemonTTY, harnessTTY int
	if _, err := fmt.Sscanf(line, "daemon=%d harness=%d", &daemonTTY, &harnessTTY); err != nil {
		t.Fatalf("the helper reported no tty numbers (read %q: %v)", line, rerr)
	}
	if daemonTTY == 0 {
		t.Fatal("precondition: the helper daemon has no controlling terminal")
	}
	if harnessTTY != 0 {
		t.Fatalf("the step harness holds the daemon's controlling terminal (tty_nr %d)", harnessTTY)
	}
}

// TestStepHarnessCttyHelper is not a test: re-executed on a pty by
// TestStepHarnessHasNoControllingTerminal, it starts a step harness the way
// the daemon does and reports both tty numbers on fd 3.
func TestStepHarnessCttyHelper(t *testing.T) {
	if os.Getenv(harnessCttyHelperEnv) == "" {
		t.Skip("helper process only: re-executed by TestStepHarnessHasNoControllingTerminal")
	}
	out := os.NewFile(3, "result")
	pr, err := startHarness(exec.Command("cat"), SpawnParams{WorkItemID: "wi_ctty"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Terminate(5 * time.Second) }()
	self, err := ttyNr(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	child, err := ttyNr(pr.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(out, "daemon=%d harness=%d\n", self, child)
}
