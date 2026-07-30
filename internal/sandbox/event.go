package sandbox

import "time"

// EventKind discriminates what happened in a sandbox. Terminal output alone
// isn't enough for a readable terminal: without explicit command boundaries the
// output of two consecutive commands renders as one undifferentiated wall of
// text.
type EventKind string

const (
	// EventStarted is emitted once per command, before any output.
	EventStarted EventKind = "started"

	// EventStdout and EventStderr carry one chunk of output each, in the order
	// it was read.
	EventStdout EventKind = "stdout"
	EventStderr EventKind = "stderr"

	// EventFinished is emitted once per command, carrying its exit status.
	EventFinished EventKind = "finished"

	// EventKilled is emitted when a command's process group was torn down by an
	// administrator rather than exiting on its own.
	EventKilled EventKind = "killed"

	// EventBlocked and EventReleased mark administrative state changes, so a
	// watcher sees why output stopped instead of just seeing it stop.
	EventBlocked  EventKind = "blocked"
	EventReleased EventKind = "released"

	// EventGap is synthesized for a reconnecting watcher whose requested
	// position has already been evicted from the ring buffer. It is never
	// published by a sandbox — it is how the stream admits it cannot show a
	// continuous picture, rather than silently drawing one.
	EventGap EventKind = "gap"
)

// Event is one thing that happened in one sandbox.
//
// Fields are populated per Kind rather than split across separate types: the
// stream is serialized as JSON to a browser, and one shape with documented
// optional fields is easier to consume there than a tagged union.
type Event struct {
	// Seq is monotonic per sandbox, starting at 1. A watcher reconnects with
	// the last Seq it saw.
	Seq       uint64    `json:"seq"`
	SandboxID string    `json:"sandbox_id"`
	Kind      EventKind `json:"kind"`
	At        time.Time `json:"at"`

	// ExecID ties started/stdout/stderr/finished/killed events of one command
	// together. Empty for blocked/released/gap.
	ExecID string `json:"exec_id,omitempty"`

	// Data carries the output chunk for stdout/stderr.
	Data string `json:"data,omitempty"`

	// Command and WorkDir are set on started.
	Command string `json:"command,omitempty"`
	WorkDir string `json:"work_dir,omitempty"`

	// ExitCode, DurationMs and Truncated are set on finished.
	ExitCode   int   `json:"exit_code,omitempty"`
	DurationMs int64 `json:"duration_ms,omitempty"`
	Truncated  bool  `json:"truncated,omitempty"`

	// ByActor and Reason are set on blocked, released and killed.
	ByActor string `json:"by_actor,omitempty"`
	Reason  string `json:"reason,omitempty"`

	// MissedEvents is set on gap: how many events were evicted before the
	// watcher reconnected.
	MissedEvents uint64 `json:"missed_events,omitempty"`
}

// size approximates the event's memory cost for ring-buffer accounting. Only
// Data varies by orders of magnitude; the fixed fields are covered by a flat
// overhead rather than measured exactly, since the point is to bound memory,
// not to report it.
func (e Event) size() int {
	const fixedOverhead = 256
	return len(e.Data) + len(e.Command) + len(e.WorkDir) + len(e.Reason) + fixedOverhead
}
