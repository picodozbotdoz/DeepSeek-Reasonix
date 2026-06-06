# Desktop Frontend Components

The desktop app is a Wails-based application with a React frontend (`desktop/frontend/`). It has 30+ React components that provide a rich, native-like GUI for Reasonix. This document describes the component architecture, key components, and the bridge between the Go backend and the TypeScript frontend.

## Architecture

### Technology Stack

- **Wails v2**: Go backend + WebView frontend
- **React**: UI component library
- **TypeScript**: Type-safe frontend code
- **Tailwind CSS**: Utility-first styling

### Directory Structure

```
desktop/frontend/src/
├── components/        ← React components
│   └── editors/       ← Code editor components
├── lib/               ← Utilities and hooks
└── locales/           ← i18n locale files
```

### Backend-Frontend Bridge

The `lib/bridge.ts` module provides the TypeScript bindings for Wails runtime calls. It wraps the Go backend methods (Send, Cancel, Approve, etc.) in typed async functions that the React components call.

## Key Components

### Conversation & Messages

#### `Transcript.tsx`

The main conversation view that renders the full message history:

- User messages with markdown rendering
- Assistant messages with streaming text support
- Tool cards (nested under assistant messages)
- Reasoning blocks (collapsible)
- Message actions (copy, retry)

#### `Message.tsx`

A single message in the conversation. Handles:

- Streaming text with real-time rendering
- Markdown rendering with syntax highlighting
- Tool call groups (multiple calls in one assistant turn)
- Reasoning content display

#### `ToolCard.tsx`

Displays a tool call's lifecycle:

- **Dispatch state**: Tool name, arguments, read-only badge
- **Preview state**: Diff view for writer tools
- **Approval state**: Allow/deny buttons with explanation
- **Result state**: Output display with truncation indicator
- **Nested sub-agent calls**: Indented tool cards under the parent task

### User Input

#### `Composer.tsx`

The main input area at the bottom of the chat:

- Multi-line text input with auto-resize
- File attachment support (`@` mentions)
- Image paste support
- Slash command autocomplete
- Submit on Enter (Shift+Enter for newline)

#### `SlashMenu.tsx`

An autocomplete menu for slash commands:

- Fuzzy search filtering
- Command descriptions from i18n catalog
- Keyboard navigation (up/down/enter)

#### `AskCard.tsx`

Renders the `ask` tool's multiple-choice questions:

- Tab-based question layout
- Option selection (single and multi-select)
- Free-text input option
- Submit button

### Navigation & Panels

#### `TabBar.tsx`

The top-level tab bar for managing multiple conversations:

- Session tabs with titles
- Topic grouping
- Close/reopen tabs
- New tab button

#### `HistoryPanel.tsx`

A sidebar panel showing conversation history:

- Session list with previews
- Scope filtering (project/global)
- Search/filter
- Resume functionality

#### `ContextPanel.tsx`

Shows the current context window usage:

- Token count visualization
- Cache hit rate display
- Compaction trigger indicator

#### `WorkspacePanel.tsx`

Displays the current workspace structure:

- File tree browser
- Recent files
- Workspace root indicator

#### `ProjectTree.tsx`

A tree view of the project's file structure:

- Expandable directories
- File type icons
- Click to read files

### Settings & Configuration

#### `SettingsPanel.tsx`

The main settings interface:

- Provider configuration
- API key management
- Model selection
- Sandbox settings
- Permission rules
- Hook configuration

#### `CapabilitiesPanel.tsx`

Shows the current agent capabilities (read-only projection from `internal/inspect/`):

- Available providers and their status
- Tool list (built-in vs MCP, read-only badge)
- MCP server connections
- Slash commands

#### `ModelSwitcher.tsx`

A dropdown for switching models mid-session:

- Lists configured providers
- Shows the current model
- Triggers a controller rebuild on switch

#### `EffortSwitcher.tsx`

A control for setting the effort level:

- Auto, Low, Medium, High, XHigh, Max options
- Persists to configuration

### Approval & Security

#### `ApprovalModal.tsx`

A modal dialog for tool approval:

- Tool name and arguments
- Diff preview for writer tools
- Allow/deny with session-persist option
- Source attribution (built-in, MCP, skill)

#### `MemoryPanel.tsx`

Manages memory files:

- List loaded memory documents
- Edit/remember/forget operations
- Source paths

#### `TodoPanel.tsx`

