# Deprecated Frontend (Wails/Vue3)

**Status**: DEPRECATED - Will be removed after Tauri frontend is stable
**Deprecated**: 2026-07-04
**Replacement**: `frontend/` (Tauri v2 + Vue3)

---

## Overview

This directory contains the old Wails-based frontend that was used with Parade before the Tauri migration. It is kept for reference only and will be removed in a future version.

The old frontend communicated directly with the Parade daemon through Wails bindings, which auto-generated Go-to-JavaScript bindings. The new frontend uses Tauri commands and Unix Domain Socket (UDS) communication instead.

---

## Technology Stack

| Component | Old (Wails) | New (Tauri) |
|-----------|-------------|--------------|
| Desktop framework | Wails v2 | Tauri v2 |
| Frontend | Vue 3 + JavaScript + Vite | Vue 3 + TypeScript + Vite |
| State management | Custom composables | Pinia (planned) |
| Backend binding | Auto-generated Go bindings | Manual Rust IPC client |
| IPC transport | Wails runtime | UDS/Named Pipe |
| Event system | wails.Events.On() | Tauri event system |
| Window management | Built-in | Tauri window API |
| Build tool | Wails CLI | Tauri CLI (cargo tauri) |

---

## Directory Structure

```
deprecated/frontend/
├── dist/                          # Build output (do not use)
├── index.html                     # Entry HTML with inline CSS
├── node_modules/                  # Dependencies (do not install)
├── package.json                   # npm config (do not use)
├── package-lock.json              # Lock file (do not use)
├── package.json.md5               # Checksum (ignore)
├── src/
│   ├── App.vue                    # Root Vue component
│   ├── main.js                    # Application entry point
│   ├── lib/
│   │   └── wailsjs/              # Auto-generated Wails bindings
│   │       ├── go/
│   │       │   └── app/          # App method bindings
│   │       │       └── App.js    # OnForeground, etc.
│   │       └── runtime/          # Wails runtime
│   │           ├── runtime.js    # Wails runtime script
│   │           └── runtime.d.ts  # TypeScript definitions
│   ├── components/
│   │   ├── ChatPanel.vue         # Team/private chat interface
│   │   ├── CollapsibleSection.vue # Collapsible panel component
│   │   ├── ConversationList.vue  # Conversation list sidebar
│   │   ├── DownloadList.vue       # Active downloads display
│   │   ├── FileBrowser.vue        # Local/remote file browser
│   │   ├── IdentityPanel.vue      # User identity display
│   │   ├── LanguageToggle.vue     # i18n language switcher
│   │   ├── LogPanel.vue           # Debug log viewer
│   │   ├── PeerStatus.vue         # Connected peers display
│   │   └── TeamPanel.vue          # Team join/leave interface
│   ├── composables/
│   │   ├── useBackend.js          # Backend method invocations
│   │   ├── useEvents.js           # Wails event subscriptions
│   │   ├── useLogStore.js         # Log message store
│   │   └── useStore.js            # App state store
│   └── i18n/                      # Internationalization files
└── vite.config.js                # Vite bundler configuration
```

---

## Key Files Reference

### Entry Point Files

| File | Purpose | Migration Notes |
|------|---------|-----------------|
| `index.html` | Entry HTML with inline CSS design system | Replaced by Tauri HTML entry |
| `src/main.js` | Vue app initialization, i18n setup | Replaced by `frontend/src/main.ts` |
| `src/App.vue` | Root layout (3-column: left, center, right panels) | Reference for new layout |

### Wails Bindings

| File | Purpose | Migration Notes |
|------|---------|-----------------|
| `src/lib/wailsjs/go/app/App.js` | Auto-generated Go method bindings | Replaced by UDS RPC calls |
| `src/lib/wailsjs/runtime/runtime.js` | Wails runtime (events, logging) | Replaced by Tauri events |
| `src/composables/useBackend.js` | Wrapper around Wails calls | Replaced by new RPC client |
| `src/composables/useEvents.js` | Event subscription logic | Replaced by Tauri listen() |

### Components (src/components/)

