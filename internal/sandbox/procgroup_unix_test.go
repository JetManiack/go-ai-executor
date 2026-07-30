//go:build unix

package sandbox

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// commandTimeout is deliberately far longer than any deadline these tests wait
// on. An earlier version passed 5s here and asserted with a 5s deadline, so a
// process that survived the kill was reaped by the command's own timeout just in
// time for the assertion to pass — the leak these tests exist to catch was
// invisible.
const commandTimeout = 60 * time.Second

// promptly is how long a correct kill may take: signal delivery plus one
// convergence pass, with room for a loaded machine.
const promptly = 3 * time.Second

// alive reports whether pid still exists. Signal 0 performs the permission and
// existence checks without delivering anything.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitGone polls until pid disappears, because a SIGKILL is asynchronous: the
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

// backgroundedChildPID runs a command that backgrounds a long sleep, prints that
// child's PID and then blocks, returning the PID once it has been printed.
//
// This is the shape that used to leak: exec.Cmd signals only the direct child —
// the shell — so the backgrounded sleep was reparented and outlived both the
// timeout and any attempt to stop the sandbox.
func backgroundedChildPID(t *testing.T, sb *Sandbox, bus *Broadcaster) (int, <-chan ExecResult) {
	t.Helper()

	_, sub := bus.Subscribe(sb.ID(), 0)
	t.Cleanup(func() { bus.Unsubscribe(sb.ID(), sub) })

	results := make(chan ExecResult, 1)
	go func() {
		res, _ := sb.ExecCommand(context.Background(), "sleep 60 & echo $!; sleep 60", commandTimeout, "")
		results <- res
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-sub.Events():
			if e.Kind != EventStdout {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(e.Data))
			if err != nil {
				t.Fatalf("expected a PID on stdout, got %q: %v", e.Data, err)
			}
			return pid, results
		case <-deadline:
			t.Fatal("timed out waiting for the backgrounded child's PID")
			return 0, results
		}
	}
}

func TestKillAllTearsDownBackgroundedChildren(t *testing.T) {
	sb, bus := newStreamingSandbox(t)
	childPID, results := backgroundedChildPID(t, sb, bus)

	if !alive(childPID) {
		t.Fatalf("backgrounded child %d is not running, so the test would prove nothing", childPID)
	}

	killed, err := sb.KillAll("Grace", "runaway loop")
	if err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	if killed != 1 {
		t.Errorf("killed %d process groups, want 1", killed)
	}

	if !waitGone(childPID, promptly) {
		t.Errorf("backgrounded child %d survived the kill", childPID)
	}

	// The real symptom of a leaked group member is here rather than in the PID
	// check: a survivor holds the command's stdout pipe open, so the tool call
	// blocks until its own timeout instead of returning once the processes are
	// gone.
	select {
	case <-results:
	case <-time.After(promptly):
		t.Error("ExecCommand did not return promptly after its process group was killed, so something in the group survived holding its output pipe")
	}
}

// TestTimeoutTearsDownBackgroundedChildren covers the same leak on the timeout
// path, which is the one that fires without anybody watching.
func TestTimeoutTearsDownBackgroundedChildren(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sb, err := mgr.GetSandbox("agent-1")
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	bus := mgr.Broadcaster()
	_, sub := bus.Subscribe(sb.ID(), 0)
	defer bus.Unsubscribe(sb.ID(), sub)

	results := make(chan ExecResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, execErr := sb.ExecCommand(context.Background(), "sleep 60 & echo $!; sleep 60", 500*time.Millisecond, "")
		results <- res
		errs <- execErr
	}()

	var childPID int
	deadline := time.After(5 * time.Second)
	for childPID == 0 {
		select {
		case e := <-sub.Events():
			if e.Kind != EventStdout {
				continue
			}
			pid, convErr := strconv.Atoi(strings.TrimSpace(e.Data))
			if convErr != nil {
				t.Fatalf("expected a PID on stdout, got %q: %v", e.Data, convErr)
			}
			childPID = pid
		case <-deadline:
			t.Fatal("timed out waiting for the backgrounded child's PID")
		}
	}

	select {
	case execErr := <-errs:
		if !errors.Is(execErr, ErrCommandTimeout) {
			t.Errorf("error = %v, want ErrCommandTimeout", execErr)
		}
	case <-time.After(promptly):
		t.Fatal("ExecCommand did not return after the timeout")
	}
	<-results

	if !waitGone(childPID, promptly) {
		t.Errorf("backgrounded child %d outlived the command timeout", childPID)
	}
}

