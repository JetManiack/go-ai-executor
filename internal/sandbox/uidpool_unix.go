//go:build unix

package sandbox

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// UIDRange is the span of user ids a worker hands out, one per agent.
//
// Zero disables the whole mechanism: commands then run as the worker's own user,
// which is what a developer's machine and the test suite need — dropping
// privileges requires CAP_SETUID, and a laptop has none.
type UIDRange struct {
	// First is the lowest id handed out; Count is how many ids follow it.
	First uint32
	Count uint32
}

// Enabled reports whether per-agent ids are in use.
func (r UIDRange) Enabled() bool { return r.Count > 0 && r.First > 0 }

// Contains reports whether id was handed out from this range.
func (r UIDRange) Contains(id uint32) bool {
	return r.Enabled() && id >= r.First && id < r.First+r.Count
}

// Validate refuses ranges that cannot work.
func (r UIDRange) Validate() error {
	if !r.Enabled() {
		return nil
	}
	// A user namespace maps 65536 ids into a pod by default, so an id above that
	// is unmappable — setuid would start failing the day the pod moves into one.
	// Refusing now costs nothing; discovering it later costs a working deployment.
	const userNamespaceIDs = 65536
	if end := uint64(r.First) + uint64(r.Count); end > userNamespaceIDs {
		return fmt.Errorf(
			"uid range %d–%d reaches past %d, which is what a user namespace maps into a pod; choose a lower range",
			r.First, end-1, userNamespaceIDs)
	}
	// Reserving nothing below the range would eventually collide with the ids the
	// distribution's own users occupy.
	const firstUnreserved = 1000
	if r.First < firstUnreserved {
		return fmt.Errorf("uid range starts at %d, inside the ids a distribution reserves for its own users", r.First)
	}
	return nil
}

// ownerOf reports the user id owning path.
func ownerOf(info fs.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}

// assignUID gives agentID a user id and makes dir belong to it.
//
// The directory's owner *is* the mapping: nothing is written down separately, so
// there is no table to drift out of step with the filesystem, and an agent
// returning to a volume it has used before lands back on files it owns.
func (m *Manager) assignUID(agentID, dir string) (uint32, error) {
	if !m.baseConfig.UIDRange.Enabled() {
		return 0, nil
	}

	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if owner, ok := ownerOf(info); ok && m.baseConfig.UIDRange.Contains(owner) {
			return owner, nil
		}
		// The directory exists but belongs to someone outside the range — a volume
		// from before per-agent ids, most likely. Re-owning it is a migration, and
		// one worth a log line: an operator seeing agent directories change hands
		// should be able to find out why.
		slog.Info("adopting a sandbox directory from before per-agent ids",
			"agent_id", agentID, "dir", dir)
	case os.IsNotExist(err):
	default:
		return 0, fmt.Errorf("inspect sandbox directory: %w", err)
	}

	uid, err := m.nextFreeUID()
	if err != nil {
		return 0, err
	}
	// The parent has to be traversable and the sandbox itself must not be, and
	// MkdirAll applies one mode to everything it creates — so they are separate
	// calls. Getting this wrong is invisible until a command runs: the agent's
	// chdir into its own sandbox fails, and Go reports it as "fork/exec: permission
	// denied" on the program, which sends you looking at the wrong file entirely.
	// 0755 on the parent is the point rather than an oversight: every agent runs as
	// a different user and each has to traverse this directory to reach its own.
	// Tightening it to 0750 would mean putting every agent in a shared group, which
	// is a worse answer to the same question. What protects an agent's files is the
	// 0700 on its own directory, below.
	parent := filepath.Dir(dir)
	// #nosec G301 -- see above
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return 0, fmt.Errorf("create the sandbox root: %w", err)
	}
	if err := adoptDir(parent, 0o755); err != nil {
		return 0, fmt.Errorf("make the sandbox root traversable: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return 0, fmt.Errorf("create sandbox directory: %w", err)
	}
	if err := chownTree(dir, uid); err != nil {
		return 0, err
	}
	return uid, nil
}