| Component | Purpose | Reference For |
|-----------|---------|---------------|
| `ChatPanel.vue` | Team/private chat interface with message list and input | Chat UI patterns |
| `ConversationList.vue` | Sidebar showing all conversations | Conversation selection UI |
| `FileBrowser.vue` | Local shared files and remote browse | File browser UI |
| `PeerStatus.vue` | List of connected peers with status | Peer list UI |
| `TeamPanel.vue` | Team join/leave interface | Team management UI |
| `IdentityPanel.vue` | User identity display (public key, status) | Identity display |
| `DownloadList.vue` | Active file transfers with progress | Download manager UI |
| `LogPanel.vue` | Debug log viewer with filtering | Log viewer patterns |
| `CollapsibleSection.vue` | Reusable collapsible panel | UI component patterns |
| `LanguageToggle.vue` | i18n language switcher | i18n integration |

### Composables (src/composables/)

| Store | Purpose | Notes |
|-------|---------|-------|
| `useBackend.js` | Wrapper for Wails Go method calls | Reference for RPC call patterns |
| `useEvents.js` | Wails.Events subscription management | Reference for event handling |
| `useStore.js` | App state (logged in, current team, etc.) | Reference for state shape |
| `useLogStore.js` | Log entries with levels and filtering | Reference for log management |

### Configuration

| File | Purpose | Notes |
|------|---------|-------|
| `vite.config.js` | Vite bundler config (base path, output dir) | Updated for Tauri in new frontend |
| `package.json` | Vue 3, vue-i18n, @vitejs/plugin-vue, vite | Dependencies replaced |

---

## Migration Notes

### What Changed

1. **IPC Mechanism**: Wails runtime bindings became Tauri commands + UDS client
2. **Build System**: Wails CLI (`wails dev`, `wails build`) became Tauri CLI (`cargo tauri dev`, `cargo tauri build`)
3. **Daemon Communication**: Auto-generated Wails bindings became manual Unix Domain Socket RPC client
4. **Event Handling**: `wails.Events.On()` became `Tauri.listen()`
5. **Window Management**: Wails built-in became Tauri window API

### Key Differences

| Aspect | Old (Wails) | New (Tauri) |
|--------|-------------|--------------|
| Backend binding | Auto-generated Go bindings | Manual Rust IPC client |
| IPC transport | Wails runtime (internal) | Unix Domain Socket |
| IPC protocol | Wails JSON-RPC | Standard JSON-RPC 2.0 |
| Event system | wails.Events.On/Emit | Tauri event system |
| Window management | Built-in | Tauri window API |
| Build output | Single binary | Rust binary + WebView |
| Rust required | No | Yes |

### Code Patterns

**Old (Wails) - Calling backend:**
```javascript
import { GetConversations } from './lib/wailsjs/go/app/App.js'
const convs = await GetConversations()
```

**New (Tauri) - Calling backend:**
```typescript
import { invoke } from '@tauri-apps/api/core'
const convs = await invoke('GetConversations')
```

**Old (Wails) - Listening to events:**
```javascript
import { EventsOn } from './lib/wailsjs/runtime/runtime.js'
EventsOn('ui_new_message', (data) => { ... })
```

**New (Tauri) - Listening to events:**
```typescript
import { listen } from '@tauri-apps/api/event'
await listen('ui_new_message', (event) => { ... })
```

---

## UI/UX Reference

The deprecated frontend provides reference implementations for these patterns:

- **Three-column layout**: Left panel (identity, team, conversations, peers), Center (chat), Right panel (files, downloads, logs)
- **Collapsible sidebars**: Panels can be collapsed to maximize chat space
- **Warm editorial design**: Light theme with warm colors, Georgia serif for headings, system sans-serif for body
- **Real-time updates**: Messages, file transfers, and peer status update via events
- **Log viewer**: Debug console with level filtering and auto-scroll

---

## Action Required

- **DO NOT** modify files in this directory
- **DO NOT** build or run this frontend
- **DO NOT** install dependencies via `npm install`
- **USE** the new frontend in `frontend/` instead
- **REFERENCE** this code only for UI/UX patterns and component structure

---

## Removal Timeline

This directory will be removed after:

1. Tauri frontend reaches feature parity (at minimum):
   - Chat (team + private)
   - File sharing (browse, download, upload)
   - Peer discovery and connection
   - Identity management (register, login)
   - Team management (join, leave)

2. Migration documentation is complete

3. No active development references this code

---

## Related Documentation

- Main README: `/README.md`
- New frontend location: `/frontend/`
- Tauri v2 documentation: https://tauri.app/
- Wails v2 (archived): https://wails.io/
