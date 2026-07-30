//go:build unix

package procexec

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitGone polls until pid disappears, because SIGKILL is asynchronous: the
// signal returns before the kernel has reaped the process.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !alive(pid)
}

// TestKillGroupTearsDownBackgroundedChildren covers the reason Configure exists:
// exec.Cmd's own cancellation signals only the direct child, so anything the
// child backgrounded is reparented and survives.
func TestKillGroupTearsDownBackgroundedChildren(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & echo $!; sleep 60")
	Configure(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = KillGroup(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil {
		t.Fatalf("read the backgrounded child's PID: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatalf("parse the PID from %q: %v", buf[:n], err)
	}
	if !alive(childPID) {
		t.Fatalf("backgrounded child %d is not running, so the test would prove nothing", childPID)
	}

	if err := KillGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	if !waitGone(childPID, 3*time.Second) {
		t.Errorf("backgrounded child %d survived the group kill", childPID)
	}
}

// TestKillGroupConvergesOnProcessesForkedDuringTheKill is the regression test for
// a real leak. kill(-pgid, SIGKILL) reaches only the members that exist when it is
// delivered, so a shell midway through forking its next command produces a child
// that inherits the group and never gets the signal — reparented to init, and
// still holding whatever pipes it inherited.
//
// The command below forks continuously, so the kill is very likely to land inside
// one of those windows. A single-pass kill fails this; the converging one passes.
func TestKillGroupConvergesOnProcessesForkedDuringTheKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "while true; do sleep 30 & sleep 0.01; done")
	Configure(cmd)
	cmd.Cancel = func() error { return KillGroup(cmd.Process.Pid) }
	cmd.WaitDelay = WaitDelay

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let the loop get going and fork a few times.
	time.Sleep(200 * time.Millisecond)

	if err := KillGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}

	// A survivor holding the inherited stdout pipe is what makes this observable:
	// the read below would block until the context deadline instead of reaching
	// EOF once the group is gone.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			if _, err := stdout.Read(buf); err != nil {
				return
			}
		}
	}()

	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		t.Error("output pipe never reached EOF: a process forked during the kill survived holding it")
	}
	_ = cmd.Wait()
}

func TestKillGroupOnAGoneProcess(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	Configure(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The process has already exited and been reaped. Killing its group reports
	// ESRCH, which callers treat as the success it is — there is nothing left to
	// stop.
	err := KillGroup(cmd.Process.Pid)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("error = %v, want nil or ESRCH", err)
	}
}
