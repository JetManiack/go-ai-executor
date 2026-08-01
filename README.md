# go-ai-executor

Three binaries: a server, the workers it dispatches to, and a standalone helper
for a developer's own machine.

**`executor`** — a multi-user MCP server over HTTP. Each agent gets a jailed
directory it can run programs and do file operations in, and a web UI lets a human
watch those terminals live and stop a sandbox that has gone wrong. Agents
authenticate to `/mcp` with a per-agent bearer token; humans authenticate to the UI
through Keycloak/OIDC, and only an administrator can stop a sandbox.

**`worker`** — where the commands actually run. The server holds no sandbox of its
own: workers dial in over a WebSocket and the server routes each agent's
operations to one of them. A worker has no database, no OIDC secrets and no listening socket,
which is what lets the pod running untrusted code be locked down far harder than
one that also serves a web UI. See [Server and workers](#server-and-workers).

**`executor-local`** — a single-user MCP server over stdin/stdout for a developer's
own machine. It runs shell command lines in the directory it was started in, and
that is all: no HTTP, no web UI, no database, no authentication, no sandbox. See
[The local helper](#the-local-helper).

## What it does

**For agents — six MCP tools, each scoped to the calling agent's own sandbox:**

| Tool | Purpose |
|---|---|
| `exec_command` | run a program with an argument vector, with a timeout and an output cap |
| `read_file` | read a file |
| `write_file` | write a file, creating parent directories |
| `list_dir` | list a directory, non-recursive |
| `delete_file` | delete a file or directory |
| `get_sandbox_status` | report the sandbox's root, limits and running commands |

**For humans — a read-only terminal and an emergency stop:**

- **Sandboxes** — every registered agent, blocked ones first, then busiest, with
  how many commands are running and how many people are watching.
- **Terminal** — the sandbox's output as it is produced, over a WebSocket, with
  command boundaries, exit codes, and an explicit marker where output was not
  retained. Read-only: nothing sent on the socket is executed.
- **Stop** — kills every process group in the sandbox and refuses its subsequent
  tool calls until an administrator releases it. Requires a reason, which the
  agent is told.
- **Agents** — register agents, issue and revoke tokens (admin only).

## Quick start

Two terminals, because there are two processes:

```sh
make run         # the server on :8080
make run-worker  # a worker, in a second terminal
```

That serves on `:8080` with `--auth-stub`, which authenticates every request as a
fixed administrator. It exists for local development only — see [Auth](#auth).
Both targets share a fixed development worker token, so they find each other in a
fresh checkout.

Without a worker the server comes up and every tool call reports *"no execution
worker is connected"*. That is the intended answer rather than a failure mode: the
server does not run commands.

Then register an agent and issue it a token:

```sh
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"display_name":"my-agent"}' http://localhost:8080/api/agents
curl -s -X POST http://localhost:8080/api/agents/<agent-id>/tokens
```

The token is returned once and never again — only its SHA-256 hash is stored.

Point an MCP client at `http://localhost:8080/mcp` with
`Authorization: Bearer <token>`, open <http://localhost:8080> and watch it work.

## Server and workers

```
server (executor)                worker (worker)
  /mcp    agents                   dials the server, WSS
  /api    humans, web UI           holds the sandboxes
  /worker workers dial in          runs commands, does file ops
  storage, blocks, OIDC            no database, no secrets
  ring buffer + browser WS         streams output back
```

Splitting does not confine a command; it relocates it into a pod a cluster can
harden. Everything else follows from keeping the worker bare — blocks are checked
server-side before dispatch, the retention buffer stays on the server, and the
worker never sees a token or an actor, only "run this in your sandbox".

The worker dials out, so it needs no Service, no ingress and no discovery. Each
one serves many agents, so the pool scales without anything above it knowing.

**Routing is sticky per agent.** A sandbox is a directory on one worker's disk, so
`write_file` and the `read_file` after it have to reach the same worker. The server
pins an agent to a connection on first use and keeps it there for the life of that
connection; new agents go to the least-loaded worker.

**A worker going away takes its sandboxes with it.** Scale-down, eviction, a pod
restart — the agents pinned there are unpinned and land elsewhere with an empty
sandbox, and watchers get a `killed` event saying why the terminal stopped. That
is the ephemerality an `emptyDir` implies, stated rather than hidden.

**The server going away does not.** A worker that redials names the agents it is
still holding, and the server pins them back where their files are; a restart or a
rollout costs an agent the command that was in flight, not its work. An agent
another live worker is already serving is left where it is — two workers holding
one agent would make which sandbox it reaches a matter of routing luck.

**The worker declares its limits in the hello, and the server sizes the
connection to them.** How much a message may carry is not a constant on either
side: it is computed from what the worker can actually produce — `2 ×
--max-output-bytes` for a result carrying stdout and stderr, `--max-file-bytes`
for a file — so the socket and the caps cannot disagree, and a pool of workers
configured differently is no longer a misconfiguration. With the defaults that
comes to a 16MB frame.

Everything else follows from that number being known on both ends: a file over
`--max-file-bytes` is refused by name before it is read or sent, a result that
escaping inflated past the frame is refused as a payload, and a worker declaring
limits above a hard ceiling is refused at the handshake rather than being allowed
to size the server's buffers. The checks happen before the write, because
exceeding a WebSocket read limit closes the connection — and many agents share
one, so an oversized file would otherwise orphan the sandboxes of everyone else on
that worker.

The link is a WebSocket the worker opens: `wss` when it crosses anything public,
plain `ws` to a ClusterIP behind a network policy in a cluster, both accepted by
`--server-url`. Workers authenticate with a pre-shared token in the
`Authorization` header, compared against the server's `--worker-token`. The hello
also carries a protocol number, and a mismatch is refused at the handshake —
server and workers are usually separate deployments that update independently, and
a worker speaking a different vocabulary would otherwise connect, look healthy,
and answer wrongly. One shared secret for the pool;
per-worker revocable tokens and mTLS are in the [ROADMAP](docs/ROADMAP.md). Without
a token configured the server refuses every worker connection rather than
accepting all of them, so a missing flag is a pool that cannot execute anything
rather than an open execution service.

## What is in a sandbox

The image carries the tools an agent is likely to reach for: **python3** with
`venv` and `pip`, **git**, **openssh-client**, **curl**, **jq**, **ripgrep**,
**less**, **unzip**, **xz**, **procps**. Deliberately absent is a compiler
toolchain — `build-essential` is most of a quarter-gigabyte, and with glibc wheels
the common Python cases do not need one. Add it to the Dockerfile if your agents'
tasks turn out to.

The runtime image is Debian rather than Alpine, and Python is the reason: PyPI's
binary wheels target manylinux, which means glibc. On musl, `pip install` falls
back to building from source for much of the ecosystem — numpy, pandas,
cryptography, anything with a C extension — which needs that toolchain and turns
seconds into minutes or into an error an agent cannot act on. A bigger image where
Python works beats a small one where it nominally exists.

**Each sandbox gets its own Python environment** at `--venv-dir` (`.venv` by
default), created on the agent's first command and owned by that agent. One per
sandbox rather than one shared: `pip install` is a thing agents do, and a shared
environment would make one agent's dependency the next agent's problem.

It is *activated* by the environment rather than by a script, because there is no
shell here to source `activate` in: every command runs with the environment's
`bin` first on `PATH` and `VIRTUAL_ENV` set, which is all activation ever was. So
`python` and `pip` mean the sandbox's own, always, rather than only when the agent
remembered to activate first.

If the image has no interpreter the sandbox still works — creation failure is
logged and commands fall back to whatever `PATH` offers. `--venv-dir ""` turns it
off. An agent that deletes its own `.venv` can rebuild it with `python3 -m venv
.venv`; the worker creates it once per process, not once per command.

## Sandboxing

Each agent gets `<sandbox-dir>/agents/<actor-id>` **on the worker holding it**.
Containment rests on four things, not on inspecting the command:

- **Path confinement.** File operations go through `os.Root`, so the kernel
  refuses any name that leaves the sandbox — including one that leaves through a
  symlink the agent created inside its own sandbox, which a purely lexical check
  on the requested path cannot catch. A command's working directory cannot use
  `os.Root` (exec takes a path), so its symlink chain is resolved and checked
  explicitly.
- **An allowlisted environment.** Commands inherit only the variables named by
  `--env-passthrough` (`PATH`, `LANG`, `LC_ALL`, `LC_CTYPE`, `TZ` by default),
  plus anything the operator adds with `--env`, plus `HOME` and `PWD` pointing
  inside the sandbox. The rest of the worker's environment is dropped, and a
  worker's environment is a short list to begin with: its own token and where to
  dial. The server's `DB_DSN`, `OIDC_CLIENT_SECRET` and `SESSION_ENCRYPTION_KEY`
  are in a different pod entirely, and naming any of them is refused at startup
  regardless.
- **Process groups.** Commands run in their own group and are torn down as a
  group, so a backgrounded child cannot outlive its timeout or the stop button.
- **Bounded output.** A per-call cap on what a tool returns, and a bounded ring
  buffer for what the terminal retains.

### `exec_command` takes a program and arguments, not a command line

```json
{"command": "go", "args": ["test", "./..."], "timeout_sec": 300}
```

The program is executed directly. No shell sits between the caller and the
process, so `&&`, `|`, `>` and globs are not interpreted — an argument containing
them is passed through as the literal text it is. A bare program name is resolved
against the `PATH` the command will actually run with; a name containing a `/`
(`./build.sh`) is resolved relative to the working directory, so an agent can run
a script it just wrote.

An agent that genuinely needs shell features asks for a shell by name, which is
explicit and visible in the terminal stream:

```json
{"command": "/bin/sh", "args": ["-c", "make build && make test"]}
```

### What `exec_command` is not

Removing the implicit shell makes the tool's contract unambiguous. It does **not**
make it a boundary, and it is worth being exact about why: the path confinement
above covers the file tools, but `exec_command` runs a program as the server's
user, so it reaches every file that user reaches — and if a shell is installed the
agent can invoke it deliberately, as above. `cd / && rm -rf .` remains expressible.

For `exec_command` the sandbox root is a working directory, not a boundary. The
boundary has to come from the deployment.

**Each agent's commands and file operations run as their own user id**, handed out
from `--uid-range`. That is what separates agents from each other and from the
worker: a command cannot read another agent's directory, cannot signal another
agent's processes, and cannot read the worker's own credentials out of
`/proc/1/environ` — the one-line read that used to yield everything the worker
holds. The privilege drop happens between fork and exec, so the process that runs
an agent's code never holds the id that could reach any of it.

File operations go the same way. Once a sandbox belongs to uid 20001 the worker
genuinely cannot read it, which is the point — so `read_file` and the rest fork
and drop too, rather than the worker keeping `CAP_DAC_OVERRIDE` and with it the
ability to read every agent's files.

**Without `--uid-range` none of that holds**, and the worker says so at startup.
Every agent then shares the worker's user, which is fine on a developer's machine
— dropping privileges needs `CAP_SETUID`, which a laptop has not got — and is not
fine anywhere with more than one tenant.

**What it costs is the strictest Pod Security label.** The worker needs
`SETUID`, `SETGID`, `CHOWN` and `KILL`, and Kubernetes only grants added
capabilities to a container running as uid 0, so the namespace enforces `baseline`
rather than `restricted`. That is container-root with no `DAC_OVERRIDE`, no
`SYS_ADMIN`, not privileged, seccomp on, read-only root filesystem, no service
account token — and with `hostUsers: false` it maps to an unprivileged id on the
host. Weigh it against the alternative, which is every agent sharing one user with
the worker's token.

**What it does not cover:** an agent's own commands are not isolated from each
other, and confinement *inside* an agent's sandbox is still the filesystem's
business. Landlock is the next layer there — see the [ROADMAP](docs/ROADMAP.md).

The rest of the boundary comes from the deployment:

- run the service in a container, as a non-root user, with only the sandbox
  volume writable — this is what the shipped image and the `USER executor` line
  are for;
- give that container no more network access than the agents actually need;
- treat the sandbox root as untrusted data, and never mount anything into it that
  the agents should not have.

`internal/sandbox/containment_test.go` asserts this boundary explicitly rather
than leaving it to prose.

## Auth

**Agents → `/mcp`.** A per-agent bearer token, stored hashed and revocable
individually. There is no shared secret.

**Humans → `/api`, `/`.** Keycloak/OIDC authorization-code flow with server-side
sessions: the session cookie holds a random ID whose hash is what the database
stores, and the refresh token is encrypted at rest with AES-256-GCM. Cached
claims are re-validated against Keycloak every 15 minutes, so removing someone
from the admin group takes effect without waiting out their session.

Roles come from the ID token's `groups` claim: members of `--admin-group` get
`admin`, everyone else `viewer`.

| | viewer | admin |
|---|---|---|
| list sandboxes, watch terminals | ✅ | ✅ |
| stop and release sandboxes | — | ✅ |
| register agents, issue/revoke tokens | — | ✅ |

`--auth-stub` replaces all of that with a fixed always-admin identity and **no
credential check whatsoever**. It defaults to off, and the server refuses to
start on an incomplete OIDC configuration rather than falling back to it. Never
set it outside a trusted development machine: this UI streams every sandbox's
terminal and can kill what is running in them.

## Configuration

Every flag has an environment variable equivalent. The two binaries share a
`--worker-token` and nothing else: the limits and the sandbox root belong to the
process that holds the sandboxes.

**`executor` (the server):**

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--listen-addr` | `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `--db-dsn` | `DB_DSN` | `data/executor.db` | SQLite path or `postgres://…` |
| `--worker-token` | `WORKER_TOKEN` | — | shared secret workers present; unset means no worker may connect |
| `--stream-buffer-bytes` | `STREAM_BUFFER_BYTES` | `262144` | terminal output retained per sandbox for replay |
| `--audit-retention` | `AUDIT_RETENTION` | `168h` | how long the action journal is kept; `0` keeps everything |
| `--auth-stub` | `AUTH_STUB` | `false` | fixed admin identity, development only |
| `--oidc-issuer` | `OIDC_ISSUER` | — | Keycloak realm issuer URL |
| `--oidc-client-id` | `OIDC_CLIENT_ID` | — | OIDC client ID |
| `--oidc-client-secret` | `OIDC_CLIENT_SECRET` | — | OIDC client secret |
| `--public-url` | `PUBLIC_URL` | — | externally-reachable base URL, for the redirect URI |
| `--admin-group` | `ADMIN_GROUP` | `admins` | group whose members get `admin` |
| `--session-encryption-key` | `SESSION_ENCRYPTION_KEY` | — | base64 32-byte key (`openssl rand -base64 32`) |
| `--devel` | `DEVEL` | `false` | serve static assets from disk, not the embedded snapshot |

**`worker`:**

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--server-url` | `SERVER_URL` | — | the server's base URL; `http(s)://` and `ws(s)://` both accepted |
| `--worker-token` | `WORKER_TOKEN` | — | shared secret presented to the server; must match its `--worker-token` |
| `--worker-token-file` | `WORKER_TOKEN_FILE` | — | read that secret from a file instead, keeping it out of `/proc/<pid>/environ` (preferred) |
| `--worker-id` | `WORKER_ID`, `HOSTNAME` | the hostname | name reported to the server, so in a pod it is the pod name |
| `--sandbox-dir` | `SANDBOX_DIR` | `/sandboxes` | root the per-agent sandboxes live under; mount an `emptyDir` here |
| `--default-timeout` | `DEFAULT_TIMEOUT` | `30s` | per-command timeout when the caller sets none |
| `--max-output-bytes` | `MAX_OUTPUT_BYTES` | `524288` | cap on the output a tool call returns |
| `--max-file-bytes` | `MAX_FILE_BYTES` | `8388608` | cap on one `read_file` or `write_file`; larger files are refused, not truncated |
| `--uid-range` | `UID_RANGE` | — | per-agent user ids, e.g. `20000-20999`; needs `SETUID`/`SETGID`/`CHOWN`/`KILL`, unset runs every agent as the worker's user |
| `--venv-dir` | `VENV_DIR` | `.venv` | Python environment created in each sandbox and put on every command's `PATH`; empty disables it |
| `--python` | `PYTHON` | `python3`, then `python` | interpreter used to create it |
| `--env-passthrough` | `ENV_PASSTHROUGH` | `PATH,LANG,LC_ALL,LC_CTYPE,TZ` | variable names commands inherit from the worker |
| `--env` | `SANDBOX_ENV` | — | extra `KEY=VALUE` entries for every command (repeatable) |

## HTTP surfaces

| Path | Protocol | Auth |
|---|---|---|
| `/mcp` | MCP over Streamable HTTP | per-agent bearer token |
| `/worker` | WebSocket, JSON frames | shared worker token |
| `/api/*` | REST + JSON | OIDC session |
| `/api/sandboxes/{id}/stream` | WebSocket | OIDC session, read-only |
| `/auth/login`, `/auth/callback`, `/auth/logout` | OIDC | — |
| `/livez`, `/readyz` | plain HTTP | none |
| `/` | web UI | OIDC session |

```
GET    /api/sandboxes                          viewer
GET    /api/sandboxes/{id}                     viewer
GET    /api/sandboxes/{id}/stream              viewer  (WebSocket)
POST   /api/sandboxes/{id}/block               admin
DELETE /api/sandboxes/{id}/block               admin
GET    /api/agents                             admin
POST   /api/agents                             admin
DELETE /api/agents/{id}                        admin   (revokes its credentials)
GET    /api/agents/{id}/tokens                 admin
POST   /api/agents/{id}/tokens                 admin   (token returned once)
DELETE /api/agents/{id}/tokens/{tokenID}       admin
GET    /api/me
```

`/livez` is unconditional once the process serves HTTP; `/readyz` reports the
database. If the database is unreachable at startup the server still listens,
serving `/readyz` as not-ready while retrying with capped backoff, then swaps the
real routes in on the same listener — a crash loop cannot fix a database outage.

## The action journal

Every tool call writes two rows to the database: one when it is attempted, one
when it returns, sharing a call id.

Two rather than one because the cases worth having a journal for are the ones
that never return — a command that hung until the pod was killed leaves a started
row with no finished row, where a single row written on completion would leave no
trace and read as an action that never happened.

A row records who, when, which tool, what it was aimed at, which worker served
it, how long it took, how many bytes it moved, and how it ended: `ok`, `error`,
or `blocked` — an administrator's decision filed apart from a failure, because
"was this agent stopped" and "did its calls fail" are different questions.

It records that a file was written and how big it was, never the contents. That
is the terminal's job, and putting output here would turn the database into a log
store with none of the retention a log store has. Rows are pruned hourly past
`--audit-retention`, a week by default; `0` keeps everything, which is a decision
rather than a default.

There is no API for reading it yet — see the [ROADMAP](docs/ROADMAP.md).

## The terminal stream

The stream is a sequence of JSON events per sandbox, each with a monotonic `seq`:

| `kind` | Meaning |
|---|---|
| `started` | a command began; carries `command` and `work_dir` |
| `stdout`, `stderr` | one chunk of output, in `data` |
| `finished` | the command exited; carries `exit_code`, `duration_ms` |
| `killed` | its process group was torn down by `by_actor` |
| `blocked`, `released` | an administrator changed the sandbox's state |
| `gap` | `missed_events` events were evicted and cannot be shown |

Reconnect with `?after=<seq>` to resume. Output is retained per sandbox up to
`--stream-buffer-bytes`; anything older is gone, and asking for it produces a
`gap` rather than a silent jump. A watcher that stops reading is disconnected
rather than having its events dropped, so a reconnect reports the loss honestly.

## The local helper

`executor-local` exists because the server answers a different question. The server
is multi-tenant: several agents share one worker, so each needs a jailed directory,
an allowlisted environment and an argument vector no shell reinterprets.
On your own machine there is one user, and the agent acts with exactly your
authority — the same authority it has if you paste the command into your terminal.
Confinement there would buy nothing it could not trivially step around, so it is
not pretended at.

```sh
make build-local
```

Point an MCP client at the binary; it speaks MCP on stdin/stdout and writes nothing
else to either stream.

```json
{
  "mcpServers": {
    "executor-local": {
      "command": "/path/to/bin/executor-local",
      "args": ["--dir", "/path/to/your/repo"]
    }
  }
}
```

The same six tool names the server uses, so a client config or a prompt written
for one works against the other. The two are never connected at the same time —
you point a client at one or the other — and a test compares the local
registration against the server's so a rename on either side fails the build
rather than surfacing as a tool an agent cannot find.

| Tool | Local behaviour |
|---|---|
| `exec_command` | with `args`: the program is executed directly, as on the server. Without `args`: `command` is a shell command line, so pipes, redirection, globs and `&&` work |
| `read_file` | read a file; long files are cut at the output cap and say so via `truncated` |
| `write_file` | write a file, creating parent directories |
| `list_dir` | list a directory, non-recursive |
| `delete_file` | delete a file, or a directory and its subtree |
| `get_sandbox_status` | report the directory, shell and limits — and `"sandboxed": false` |

`exec_command`'s input is a strict superset of the server's: send `args` and it
behaves identically, omit them and you get a shell. That is the one place the
contracts differ, and it differs by addition rather than by variation.

Paths are relative to the helper's directory, or absolute, and are **not** confined
to it — `read_file` will read `/etc/passwd` if asked. Two delete targets are
refused, the filesystem root and the working directory itself, not as confinement
but because both are almost certainly a mistake and the cost of being wrong is
total.

`get_sandbox_status` keeps the server's name even though there is no sandbox here,
which is exactly why its output states `"sandboxed": false`: an agent reading the
tool name alone would assume otherwise.

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--dir` | `EXECUTOR_LOCAL_DIR` | the directory it was started in | working directory for commands |
| `--shell` | `EXECUTOR_LOCAL_SHELL` | `$SHELL`, then `/bin/sh` | shell that runs the command lines |
| `--timeout` | `EXECUTOR_LOCAL_TIMEOUT` | `2m` | default per-command timeout |
| `--max-output-bytes` | `EXECUTOR_LOCAL_MAX_OUTPUT_BYTES` | `1048576` | per-stream output cap |

What it keeps from the sandboxed path is the mechanics that are about correctness
rather than containment: a timeout tears down the command's whole process tree, so
a `make` that spawned workers does not leave them behind; output is bounded; and a
multi-byte character is never cut in half.

What it deliberately does not do: confine paths, filter the environment (a command
sees yours, because that is what you would have given it), log anything, or serve
anything over the network.

The server binary has no stdio mode. It used to, attributing calls to a synthesized
`stdio-local` agent; two stdio paths with different semantics — one sandboxed and
audited, one not — is worse than one of each done properly.

## Deployment

```sh
docker build -t go-ai-executor .
docker compose --profile app up --build   # the server and one worker, locally
docker compose up -d                      # Postgres alone, for the storage suite
```

Both commands ship in one image; the first argument picks which (`./executor`,
`./worker`). The image is multi-stage (`golang:1.26-alpine` → `alpine:3.22`), runs
as uid 65532, and fails the build if the frontend assets are missing rather than
shipping a binary that 404s on every asset. Published to
`ghcr.io/jetmaniack/go-ai-executor` on pushes to `main` and on tags, after an
image smoke test that boots the container and requests the paths a browser
actually requests.

**Kubernetes:** no manifests here — deployment belongs to whoever operates the
cluster, and a copy in this repository would drift from the one actually applied.
What the deployment has to know is in this README rather than in YAML:

- The worker is what a cluster should lock down, and it is built to allow it: no
  Service, no ingress, no port, no database, no service account token. It runs
  happily read-only with an `emptyDir` for `--sandbox-dir` and a writable `/tmp`,
  as uid 65532, with every capability dropped.
- **Egress from a worker should reach the internet and not the cluster.** Agents
  need to install packages; they have no business reaching the database or the
  node's metadata endpoint.
- **The server must be one replica.** Workers hold a connection to one, and the
  terminal's retention is per process — see [Server and workers](#server-and-workers).
- **Scaling the pool down destroys the sandboxes on the workers removed**, and
  CPU is a poor signal for choosing which to remove: a worker holding a sandbox
  for an agent that is thinking between commands is idle, so it is exactly what
  a CPU-driven autoscaler takes first. Either give the server a drain to
  participate in, or put the sandboxes on per-worker volumes, or do not autoscale.
  An `emptyDir` also means an operator cannot inspect what an agent did after the
  pod is gone — a per-worker PVC keeps that and costs the ephemerality.
- **Agents are isolated by user id, and that needs four capabilities** —
  `SETUID`, `SETGID`, `CHOWN`, `KILL`, which Kubernetes only grants to a container
  running as uid 0, so the namespace is `baseline` rather than `restricted`.
  Without `--uid-range` the worker still runs, with every agent sharing its user.
  See [What `exec_command` is not](#what-exec_command-is-not).

## Development

```sh
make            # list targets
make build      # all three binaries into bin/
make run        # the server, stub auth, on :8080
make run-worker # a worker against it, in a second terminal
make generate   # bundle the frontend, vendor and checksum React + fonts
make test       # go test ./...
make test-race  # with the race detector
make lint       # golangci-lint
make security   # gosec, govulncheck, staticcheck
```

`make generate` downloads React, react-dom and the three fonts and verifies them
against SHA-256 digests pinned in the Makefile, then serves them from this
origin. A mismatch fails the build instead of baking a substituted response into
the image.

Storage tests run against SQLite by default. Point `TEST_POSTGRES_DSN` at a
Postgres instance and every one of them runs against Postgres as well, each in
its own schema:

```sh
docker compose up -d
TEST_POSTGRES_DSN=postgres://executor:executor@localhost:5432/executor_test \
  go test ./internal/storage/... -v
```

## Layout

```
cmd/executor/            server CLI: flag/env parsing, HTTP wiring
cmd/worker/              worker CLI: dials the server, holds the sandboxes
cmd/executor-local/      local stdio helper CLI
internal/mcpserver/      the server's MCP surface: one tool_*.go per tool, plus auth
internal/workerproto/    the wire vocabulary both ends share: frames, ops, payloads
internal/workerhub/      server side of the link: accept, route, correlate
internal/workerlink/     worker side of the link: dial, serve, forward
internal/workertest/     a real hub with real workers, for tests above the link
internal/sandbox/        the jail: exec, os.Root confinement, event sink
internal/stream/         events, ring buffer, fan-out to watchers (server side)
internal/localexec/      the local helper's runner: $SHELL in $CWD, unconfined
internal/localmcp/       the local helper's MCP surface: run_shell, get_status
internal/sandboxop/      one file operation, in a child running as the agent's user
internal/procexec/       shared mechanics: process-group teardown, privilege drop
internal/storage/        GORM models, SQLite/Postgres backends, repositories
internal/restapi/        REST API and the terminal WebSocket, mounted at /api
internal/humanauth/      OIDC provider, sessions, crypto, stub auth
internal/health/         /livez, /readyz
internal/frontend/       go:embed of the built SPA
web/src/                 React SPA (hash routing, esbuild)
```

`internal/stream` is separate from `internal/sandbox` because the two ends are now
in different processes: the worker produces events and the server retains and fans
them out, so the server needs the vocabulary without needing a sandbox at all.

`internal/procexec` exists so the two subtle parts have one implementation each: a
group kill that converges on processes forked while the signal was being delivered,
and output cut on a character boundary. Both binaries need them, and duplicating
either would mean fixing it twice.

`internal/workertest` exists because execution now crosses a process boundary. The
tests above the link — the MCP tools, the REST API — run against a real hub with
real workers attached, since a fake executor would pass while the wire contract was
broken.

Architecture and the reasoning behind it:
[`docs/superpowers/specs/2026-07-30-executor-parity-design.md`](docs/superpowers/specs/2026-07-30-executor-parity-design.md)
and
[`docs/superpowers/specs/2026-07-30-worker-split-design.md`](docs/superpowers/specs/2026-07-30-worker-split-design.md).
Planned work: [`docs/ROADMAP.md`](docs/ROADMAP.md).
