# Roadmap

Ordered roughly by how much each one is missed in practice. Nothing here is
committed to a date.

## Reading the audit trail

Every tool call now writes two journal rows — one when it is attempted, one when
it returns — kept for `--audit-retention` (a week by default) and pruned hourly.
What is missing is anywhere to read them: there is no API and no UI, so answering
"who deleted this" means a SQL client.

An admin-only endpoint over `storage.ListAudit`, and a view beside the terminal,
are the obvious next step. Two things to decide when building it: whether human
actions (blocking a sandbox, issuing a token) join the same table — they are
recorded nowhere today — and whether a row should link to the terminal output it
produced, which needs the exec id the tool currently keeps to itself.

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
- **Coalescing tiny writes.** One event is published per read, and a read returns
  whatever is in the pipe — so a command that flushes per line produces an event
  per line. Measured: 48KB of shell output arrived as two thousand events of about
  twenty bytes. A watcher may only fall behind by 256 events before it is
  disconnected as a slow consumer, so a chatty command can disconnect a browser
  that is doing nothing wrong; it reconnects and reports a gap, which is honest but
  avoidable. Batching reads that arrive within a few milliseconds, or under some
  size, would cut the event count by orders of magnitude without changing what a
  terminal shows.

## The worker link

- **Per-worker revocable tokens.** One shared `--worker-token` authenticates the
  whole pool, so removing a compromised worker's access means rotating the secret
  and restarting every other worker with it. Tokens issued per worker, revocable
  individually, are the obvious next step; mTLS is the one after that.
- **Draining a worker before it goes.** SIGTERM cancels commands in flight and the
  agents pinned there get an empty sandbox elsewhere. A worker that stopped
  accepting new agents, finished what it was running, and only then exited would
  turn a routine scale-down from something an agent notices into something it
  doesn't. The HPA's fifteen-minute scale-down window is the blunt version.
- **Results in flight do not survive a reconnect.** A worker that redials
  reclaims the agents it holds, so a server restart no longer costs an agent its
  files — but a command that was running when the connection dropped keeps
  running with nowhere to report, and its caller has already been told the worker
  disconnected. Re-delivering those results means request ids that outlive a
  connection, which is a larger change than the reclaim was.

## Confinement inside an agent's own sandbox

Per-agent user ids separate agents from each other and from the worker's
credentials. What they do not do is confine an agent within its own sandbox: a
command still reaches anything its uid can, which is its own directory plus
whatever is world-readable in the image.

Landlock is the answer and it is now available unprivileged — kernel 6.8 with ABI
4, permitted by containerd 2.2's default seccomp profile, so no node-local profile
and no Pod Security relaxation. The shape is a ruleset applied in the child between
fork and exec, scoping it to the agent's directory; it is inherited across execve
and cannot be dropped. `internal/sandboxop` is where it goes, since every
sandbox-touching operation already passes through there.

Not available on the same node: `CLONE_NEWUSER` is still blocked by the seccomp
profile, so bubblewrap-style nesting is out.

## Resource limits

The sandbox bounds paths, environment, output size and wall-clock time. It does
not bound CPU, memory, disk or the number of processes: an agent can fill the
sandbox directory or fork until the host suffers, and only the stop button
answers that. Per-sandbox cgroup limits (Linux) or a per-agent disk quota would
turn "an operator noticed" into "the kernel refused".

A worker deployment does most of this at the pod boundary instead — memory limits,
a bounded `emptyDir`, and egress that reaches the internet but not the cluster —
which bounds the blast radius of a whole worker rather than of one agent. Several
agents share a worker, so a per-sandbox limit is still the thing that stops one of
them from starving the others.

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
The terminal stream does not, and the worker split sharpened the problem rather
than solving it: a worker holds its connection to one replica, so that replica is
the only one that sees its events and the only one that can dispatch to it. A
watcher on replica B watches nothing, and an agent whose MCP call lands on B is
told no worker is connected — even though one is, to A.

Making the server horizontally scalable therefore needs both halves: a shared bus
(Redis, NATS) for the events, and cross-replica dispatch — either a worker
registry every replica can route through, or sticky routing by agent ID at the
ingress. Until then the server runs as a single replica deliberately, and the
worker pool is what scales.

## The local helper

- **Streaming output.** A long build reports nothing until it finishes. The server
  streams over a WebSocket; here the natural equivalent is MCP progress
  notifications, which the client would have to render.
- **A read cap that is not the output cap.** `read_file` is bounded by
  `--max-output-bytes`, which is really about command output. A source file a
  little over the cap comes back truncated for no good reason; a separate, larger
  `--max-read-bytes` would fit both uses better.

## `read_file` refuses large files rather than truncating them

`--max-file-bytes` bounds one transfer, and a file over it is refused by name —
checked by stat before the read, so the worker does not load what it cannot
return. That is a real limit rather than the wire's accident, but it is still the
blunter of the two behaviours: the local helper cuts a long read and reports
`truncated`, which is what an agent skimming a large log actually wants.

Giving the server the same needs a `truncated` field on a tool clients already
use. Chunking transfers over the link would remove the ceiling rather than move
it, and is the more complete answer — at which point `--max-file-bytes` stops
sizing the socket and becomes a policy limit on its own.

## Operational polish

- **Metrics.** No Prometheus endpoint. Commands per agent, durations, exit codes,
  active blocks and watcher counts are all already computed and would cost little
  to export.
- **Structured request logging.** The server logs startup, shutdown and errors;
  it does not log requests.
- **Rate limiting.** An agent can call `exec_command` as fast as the host allows.
- **Multiple admin groups / finer roles.** Today it is one `--admin-group` and two
  roles. A "can stop but not manage tokens" role is the obvious next split.