func TestKillAllOnIdleSandbox(t *testing.T) {
	sb, _ := newStreamingSandbox(t)

	// Nothing running is the state the caller asked for, not an error: blocking
	// an idle sandbox is still meaningful, since the block stops its next call.
	killed, err := sb.KillAll("Grace", "just blocking")
	if err != nil {
		t.Fatalf("KillAll on an idle sandbox: %v", err)
	}
	if killed != 0 {
		t.Errorf("killed = %d, want 0", killed)
	}
}

func TestKillAllPublishesKilledEvent(t *testing.T) {
	sb, bus := newStreamingSandbox(t)
	_, results := backgroundedChildPID(t, sb, bus)

	// A separate watcher, so the events consumed while waiting for the PID
	// aren't missing from what this assertion reads.
	_, sub := bus.Subscribe(sb.ID(), bus.LastSeq(sb.ID()))
	defer bus.Unsubscribe(sb.ID(), sub)

	if _, err := sb.KillAll("Grace", "runaway loop"); err != nil {
		t.Fatalf("KillAll: %v", err)
	}

	deadline := time.After(promptly)
	for {
		select {
		case e := <-sub.Events():
			if e.Kind != EventKilled {
				continue
			}
			if e.ByActor != "Grace" || e.Reason != "runaway loop" {
				t.Errorf("killed event = %+v, want it to name who stopped it and why", e)
			}
			<-results
			return
		case <-deadline:
			t.Fatal("no killed event reached the terminal stream, so a watcher would see output simply stop")
		}
	}
}

func TestRunningCommandsTracksExecutions(t *testing.T) {
	sb, bus := newStreamingSandbox(t)

	if got := sb.RunningCommands(); got != 0 {
		t.Fatalf("running commands on a fresh sandbox = %d, want 0", got)
	}

	_, results := backgroundedChildPID(t, sb, bus)
	if got := sb.RunningCommands(); got != 1 {
		t.Errorf("running commands during execution = %d, want 1", got)
	}

	if _, err := sb.KillAll("Grace", "cleanup"); err != nil {
		t.Fatalf("KillAll: %v", err)
	}
	<-results

	if got := sb.RunningCommands(); got != 0 {
		t.Errorf("running commands after the command returned = %d, want 0", got)
	}
}

// TestKillConvergesOnProcessesForkedDuringTheKill is the regression test for a
// real leak: `kill(-pgid, SIGKILL)` reaches only the members that exist when it
// is delivered, so a shell midway through forking its next command produces a
// child that inherits the group and never gets the signal. Reparented to init and
// still holding the command's stdout pipe, it kept ExecCommand blocked until the
// command's own timeout — an emergency stop that visibly did not stop anything.
//
// The command below forks repeatedly for the whole run, so the kill is very
// likely to land inside one of those windows.
func TestKillConvergesOnProcessesForkedDuringTheKill(t *testing.T) {
	sb, bus := newStreamingSandbox(t)
	_, sub := bus.Subscribe(sb.ID(), 0)
	defer bus.Unsubscribe(sb.ID(), sub)

	results := make(chan ExecResult, 1)
	go func() {
		res, _ := sb.ExecCommand(context.Background(),
			"while true; do sleep 30 & sleep 0.01; done", commandTimeout, "")
		results <- res
	}()

	// Wait until the loop is definitely running and forking.
	deadline := time.After(promptly)
	for started := false; !started; {
		select {
		case e := <-sub.Events():
			if e.Kind == EventStarted {
				started = true
			}
		case <-deadline:
			t.Fatal("command never started")
		}
	}
	time.Sleep(200 * time.Millisecond)

	if _, err := sb.KillAll("Grace", "runaway fork loop"); err != nil {
		t.Fatalf("KillAll: %v", err)
	}

	select {
	case <-results:
	case <-time.After(promptly):
		t.Fatal("ExecCommand did not return promptly after the kill: a process forked during the kill survived and is holding the output pipe")
	}
}
