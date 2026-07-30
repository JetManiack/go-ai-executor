# Roadmap

Ordered roughly by how much each one is missed in practice. Nothing here is
committed to a date.

## Audit trail

The web UI observes and stops; it does not journal. Once a sandbox's output falls
out of the ring buffer, or the process restarts, there is no record of what an
agent did — including which files it deleted.

That was a deliberate choice: a live terminal is an operations tool, and adding a
history table to it doubles the write path for every tool call. But "who deleted
this" is a question that will eventually be asked, and answering it needs a
persisted `ToolCall`-style table with its own retention policy, as in
`go-ai-webtools`. Worth doing properly rather than by accident, which is why the
tables are not there now.

## Terminal fidelity

- **ANSI colour.** Escape sequences are currently stripped. Rendering them needs
  a terminal emulator (xterm.js), which is another vendored, checksum-pinned
  dependency; the tradeoff is worth revisiting once someone actually misses the
  colour.
- **A PTY.** Commands run with pipes, not a terminal, so anything that checks
  `isatty` takes its non-interactive path and progress bars arrive as one line
  per update. A PTY would fix both and would also make interactive commands
  possible — which is a change in what this service is for, not just how it
  renders.
- **Search and download.** Grepping a long run, and saving it, both currently
  mean selecting text in the browser.

## Resource limits

The sandbox bounds paths, environment, output size and wall-clock time. It does
not bound CPU, memory, disk or the number of processes: an agent can fill the
sandbox directory or fork until the host suffers, and only the stop button
answers that. Per-sandbox cgroup limits (Linux) or a per-agent disk quota would
turn "an operator noticed" into "the kernel refused".

Network egress is likewise unrestricted — a command reaches whatever the server
can reach.

## Sandbox lifecycle

- **Reaping.** Sandbox directories are never cleaned up; they accumulate for
  every agent that has ever run. A retention policy, or an explicit "wipe
  sandbox" action for administrators, would bound that. Wiping deliberately did
  not become part of the stop button: it destroys the evidence the operator
  pressed stop over.
- **Pre-warming.** Sandboxes are created lazily on an agent's first tool call, so
  the UI shows "no sandbox yet this run" for agents that have not connected since
  the process started. Harmless, but it reads as missing data.

## Multi-replica behaviour

Blocks are read from the database per tool call, so they work across replicas.
The terminal stream does not: a watcher connected to replica A sees only the
commands that ran on replica A, and the ring buffer is per process. Making the
stream cluster-wide needs a shared bus (Redis, NATS) or sticky routing by agent
ID. Single-replica deployments are unaffected.

## The local helper

- **Streaming output.** A long build reports nothing until it finishes. The server
  streams over a WebSocket; here the natural equivalent is MCP progress
  notifications, which the client would have to render.
- **A read cap that is not the output cap.** `read_file` is bounded by
  `--max-output-bytes`, which is really about command output. A source file a
  little over the cap comes back truncated for no good reason; a separate, larger
  `--max-read-bytes` would fit both uses better.

## The server's `read_file` is unbounded

The server's `read_file` returns a file whole, however large — an agent asking for
a multi-gigabyte file balloons the process and the MCP response. The local helper
caps reads and reports `truncated`; the server should do the same, which means a
new flag and a new output field on a tool clients already use.

## Operational polish

- **Metrics.** No Prometheus endpoint. Commands per agent, durations, exit codes,
  active blocks and watcher counts are all already computed and would cost little
  to export.
- **Structured request logging.** The server logs startup, shutdown and errors;
  it does not log requests.
- **Rate limiting.** An agent can call `exec_command` as fast as the host allows.
- **Multiple admin groups / finer roles.** Today it is one `--admin-group` and two
  roles. A "can stop but not manage tokens" role is the obvious next split.
