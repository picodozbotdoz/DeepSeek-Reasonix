# Desktop App Architecture

The desktop app is a Wails-based application that provides a native GUI for Reasonix. It drives the same `control.Controller` as the TUI and HTTP frontends, wrapped in a system-tray-aware, multi-tabbed, auto-updating desktop experience.

## Architecture

```
┌────────────────────────────────────────────────────────┐
│                    Desktop App                         │
│                                                        │
│  ┌──────────────────────────────────────────────────┐  │
│  │              Wails Runtime                        │  │
│  │  ┌──────────────┐  ┌──────────────────────────┐  │  │
│  │  │  Go Backend  │  │  React/TypeScript        │  │  │
│  │  │  (Wails      │◄─┤  Frontend                │  │  │
│  │  │   Bindings)  │──►│                          │  │  │
│  │  └──────┬───────┘  └──────────────────────────┘  │  │
│  │         │                                         │  │
│  │  ┌──────▼───────┐                                │  │
│  │  │  Controller  │  (same control.Controller)     │  │
│  │  └──────────────┘                                │  │
│  └──────────────────────────────────────────────────┘  │
│                                                        │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐  │
│  │  System Tray │  │  Tab Manager │  │  Updater     │  │
│  └──────────────┘  └──────────────┘  └─────────────┘  │
└────────────────────────────────────────────────────────┘
```

## Go Backend

The desktop is a separate Go module (`desktop/go.mod`) that imports the main Reasonix kernel. Key files:

| File | Purpose |
|------|---------|
| `main.go` | Wails entry point, app initialization |
| `app.go` | Core app logic, Wails bindings |
| `wire.go` | Dependency injection wiring |
| `tray.go` | System tray icon and menu |
| `tabs.go` | Multi-tab session management |
| `sessions.go` | Session persistence and resume |
| `workspace.go` | Workspace detection and changes |
| `menu.go` | Native menu bar |
| `settings_app.go` | Settings management |
| `dotenv.go` | .env loading for desktop |
| `updater.go` | Auto-update logic |
| `single_instance.go` | Single-instance enforcement |
| `window_state.go` | Window position/size persistence |
| `system_quit.go` | Graceful shutdown |

### Wails Bindings

The Go backend exposes methods to the React frontend via Wails bindings. The frontend calls these through the `bridge.ts` library:

```typescript
// desktop/frontend/src/lib/bridge.ts
import { Get, Post } from "../wailsjs/go/desktop/App"
```

Key bindings include all controller operations (Submit, Cancel, Approve, etc.) plus desktop-specific features (tab management, settings, updates).

### Wire (Dependency Injection)

`wire.go` sets up the full dependency graph for the desktop app — loading config, building the controller, wiring event sinks, and connecting the Wails runtime. It mirrors the boot sequence in `internal/boot/`.

## React Frontend

The frontend (`desktop/frontend/`) is a TypeScript/React application:

| Directory | Purpose |
|-----------|---------|
| `src/components/` | UI components |
| `src/lib/` | Utilities (bridge, types, theme, etc.) |
| `src/locales/` | i18n strings (en, zh) |
| `src/styles.css` | Global styles |

### Key Components

| Component | Description |
|-----------|-------------|
| `App.tsx` | Root component, layout management |
| `Composer.tsx` | Message input with autocomplete |
| `Transcript.tsx` | Chat message history |
| `Message.tsx` | Individual message rendering |
| `ToolCard.tsx` | Tool call display with diffs |
| `ApprovalModal.tsx` | Tool approval dialog |
| `AskCard.tsx` | Structured question cards |
| `DiffView.tsx` | File diff visualization |
| `TodoPanel.tsx` | Task list display |
| `ContextPanel.tsx` | Context/memory viewer |
| `MemoryPanel.tsx` | Memory file editor |
| `WorkspacePanel.tsx` | Workspace file tree |
| `HistoryPanel.tsx` | Session history browser |
| `CapabilitiesPanel.tsx` | Skill/MCP tool browser |
| `SettingsPanel.tsx` | App settings |
| `ModelSwitcher.tsx` | Model selection dropdown |
| `EffortSwitcher.tsx` | Reasoning effort control |
| `TabBar.tsx` | Multi-tab session tabs |
| `StatusBar.tsx` | Context gauge, cache stats, cost |
| `UpdateBanner.tsx` | Auto-update notification |
| `OnboardingOverlay.tsx` | First-run setup wizard |
| `PromptShelf.tsx` | Saved prompts |
| `ProjectTree.tsx` | Project file tree |
| `SlashMenu.tsx` | Slash command autocomplete |
| `ArgMenu.tsx` | Argument autocomplete |

### Bridge Library

`bridge.ts` wraps the Wails Go bindings with TypeScript-friendly APIs and event handling. It manages the bidirectional communication between the React frontend and the Go backend.

### Theme System

The desktop supports light and dark themes, controlled via the system theme or manual toggle. Theme state is synchronized with the Wails backend.

### i18n

Internationalization is handled via locale files (`en.ts`, `zh.ts`) and the `i18n.tsx` library. The language auto-detects from the system locale or can be set manually.

## System Tray

The system tray (`tray.go`) provides:
- Show/hide the main window
- Quick access to recent sessions
- Status indication (idle, running)
- Quit

Platform-specific implementations:
- `tray_icon_unix.go` — Linux tray icon
- `tray_icon_windows.go` — Windows tray icon
- `tray_loop_windows.go` — Windows event loop
- `tray_loop_external.go` — External tray loop
- `tray_supported_*.go` — Platform support detection

## Multi-Tab Sessions

`tabs.go` manages multiple concurrent sessions, each with its own controller. The tab bar shows all open sessions with labels derived from the first message or LLM-generated titles.

## Auto-Updater

The updater (`updater.go`, `updater_app.go`) checks for new versions and prompts the user to update. It verifies update manifests with cryptographic signatures (`internal/update/verify.go`) to prevent supply-chain attacks.

## Single Instance

`single_instance.go` ensures only one desktop app instance runs at a time. A second launch activates the existing instance instead.

## Window State

`window_state.go` persists the window position and size across launches, restoring the previous layout.

## Workspace Detection

`workspace.go` detects the current workspace (project directory) and watches for changes. The workspace root is used for:
- File-tree display
- Sandbox confinement
- Checkpoint restore boundaries

## Build

The desktop app is built with Wails:

```bash
cd desktop
wails build
```

This produces a platform-native application (`.app` on macOS, `.exe` on Windows, AppImage on Linux).

## See Also

- [Architecture Overview](architecture-overview.md)
- [Controller & Frontends](controller-frontends.md)
- [Event System](event-system.md)
