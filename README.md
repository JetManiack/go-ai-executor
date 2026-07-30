# go-ai-executor

Two binaries for two different situations.

**`executor`** — a multi-user MCP server over HTTP. Each agent gets a jailed
directory it can run programs and do file operations in, and a web UI lets a human
watch those terminals live and stop a sandbox that has gone wrong. Agents
authenticate to `/mcp` with a per-agent bearer token; humans authenticate to the UI
through Keycloak/OIDC, and only an administrator can stop a sandbox.

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

```sh
make run
```

That serves on `:8080` with `--auth-stub`, which authenticates every request as a
fixed administrator. It exists for local development only — see [Auth](#auth).

Then register an agent and issue it a token:

```sh
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"display_name":"my-agent"}' http://localhost:8080/api/agents
curl -s -X POST http://localhost:8080/api/agents/<agent-id>/tokens
```

The token is returned once and never again — only its SHA-256 hash is stored.

Point an MCP client at `http://localhost:8080/mcp` with
`Authorization: Bearer <token>`, open <http://localhost:8080> and watch it work.

## Sandboxing

Each agent gets `<sandbox-dir>/agents/<actor-id>`. Containment rests on four
things, not on inspecting the command:

- **Path confinement.** File operations go through `os.Root`, so the kernel
  refuses any name that leaves the sandbox — including one that leaves through a
  symlink the agent created inside its own sandbox, which a purely lexical check
  on the requested path cannot catch. A command's working directory cannot use
  `os.Root` (exec takes a path), so its symlink chain is resolved and checked
  explicitly.
- **An allowlisted environment.** Commands inherit only the variables named by
  `--env-passthrough` (`PATH`, `LANG`, `LC_ALL`, `LC_CTYPE`, `TZ` by default),
  plus anything the operator adds with `--env`, plus `HOME` and `PWD` pointing
  inside the sandbox. The rest of the server's environment is dropped, because
  that is where this service's `DB_DSN`, `OIDC_CLIENT_SECRET` and
  `SESSION_ENCRYPTION_KEY` live — those three cannot be passed through at all,
  and the server refuses to start if they are named.
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
boundary has to come from the deployment:

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

Every flag has an environment variable equivalent.

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--listen-addr` | `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `--db-dsn` | `DB_DSN` | `data/executor.db` | SQLite path or `postgres://…` |
| `--sandbox-dir` | `SANDBOX_DIR` | `./scratch` | root the per-agent sandboxes live under |
| `--default-timeout` | `DEFAULT_TIMEOUT` | `30s` | per-command timeout when the caller sets none |
| `--max-output-bytes` | `MAX_OUTPUT_BYTES` | `524288` | cap on the output a tool call returns |
| `--env-passthrough` | `ENV_PASSTHROUGH` | `PATH,LANG,LC_ALL,LC_CTYPE,TZ` | variable names commands inherit from the server |
| `--env` | `SANDBOX_ENV` | — | extra `KEY=VALUE` entries for every command (repeatable) |
| `--stream-buffer-bytes` | `STREAM_BUFFER_BYTES` | `262144` | terminal output retained per sandbox for replay |
| `--auth-stub` | `AUTH_STUB` | `false` | fixed admin identity, development only |
| `--oidc-issuer` | `OIDC_ISSUER` | — | Keycloak realm issuer URL |
| `--oidc-client-id` | `OIDC_CLIENT_ID` | — | OIDC client ID |
| `--oidc-client-secret` | `OIDC_CLIENT_SECRET` | — | OIDC client secret |
| `--public-url` | `PUBLIC_URL` | — | externally-reachable base URL, for the redirect URI |
| `--admin-group` | `ADMIN_GROUP` | `admins` | group whose members get `admin` |
| `--session-encryption-key` | `SESSION_ENCRYPTION_KEY` | — | base64 32-byte key (`openssl rand -base64 32`) |
| `--devel` | `DEVEL` | `false` | serve static assets from disk, not the embedded snapshot |

## HTTP surfaces

| Path | Protocol | Auth |
|---|---|---|
| `/mcp` | MCP over Streamable HTTP | per-agent bearer token |
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
is multi-tenant: several agents share one process, so each needs a jailed
directory, an allowlisted environment and an argument vector no shell reinterprets.
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

Two tools:

| Tool | Purpose |
|---|---|
| `run_shell` | run a shell command line — pipes, redirection, globs and `&&` all work |
| `get_status` | report the directory, shell, timeout and output cap in use, and that nothing is sandboxed |

`run_shell` is named for what it is rather than mirroring the server's
`exec_command`: that tool takes a program and an argument vector, this one takes a
command line. One name with two schemas would mislead an agent that talks to both.

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
anything over the network. `get_status` reports `"sandboxed": false` so an agent
cannot mistake it for the server.

The server binary has no stdio mode. It used to, attributing calls to a synthesized
`stdio-local` agent; two stdio paths with different semantics — one sandboxed and
audited, one not — is worse than one of each done properly.

## Deployment

```sh
docker compose up -d          # Postgres, for running the storage suite against it
docker build -t go-ai-executor .
```

The image is multi-stage (`golang:1.26-alpine` → `alpine:3.22`), runs as a
non-root user, and fails the build if the frontend assets are missing rather than
shipping a binary that 404s on every asset. Published to
`ghcr.io/jetmaniack/go-ai-executor` on pushes to `main` and on tags, after an
image smoke test that boots the container and requests the paths a browser
actually requests.

## Development

```sh
make            # list targets
make build      # both binaries into bin/
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
cmd/executor-local/      local stdio helper CLI
internal/mcpserver/      the server's MCP surface: one tool_*.go per tool, plus auth
internal/sandbox/        the jail: exec, streaming, ring buffer, os.Root confinement
internal/localexec/      the local helper's runner: $SHELL in $CWD, unconfined
internal/localmcp/       the local helper's MCP surface: run_shell, get_status
internal/procexec/       shared mechanics: process-group teardown, bounded output
internal/storage/        GORM models, SQLite/Postgres backends, repositories
internal/restapi/        REST API and the terminal WebSocket, mounted at /api
internal/humanauth/      OIDC provider, sessions, crypto, stub auth
internal/health/         /livez, /readyz
internal/frontend/       go:embed of the built SPA
web/src/                 React SPA (hash routing, esbuild)
```

`internal/procexec` exists so the two subtle parts have one implementation each: a
group kill that converges on processes forked while the signal was being delivered,
and output cut on a character boundary. Both binaries need them, and duplicating
either would mean fixing it twice.

Architecture and the reasoning behind it:
[`docs/superpowers/specs/2026-07-30-executor-parity-design.md`](docs/superpowers/specs/2026-07-30-executor-parity-design.md).
Planned work: [`docs/ROADMAP.md`](docs/ROADMAP.md).
