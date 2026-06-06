# Building & Distribution

Reasonix is distributed as a single static Go binary, installable via npm, Homebrew, or direct download. This document covers the build system, cross-compilation, npm packaging, and release process.

## Build System

### Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build CLI binary + example plugin → `bin/` |
| `make cross` | Cross-compile for all 6 targets → `dist/` |
| `make test` | Run the full test suite |
| `make vet` | Run `go vet ./...` |
| `make fmt` | Run `gofmt -w .` |
| `make hooks` | Install git hooks (pre-push: go vet) |
| `make clean` | Remove `bin/` and `dist/` |
| `make e2e-codegraph` | Fetch CodeGraph and run gated e2e test |

### Build Command

```bash
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/reasonix ./cmd/reasonix
```

- `CGO_ENABLED=0` — pure Go, no C dependencies.
- `-s -w` — strip debug info and DWARF symbols for smaller binaries.
- `-X main.version=...` — inject the version string from `git describe --tags --always`.

### Cross-Compilation

```bash
make cross
```

Produces binaries for all 6 targets:
- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

### Version Injection

```makefile
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
```

The version is injected at build time via ldflags. `git describe --tags --always` produces a version like `v1.0.0` or `v1.0.0-5-gabcdef` (5 commits after tag, at commit abcdef).

## Entry Point

`cmd/reasonix/main.go` is the CLI entry point. It:
1. Blank-imports built-in providers and tools (triggering `init()` registration):
   ```go
   import _ "reasonix/internal/provider/openai"
   import _ "reasonix/internal/provider/anthropic"
   import _ "reasonix/internal/tool/builtin"
   ```
2. Parses CLI flags and subcommands.
3. Delegates to `internal/cli/` for the actual command execution.

## npm Packaging

The `npm/` directory contains the npm wrapper:

```
npm/
├── build.mjs           # Build script: downloads platform-specific binary
└── reasonix/
    ├── bin/
    │   └── reasonix.js # Node.js entry point that spawns the native binary
    └── package.json     # npm package metadata
```

### How It Works

1. `npm i -g reasonix` installs the npm package.
2. The postinstall script (`build.mjs`) detects the platform and architecture.
3. It downloads the matching prebuilt binary from GitHub releases.
4. The `reasonix.js` entry point spawns the native binary as a subprocess.

This is the same model used by esbuild, Biome, and other native-binary-via-npm tools — npm is the installer, not a runtime dependency.

### npm Tags

- `latest` — deliberately stays on `0.x` (the legacy TypeScript line).
- `next` — the Go rewrite (1.x). Users must opt in: `npm i -g reasonix@next`.

## Homebrew

```bash
brew install esengine/reasonix/reasonix
```

The Homebrew formula downloads the prebuilt binary for macOS (amd64/arm64).

## Release Archives

Every GitHub release includes:
- Prebuilt archives: `reasonix-<os>-<arch>.tar.gz` / `.zip`
- `SHA256SUMS` file for verification
- The desktop installer (platform-specific)

## Example Plugin

`cmd/reasonix-plugin-example/` builds a reference MCP stdio server:

```bash
make build  # also builds bin/reasonix-plugin-example
```

This server provides:
- `echo` tool — echoes back its input
- `wordcount` tool — counts words in text
- `review` prompt — generates a code review prompt
- Style-guide resource — exposes a coding style guide

## Testing

```bash
make test                   # all tests
go test ./internal/agent/ -v           # verbose, one package
go test ./internal/tool/builtin/ -run TestGrep  # one test
```

The test suite is extensive — nearly every package has companion `_test.go` files with unit tests, integration tests, and e2e tests.

### CodeGraph E2E Test

```bash
make e2e-codegraph
```

Fetches the matching CodeGraph bundle and runs the gated MCP end-to-end test. Requires `gh` (GitHub CLI).

## Desktop Build

```bash
cd desktop
wails build
```

Produces a platform-native application. See [Desktop App](desktop-app.md) for details.

## Git Hooks

```bash
make hooks  # installs .githooks/ as core.hooksPath
```

The pre-push hook runs `go vet ./...` to catch issues before they reach CI.

## Dependency Policy

- Standard library by default.
- A third-party dependency must be pure-Go, lightweight, and must not compromise the single-binary / cross-platform / distribution story.
- Current dependencies:
  - `BurntSushi/toml` — TOML parsing (the one accepted dependency)
  - Charm stack (bubbletea, lipgloss, bubbles) — TUI rendering
  - `yuin/goldmark` — Markdown rendering
  - `alecthomas/chroma` — Syntax highlighting
  - `sabhiram/go-gitignore` — .gitignore support
  - `golang.org/x/text` — encoding detection
  - `golang.org/x/net` — HTTP client
  - `golang.org/x/term` — terminal control

## See Also

- [Architecture Overview](architecture-overview.md)
- [Desktop App](desktop-app.md)