Displays the current todo list:

- Task status badges (pending, in_progress, completed)
- Active form display
- Nesting levels

### UI Primitives

#### `Markdown.tsx`

Renders markdown content in the chat:

- Syntax highlighting for code blocks
- LaTeX rendering (via KaTeX or similar)
- Link handling
- Image display
- Table formatting

#### `DiffView.tsx`

Renders unified diffs for tool previews:

- Added/removed line highlighting
- Line numbers
- Syntax highlighting
- Fold controls for long diffs

#### `CodeViewer.tsx`

A read-only code viewer with:

- Syntax highlighting
- Line numbers
- File path display

#### `FloatingMenu.tsx`

A context menu that appears on text selection:

- Copy
- Explain
- Refactor

#### `ContextMenu.tsx`

A right-click context menu:

- Copy
- Paste image
- Explain selection

#### `ResizableDrawer.tsx`

A draggable panel for sidebar components:

- Min/max width constraints
- Smooth resize animation
- Collapse/expand toggle

#### `AnchoredPopover.tsx`

A popover component anchored to a trigger element:

- Used for dropdowns, tooltips, and pickers
- Click-outside to close
- Keyboard accessible

#### `Tooltip.tsx`

A hover tooltip component with:

- Delayed appearance
- Position adjustment for viewport boundaries

#### `InlineConfirmButton.tsx`

A button that requires confirmation:

- Click once to arm (changes to confirm state)
- Click again to execute
- Click elsewhere to cancel

#### `CopyButton.tsx`

A one-click copy button with:

- Success feedback (icon change)
- Error handling

### Onboarding

#### `Welcome.tsx`

First-run experience:

- Product introduction
- Setup steps
- Link to init wizard

#### `OnboardingOverlay.tsx`

A guided overlay for new users:

- Highlights key UI elements
- Step-by-step walkthrough
- Dismissible

#### `UpdateBanner.tsx`

A banner that appears when a new version is available:

- Version number
- Download link
- Dismiss button

### File Management

#### `FileMenu.tsx`

Context menu for file operations:

- Open in editor
- Copy path
- Delete
- View diff

#### `ArgMenu.tsx`

An autocomplete menu for `@` file mentions:

- File path completion
- Fuzzy search
- Preview of selected file

## Library Modules

### `useController.ts`

A React hook that provides access to the Wails controller:

- Send messages
- Cancel turns
- Approve/deny tool calls
- Switch models
- Manage sessions

### `session.ts`

Session state management:

- Active session tracking
- Session persistence
- Branch management

### `theme.ts`

Theme management:

- Dark/light mode detection
- Theme switching
- CSS variable generation

### `i18n.tsx`

Frontend internationalization:

- Language detection
- Catalog loading
- Translation function

### `tools.ts`

Tool metadata utilities:

- Tool classification (reader/writer)
- Tool icon mapping
- Tool description formatting

### `types.ts`

Shared TypeScript type definitions:

- Event types
- Tool types
- Session types
- Configuration types

### `diff.ts`

Diff computation and rendering utilities:

- Unified diff generation
- Line classification (added/removed/unchanged)
- Diff statistics

### `highlight.ts`

Syntax highlighting utilities:

- Language detection from file extension
- Chroma/theme integration
- Token classification

### `todoVisibility.ts`

Todo panel visibility state:

- Show/hide toggle
- Persistence across sessions

### `layoutPreferences.ts`

Layout preference management:

- Panel sizes
- Panel visibility
- Sidebar state

### `projectColors.ts`

Project-specific color generation:

- Deterministic colors from project paths
- Used for tab indicators and workspace badges

### `windowState.ts`

Window state persistence:

- Size and position
- Maximized/normal state
- Panel layout

### `workspaceDrag.ts`

Drag-and-drop workspace support:

- File drag from external sources
- Tab reordering

### `shellExpand.tsx`

Shell command expansion in the composer:

- `!` prefix detection
- Shell mode indicator

### `useUpdater.ts`

Update checking hook:

- Periodic version check
- Download progress
- Install prompt

### `crash.ts`

Crash reporting and recovery:

- Error boundary integration
- Crash log collection
- Recovery prompts

### `textSize.ts`

Text size preferences:

- Scale factor
- Persistence
- Accessibility support

### `lang.ts`

Language utilities:

- Current language tag
- Formatting helpers (numbers, dates)
