# AI Executor MCP Server (`go-ai-executor`)

An MCP (Model Context Protocol) server written in Go for executing shell commands and performing file operations inside a secure, jailed sandbox directory.

Designed using the architecture patterns from [go-ai-rendezvous-point](https://github.com/JetManiack/go-ai-rendezvous-point).

## Features

- **MCP Tools exposed**:
  - `exec_command`: Execute shell commands inside the sandbox root directory with configurable timeouts and output size caps.
  - `read_file`: Read contents of a file inside the sandbox.
  - `write_file`: Write/create a file inside the sandbox.
  - `list_dir`: List files and subdirectories inside the sandbox.
  - `delete_file`: Safely delete a file or directory inside the sandbox.
  - `get_sandbox_status`: Retrieve sandbox root directory path, shell settings, and limits.
- **Security & Sandboxing**:
  - Path sanitization (`filepath.Rel` validation) prevents directory traversal attacks (`../`).
  - Context timeout handling (default 30s) with clean signal termination.
  - Environment variable isolation (`HOME`, `PWD`, `PATH`).
  - Output buffer caps (default 512KB) to avoid memory exhaustion.
- **Transports**:
  - `stdio`: For direct integration with MCP clients (Claude Desktop, Cursor, Antigravity CLI).
  - `http`: Streamable HTTP server on `/` with optional Bearer Token authentication.

## Quick Start

### Build

```sh
make build
```

### Run (stdio mode)

```sh
./bin/go-ai-executor --transport=stdio --sandbox-dir=./scratch
```

### Run (HTTP mode with Auth)

```sh
./bin/go-ai-executor --transport=http --listen-addr=:8081 --sandbox-dir=./scratch --auth-token=mysecret
```

### Run Unit Tests

```sh
make test
```

## MCP Client Configuration Example

Add to your MCP configuration file (`mcp_config.json`):

```json
{
  "mcpServers": {
    "go-ai-executor": {
      "command": "/Users/igor.lazarev/Documents/Projects/go/src/github.com/JetManiack/go-ai-executor/bin/go-ai-executor",
      "args": [
        "--transport=stdio",
        "--sandbox-dir=/tmp/ai-sandbox"
      ]
    }
  }
}
```
