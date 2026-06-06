# Code Intelligence & LSP

Reasonix integrates two code intelligence systems: **CodeGraph** (tree-sitter-based symbol/call-graph search) and **LSP** (Language Server Protocol client for diagnostics). Together they give the agent deep understanding of codebases without embedding services or API costs.

## CodeGraph

CodeGraph (`internal/codegraph/`) is a built-in MCP server that provides symbol search, call-graph navigation, and code exploration via tree-sitter and SQLite. It replaces the embedding-based semantic search from v0.x — no embedding service, no API cost.

### Architecture

```
┌────────────────────────────────────────────────┐
│              CodeGraph Integration              │
│                                                │
│  ┌──────────────┐    ┌──────────────────────┐  │
│  │  CodeGraph   │    │  MCP Server          │  │
│  │  Runtime     │◄───┤  (built-in plugin)   │  │
│  │  (external)  │    │                      │  │
│  └──────────────┘    └──────────┬───────────┘  │
│                                 │              │
│  ┌──────────────────────────────▼───────────┐  │
│  │           Tool Registry                   │  │
│  │  codegraph_search  codegraph_context     │  │
│  │  codegraph_explore codegraph_trace       │  │
│  │  codegraph_node                          │  │
│  └──────────────────────────────────────────┘  │
└────────────────────────────────────────────────┘
```

### Installation

CodeGraph is a separate binary fetched on first use (or via `reasonix codegraph install`):

- `install.go` — downloads the matching version (`CODEGRAPH_VERSION` in Makefile) from GitHub releases.
- The runtime is cached per-version under a local directory.
- Auto-install is controlled by `[codegraph].auto_install`.

### Tier System

| Tier | Behavior |
|------|----------|
| `lazy` | Index on first use; no background work |
| `background` | Start indexing in the background after boot |
| `eager` | Block boot until indexing is complete |

### Configuration

```toml
[codegraph]
enabled      = false       # off by default for first-run sessions
auto_install = true        # fetch runtime when missing
path         = ""          # empty = cache, then PATH, then bundle beside reasonix
tier         = "lazy"      # lazy|background|eager
```

### Tools

| Tool | Description |
|------|-------------|
| `codegraph_search` | Search for symbols by name pattern |
| `codegraph_context` | Get context around a symbol (definition, references) |
| `codegraph_explore` | Explore the structure of a file or directory |
| `codegraph_trace` | Trace a call graph from a function |
| `codegraph_node` | Get details about a specific AST node |

### Read-Only Mode

`read_only.go` provides a read-only subset of CodeGraph operations that don't require the full runtime — useful when CodeGraph isn't installed.

### Symlink Safety

`symlink_escape_test.go` verifies that CodeGraph doesn't follow symlinks outside the workspace, preventing information leakage.

## LSP Client

The LSP client (`internal/lsp/`) connects to language servers for real-time diagnostics, go-to-definition, and other language-aware features.

### Architecture

```
┌────────────────────────────────────────────────┐
│              LSP Client                        │
│                                                │
│  ┌──────────────┐    ┌──────────────────────┐  │
│  │  LSP Manager │    │  Language Servers    │  │
│  │  (multiplex) │◄───┤  (gopls, tsserver,  │  │
│  │              │    │   pyright, rust-analyzer)│
│  └──────┬───────┘    └──────────────────────┘  │
│         │                                      │
│  ┌──────▼───────┐                             │
│  │  JSON-RPC    │                             │
│  │  Framing     │                             │
│  └──────────────┘                             │
└────────────────────────────────────────────────┘
```

### Manager

`manager.go` manages multiple language server instances:
- Auto-detects language servers based on file types in the workspace.
- Starts servers on demand when a file of the relevant language is opened.
- Routes requests to the appropriate server.

### JSON-RPC

`jsonrpc.go` implements the JSON-RPC 2.0 framing layer for LSP communication:
- Content-length header parsing
- Message serialization/deserialization
- Request/response correlation

### Position

`position.go` handles LSP position conversions — translating between UTF-16 code units (LSP's internal format) and UTF-8 byte offsets (used by Reasonix's file tools).

### Results

`results.go` processes LSP responses into Reasonix's internal types — diagnostics, locations, hover info, etc.

### Tool

`tool.go` exposes LSP functionality as a built-in tool:
- Get diagnostics for a file
- Go to definition
- Find references
- Get hover information

### Frame Capping

`jsonrpc_framecap_test.go` verifies that the JSON-RPC layer handles large messages correctly, capping frame sizes to prevent memory exhaustion from misbehaving language servers.

### Supported Languages

Test data is provided for:
- Python (pyright/pylsp)
- TypeScript (tsserver)
- Go (gopls)
- Rust (rust-analyzer)
- Bash (bash-language-server)

## Integration with the Agent

Both CodeGraph and LSP integrate through the tool registry:
- CodeGraph tools are registered as MCP tools (`mcp__codegraph__*`)
- LSP tools are registered as built-in tools

The agent sees them as regular tools and can use them in its tool loop like any other capability.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Tool System](tool-system.md)
- [MCP Plugin System](mcp-plugin-system.md)