// chownTree hands dir and everything under it to uid.
//
// The order is the whole subtlety, and getting it wrong needs two capabilities
// this worker deliberately does not have:
//
//   - permissions before ownership, because once the directory belongs to the
//     agent the worker no longer owns it and chmod would need CAP_FOWNER;
//   - contents before the directory itself, because a 0700 directory owned by
//     someone else cannot be descended into without CAP_DAC_OVERRIDE.
func chownTree(dir string, uid uint32) error {
	// Adopted rather than merely chmodded, because on a volume from before
	// per-agent ids this directory belongs to the old process's user, and chmod on
	// a directory you do not own needs CAP_FOWNER.
	//
	// 0700 on a directory, not a file: without the execute bit the owner cannot
	// descend into its own sandbox. The point of the mode is the absent group and
	// other bits, which are what keep the next agent out.
	if err := adoptDir(dir, 0o700); err != nil {
		return fmt.Errorf("set sandbox permissions: %w", err)
	}

	var inner []string
	if err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != dir {
			inner = append(inner, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk the sandbox: %w", err)
	}

	// Deepest first, so every chown happens while the worker can still reach it.
	for i := len(inner) - 1; i >= 0; i-- {
		if err := os.Lchown(inner[i], int(uid), int(uid)); err != nil {
			return fmt.Errorf("hand %s to uid %d: %w", inner[i], uid, err)
		}
	}
	if err := os.Lchown(dir, int(uid), int(uid)); err != nil {
		return fmt.Errorf("hand the sandbox to uid %d: %w", uid, err)
	}
	return nil
}

// nextFreeUID picks the lowest id in the range no directory already claims.
//
// The filesystem is the register, so this survives a restart without persisting
// anything. Callers hold m.mu, which is enough: one worker owns one sandbox root.
func (m *Manager) nextFreeUID() (uint32, error) {
	taken := make(map[uint32]struct{})
	entries, err := os.ReadDir(filepath.Join(m.baseConfig.RootDir, "agents"))
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read the sandbox root: %w", err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if owner, ok := ownerOf(info); ok {
			taken[owner] = struct{}{}
		}
	}

	r := m.baseConfig.UIDRange
	for uid := r.First; uid < r.First+r.Count; uid++ {
		if _, used := taken[uid]; !used {
			return uid, nil
		}
	}
	// Better than reusing one: two agents sharing an id share a sandbox, which is
	// the isolation this exists to provide, silently absent.
	return 0, fmt.Errorf("every id in %d–%d is in use by an existing sandbox; widen --uid-range or reap old sandboxes",
		r.First, r.First+r.Count-1)
}

// adoptDir takes ownership of dir if it belongs to somebody else, then sets its
// mode.
//
// The ownership step is what makes the first start against an existing volume
// work. A directory carried over from before per-agent ids belongs to whatever
// user the old process ran as, and chmod on a directory you do not own needs
// CAP_FOWNER — which this worker deliberately does not have, so the migration
// would otherwise fail with "operation not permitted" on the sandbox root and
// take every agent's first tool call with it.
//
// CAP_CHOWN is enough: it permits giving away or taking a file regardless of who
// holds it now.
func adoptDir(dir string, mode os.FileMode) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}

	// Compared as ints rather than converting the stat's uid, so a negative
	// Geteuid — which cannot happen, but is what the conversion would have to
	// assume away — is simply a mismatch instead of a wrapped-around match.
	if owner, ok := ownerOf(info); ok && int64(owner) != int64(os.Geteuid()) {
		if err := os.Chown(dir, os.Geteuid(), os.Getegid()); err != nil {
			return fmt.Errorf("take ownership of %s: %w", dir, err)
		}
	}
	if info.Mode().Perm() == mode {
		return nil
	}
	return os.Chmod(dir, mode)
}
