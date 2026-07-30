//go:build !unix

package procexec

import (
	"errors"
	"os/exec"
)

// errNoProcessGroups reports that this platform has no process-group API to
// address a command's whole process tree with.
var errNoProcessGroups = errors.New("process groups are not supported on this platform")

// Configure is a no-op off unix. The consequence is stated plainly rather
// than hidden: on such a platform a backgrounded grandchild outlives both the
// timeout and the emergency stop, so the deployable target stays unix (the image
// is Alpine).
func Configure(cmd *exec.Cmd) {}

func KillGroup(pid int) error {
	return errNoProcessGroups
}
