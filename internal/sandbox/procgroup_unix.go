//go:build unix

package sandbox

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// killPasses and killPassDelay bound the convergence loop in
// killProcessGroup. Three passes is enough in practice — see the comment there
// for why more than one is needed at all — and the delay only has to cover the
// scheduling of a process that is already doomed.
const (
	killPasses    = 3
	killPassDelay = 20 * time.Millisecond
)

// setProcessGroup puts the command in its own process group, so the whole tree it
// spawns can be signalled at once.
//
// Without this, exec.Cmd's cancellation signals only the direct child — the
// shell — and anything it backgrounded (`sh -c 'sleep 999 & sleep 999'`) is
// reparented and survives both the timeout and the emergency stop.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs every process in the group led by pid.
//
// Setpgid makes the child's process-group ID equal its PID, so the negated PID
// addresses the group. SIGKILL rather than SIGTERM: this is the emergency stop
// and the timeout path, where a process that chose to ignore a polite signal is
// exactly the case being handled.
//
// One kill(-pgid, SIGKILL) is not enough, and that is not a theoretical concern
// — it was observed. The signal reaches the members that exist at that instant,
// so a shell partway through forking (`cmd & other-cmd`) can produce a child a
// moment later that inherits the group and never receives it. Its parent dies,
// it is reparented to init, and it keeps the command's stdout pipe open: the tool
// call then blocks until its own timeout, and the operator who pressed "stop"
// watches the sandbox carry on running.
//
// Hence: SIGSTOP the group first, because a stopped process cannot complete a
// fork; SIGKILL it; SIGCONT so anything that was merely stopped proceeds to die.
// Then repeat, because the only thing that can add a member to a group is an
// existing member, and after the first pass there are none left to fork.
func killProcessGroup(pid int) error {
	var firstErr error

	for pass := range killPasses {
		// SIGSTOP and SIGCONT are best-effort: the group emptying out between
		// passes is the expected outcome, not a failure.
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
		err := syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(-pid, syscall.SIGCONT)

		if pass == 0 {
			// The first pass is the one whose outcome the caller cares about:
			// later passes report ESRCH precisely when they succeeded.
			firstErr = err
		}
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if pass < killPasses-1 {
			time.Sleep(killPassDelay)
		}
	}

	return firstErr
}
