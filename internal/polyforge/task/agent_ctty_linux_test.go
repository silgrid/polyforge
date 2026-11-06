package task

import (
	"bufio"
	"context"
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

// agentCttyHelperEnv switches the test binary into the role of a daemon
// whose controlling terminal is a pty (TestTaskAgentHasNoControllingTerminal).
const agentCttyHelperEnv = "PF_TEST_AGENT_CTTY_HELPER"

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

// TestTaskAgentHasNoControllingTerminal: a daemon running on a terminal
// (tmux's pty) dispatches a task agent that holds no controlling terminal.
func TestTaskAgentHasNoControllingTerminal(t *testing.T) {
	master, slave := openPTY(t)
	go func() { _, _ = io.Copy(io.Discard, master) }()
	resR, resW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer resR.Close()
	h := exec.Command(os.Args[0], "-test.run=^TestTaskAgentCttyHelper$", "-test.count=1", "-test.timeout=5m")
	h.Env = append(os.Environ(), agentCttyHelperEnv+"=1")
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
	var daemonTTY, agentTTY int
	if _, err := fmt.Sscanf(line, "daemon=%d agent=%d", &daemonTTY, &agentTTY); err != nil {
		t.Fatalf("the helper reported no tty numbers (read %q: %v)", line, rerr)
	}
	if daemonTTY == 0 {
		t.Fatal("precondition: the helper daemon has no controlling terminal")
	}
	if agentTTY != 0 {
		t.Fatalf("the task agent holds the daemon's controlling terminal (tty_nr %d)", agentTTY)
	}
}

// TestTaskAgentCttyHelper is not a test: re-executed on a pty by
// TestTaskAgentHasNoControllingTerminal, it runs a task agent the way the
// daemon does (the agent reports its own tty number as its text) and reports
// both tty numbers on fd 3.
func TestTaskAgentCttyHelper(t *testing.T) {
	if os.Getenv(agentCttyHelperEnv) == "" {
		t.Skip("helper process only: re-executed by TestTaskAgentHasNoControllingTerminal")
	}
	out := os.NewFile(3, "result")
	r := fakePi(t, `cat >/dev/null
n=$(cut -d' ' -f7 /proc/$$/stat)
echo '{"type":"message_update","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0}}'
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"'"$n"'"}]}}'
`)
	res, err := r.Run(context.Background(), piSpec(t, 1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	self, err := ttyNr(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(out, "daemon=%d agent=%s\n", self, res.Report)
}
