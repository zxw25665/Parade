# Frontend 层设计文档

## 1. 技术栈

| 层 | 选型 | 版本 | 说明 |
|---|------|------|------|
| 框架 | Vue 3 | 3.5.13 | Composition API + `<script setup>` 语法 |
| 构建 | Vite | 6.3.1 | `base: './'`, `outDir: 'dist'`, Wails 静态嵌入 |
| 国际化 | vue-i18n | 10.0.8 | Composition 模式, `en` + `zh`, 持久化到 `localStorage['parade-locale']` |
| 状态 | Vue `reactive()` 单例 | — | 无 Pinia/Vuex, 3 个 composable 管理全局状态 |
| 路由 | 无 | — | 固定三栏布局, 无页面跳转, 由状态驱动视图切换 |
| 样式 | 手写 CSS | — | 无 Tailwind/UI 库, `index.html` 定义设计令牌 + 基础布局, 组件 `<style scoped>` |
| IPC | Wails v2 自动生成 | — | `lib/wailsjs/go/app/App.js` 生成 Go↔JS 桥接 |

## 2. 组件树

```
index.html (全局 CSS: 357 行设计令牌 + 布局 + 表单/按钮基础样式)
 └─ <div id="app">
     └─ App.vue (provide: 'events', 'store')
        ├─ <aside class="side-panel left" [可折叠]>
        │   ├─ LanguageToggle
        │   └─ CollapsibleSection × 4
        │       ├─ IdentityPanel        ← 身份: 检测/注册/登录
        │       ├─ TeamPanel            ← 团队: 加入/切换/离开
        │       ├─ ConversationList     ← 会话列表, 点击选中
        │       └─ PeerStatus           ← 当前节点 UI (在线/离线点)
        ├─ <main class="center-panel">
        │   └─ ChatPanel               ← 消息 + 输入
        └─ <aside class="side-panel right" [可折叠]>
            └─ CollapsibleSection × 3
                ├─ FileBrowser          ← 本地/远程文件浏览
                ├─ DownloadList         ← 下载进度条
                └─ LogPanel             ← 结构化日志查看器
```

**布局机制**: `display: flex; width: 100%; height: 100%`。侧栏 `width: var(--sidebar-width)` (280px) 展开时, `width: 0` 折叠时。切换按钮绝对定位浮动。中间栏 `flex: 1`。

## 3. 状态管理 (无 Pinia, 3 个 Composable)

项目使用 3 个模块级 `reactive()` 单例, 通过 composable 导出。

### 3.1 useStore — 应用核心状态

文件: `frontend/src/composables/useStore.js`

```js
// 单例 reactive 对象, 包含:
{
  hasIdentity: bool,          // 是否存在 .parade_identity 文件
  loggedIn: bool,             // 是否已登录
  pubkey: string,             // 本节点 Curve25519 公钥 (base64)
  teamJoined: bool,           // 是否已加入团队
  teams: [],                  // 团队列表 [{id, name, active, team_hash}]
  activeTeamId: string,       // 当前活跃团队 ID
  peerTests: {},              // 节点连接测试结果 (PeerList 使用)
  conversations: [],          // 会话列表 [{id, type, display_name, peer_pubkey, last_message, ...}]
  activeConversationId: string, // 当前活跃会话 ID
  peersWithStatus: [],        // 带状态的节点列表 [{uuid, pubkey, ip, online, last_seen}]
  messagesByConv: {}          // 按会话 ID 组织的消息 { convId: [messages] }
}
```

**消费方式**: 组件通过 `inject('store')` 获取, 部分组件也直接调用 `useStore()`。

### 3.2 useEvents — Wails 事件订阅

文件: `frontend/src/composables/useEvents.js`

在 `onMounted` 时通过 `EventsOn` 注册 8 个后端事件, `onUnmounted` 时 `EventsOff`。导出 `reactive({ peers, downloads, completedDownloads })`。

**事件负载归一化**: 每个事件处理器同时处理 `snake_case` (Go 风格: `peer_uuid`, `ip_address`) 和 `PascalCase` (Wails 序列化风格) 字段名。

| Wails 事件 | 状态变更 | 后端来源 |
|---|---|---|
| `ui_peer_joined` | `events.peers[]`, `store.peersWithStatus[]` | `TopicPeerJoined` handler |
| `ui_peer_left` | 两者同步移除 | `TopicPeerLeft` handler |
| `ui_peer_status` | `store.peersWithStatus[]` (upsert) | `TopicPeerOnline/Offline` |
| `ui_conversation_updated` | 无直接变更 — 触发组件重新拉取 | `TopicConvSyncRequest` |
| `ui_new_message` | `store.messagesByConv[convId]` (push, 按 `id` 去重, 按 `hlc` 排序) | `TopicMsgReceived` / `TopicPrivateMsgReceived` |
| `ui_file_progress` | `events.downloads[taskId]` | `TopicFileProgress` handler |
| `ui_file_completed` | 将任务从 `downloads{}` 移至 `completedDownloads[]` | `TopicFileCompleted` handler |
| `ui_log` | `logStore.addLogEntry()` | `LogBroker` 回调 |

**`ui_conversation_updated` 是纯信号事件** — 本身不携带数据, 收到后组件自行调用 `listConversations()` / `getConversationMessages()` 重新拉取。

**`ui_new_message` 去重策略**: 按消息 `id` 字段去重 (`!msgs.some(m => m.id === msg.id)`), 按 HLC 字符串字典序排序 (`msgs.sort((a, b) => a.hlc.localeCompare(b.hlc))`)。

### 3.3 useLogStore — 日志缓冲

文件: `frontend/src/composables/useLogStore.js`

```js
// 单例 reactive 对象
{
  entries: []    // 最新在前, 最大 5000 条 (unshift 实现)
}
// 导出: addLogEntry(entry), clearLogs()
```

## 4. IPC 架构

### 4.1 出站 (前端 → Go)

文件: `frontend/src/composables/useBackend.js`

导入 `lib/wailsjs/go/app/App.js` 生成的 30 个函数, 每个通过 `wrapIPC(name, fn)` 包装:

```js
// wrapIPC 逻辑:
1. logStore.addLogEntry({ level: 2, source: 'ipc', message: `called ${name}` })
2. const start = performance.now()
3. 调用 fn(...args)
4. 成功: logStore.addLogEntry({ level: 2, source: 'ipc', message: `ok ${name} (${dur}ms)` })
5. 失败: logStore.addLogEntry({ level: 4, source: 'ipc', message: `ERROR ${name} (${dur}ms)` }), 重新抛出错误
```

**暴露的方法列表** (camelCase, 与 Go `App` 结构体 Wails 绑定方法 1:1 对应):

`checkHasIdentity`, `register`, `login`, `joinTeam`, `joinTeamWithName`, `listTeams`, `getActiveTeam`, `switchTeam`, `leaveTeam`, `getPubKey`, `listConversations`, `getConversationMessages`, `startPrivateConversation`, `sendTeamChat`, `sendPrivateChat`, `getPeers`, `getPeersWithStatus`, `connectToPeer`, `shareDirectory`, `unshareDirectory`, `getDirectoryChildren`, `getRemoteDirectoryChildren`, `startDownload`, `getDefaultDownloadDir`, `createShareGroup`, `listShareGroups`, `addDirectoryToShareGroup`, `removeDirectoryFromShareGroup`, `deleteShareGroup`, `getShareGroupDirs`, `onForeground`, `exportLogs`, `writeLogFile`

### 4.2 入站 (Go → 前端)

见 3.2 节 `useEvents` 的 8 个 `EventsOn` 订阅。

## 5. 组件详解

### 5.1 App.vue — 布局根组件

- 三栏 flex 布局, 提供 `store` + `events` 通过 `provide/inject`
- 注册 `document.visibilitychange` → `OnForeground` 处理器
- 可折叠侧栏: `leftOpen` / `rightOpen` ref 控制, 切换按钮绝对定位
- 唯一 `scoped` 样式: `.logo-header` flex 布局

### 5.2 IdentityPanel.vue — 身份面板

三种状态:
1. **检测中**: 调用 `checkHasIdentity()` 确定本地是否有 `.parade_identity`
2. **注册**: 表单输入密码, 调用 `register(password)`
3. **登录**: 表单输入密码, 调用 `login(password)`

登录成功后调用 `GetPubKey()` 以获取公钥, 并调用 `getRecentHistory(200, 0)` 预填充历史消息。

> **注意**: `getRecentHistory` 方法在 `useBackend.js` 中**未导出** — 这段代码是残留的过时逻辑, 运行时会导致 ReferenceError。当前历史消息应该通过 `getConversationMessages()` 获取。

### 5.3 TeamPanel.vue — 团队面板

- 输入: 可选的 `teamName` + 必需的 `teamSecret`
- 挂载时和 `store.loggedIn` 变更时自动调用 `listTeams()` + `getActiveTeam()`
- 支持操作: 加入 (`joinTeamWithName`), 切换 (`switchTeam`), 离开 (`leaveTeam`)

### 5.4 ConversationList.vue — 会话列表

- 列表展示 `store.conversations`, 排序: 团队会话优先, 然后是私聊
- 点击行选中 → 设置 `store.activeConversationId`
- 自动刷新时机: `store.loggedIn` / `teamJoined` / `activeTeamId` 变更, 以及 `ui_conversation_updated` 事件

### 5.5 PeerStatus.vue — 节点状态 (当前标准实现)

- 调用 `getPeersWithStatus()` 获取带心跳时间戳的全量节点列表
- 在线/离线状态用圆点指示 (绿色/灰色)
- 点击节点行 → 调用 `startPrivateConversation(pubkey)` 创建/选中私聊会话

### 5.6 PeerList.vue — 节点列表 (已废弃)

> **⚠ 孤儿组件**: 文件存在于 `components/` 中, 但**未在 `App.vue` 中挂载**。使用旧版 4 阶段连接 UI (`getPeers` + `connectToPeer`), 已被 `PeerStatus.vue` 取代。

### 5.7 ChatPanel.vue — 聊天面板 (中心区域)

- 根据 `store.activeConversationId` 渲染当前会话
- 分页加载: 调用 `getConversationMessages(convId, limit, offset)`
- 发送消息: 团队消息 `sendTeamChat(text)`, 私聊 `sendPrivateChat(targetUUID, text)`
- 监听 `ui_conversation_updated` 事件 → 缓存失效 → 重新拉取
- 消息排序: 始终按 `hlc.localeCompare` 排序
- 消息去重: 按消息 `id` 字段去重

### 5.8 FileBrowser.vue — 文件浏览器

两种模式:
- **本地模式**: `shareDirectory`, `unshareDirectory`, `getDirectoryChildren`
- **远程模式**: `getRemoteDirectoryChildren(peerUUID, path)`, `startDownload(peerUUID, remotePath, localPath)`
- 默认下载目录: `getDefaultDownloadDir()` → `~/Downloads`

### 5.9 DownloadList.vue — 下载列表

- 读取 `events.downloads` (活跃) 和 `events.completedDownloads` (已完成)
- 渲染进度条: `--color-success` 填充, `--color-border` 底色
- **纯事件消费者** — 不发起任何 IPC 调用

### 5.10 LogPanel.vue — 日志面板

- 直接消费 `logStore.entries`
- 支持按级别过滤 (1-5) 和按来源过滤 (ipc, network, file 等)
- 自动滚动开关
- 导出功能: 调用 `ExportLogs()`, 失败时降级为客户端 JSON blob 下载

### 5.11 CollapsibleSection.vue — 可折叠区段

- 可复用的包装器: `title` + `defaultOpen` props
- 纯 `<slot/>` 内容区
- 在 `App.vue` 中被使用 7 次

### 5.12 LanguageToggle.vue — 语言切换

- 切换 `vue-i18n` 语言环境 (en ↔ zh)
- 持久化到 `localStorage['parade-locale']`

## 6. 国际化 (i18n)

文件: `frontend/src/i18n/index.js`

```js
import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'

// 语言环境检测: localStorage['parade-locale'] > navigator.language > 'en'
const saved = localStorage.getItem('parade-locale')
const locale = saved || (navigator.language.startsWith('zh') ? 'zh' : 'en')

export default createI18n({
  locale,
  fallbackLocale: 'en',
  messages: { en, zh }
})
```

**翻译文件**:
- `en.json` (146 行): app, panel, identity, team, peer, chat, conv, file, downloads, logs 10 个命名空间
- `zh.json`: 中文镜像

所有用户可见字符串使用 `$t('path.to.key')` 格式, 无硬编码用户字符串。

## 7. 样式系统

### 7.1 全局样式表 (`index.html` 第 8-357 行)

**设计令牌** (CSS 自定义属性, `:root`):

| 令牌 | 值 | 用途 |
|---|---|---|
| `--color-bg` | `#faf8f5` | 暖色编辑风格背景 |
| `--color-surface` | `#ffffff` | 卡片/面板表面 |
| `--color-text` | `#2c2c2c` | 正文颜色 |
| `--color-text-secondary` | `#6b6b6b` | 次要文本 |
| `--color-border` | `#e0ddd6` | 边框/分隔线 |
| `--color-accent` | `#c7522a` | 强调色 (赤陶色, 用于按钮/链接/已发送消息) |
| `--color-success` | `#698b63` | 成功状态 (下载进度/在线指示) |
| `--color-error` | `#b84c4c` | 错误状态 |
| `--font-display` | serif | 标题字体 |
| `--font-body` | sans-serif | 正文字体 |
| `--font-mono` | monospace | 等宽字体 (日志/代码) |
| `--sidebar-width` | `280px` | 侧栏宽度 |
| `--radius` | `8px` | 圆角半径 |
| `--shadow` | `0 1px 3px rgba(0,0,0,0.08)` | 基础阴影 |

**基础样式**:
- 全局 reset `* { box-sizing: border-box; margin: 0; padding: 0 }`
- 表单/按钮/徽章/选择框/文本域统一默认样式
- 辅助类: `.row`, `.row-wrap`, `.list`, `.list-item`, `.badge-*`, `.error`, `.success`, `.hint`
- 进度条: `.progress-bar` (容器) > `.progress-fill` (填充)
- 空状态: `.empty-state` (居中灰色文本)
- 日志: `.log-*` (日志行, 时间戳, 来源)

**三栏布局**:
- `.app-layout`: `display: flex; width: 100vw; height: 100vh; overflow: hidden`
- `.side-panel.left|right`: `width: var(--sidebar-width); display: flex; flex-direction: column; border-right/left`
- `.side-panel.collapsed`: `width: 0; overflow: hidden`
- `.center-panel`: `flex: 1; display: flex; flex-direction: column`
- `.panel-toggle`: 绝对定位浮动切换按钮 (展开/折叠箭头图标)
- `.panel-section*`: 侧栏内容区样式 (标题/内容滚动)

**聊天样式**:
- `.message-list`: flex 列, `overflow-y: auto`
- `.message-item`: 对齐左/右 (`.self → float: right`), 不同背景色
- `.chat-header`: 会话标题栏
- `.chat-input-area`: 发送文本域 + 按钮
- `.tab-bar`: 模式切换标签

### 7.2 组件级 `<style scoped>`

每个组件补充自有样式:
- `ChatPanel`: 已发送消息使用 `--color-accent` 背景
- `PeerStatus`: `.peer-row .offline` 灰色化, `.peer-pubkey` 等宽字体
- `LogPanel`: 按日志级别着色 (Trace=灰, Debug=蓝, Info=绿, Warning=橙, Error=红)
- `FileBrowser`: 树形缩进, 文件夹/文件图标
- `DownloadList`: 进度条动画

## 8. 路由设计

**无路由库。** 应用是单页单视图聊天客户端。导航方式:

- 本地组件状态控制: `leftOpen` / `rightOpen` ref 控制面板折叠 (App.vue 内部)
- 共享状态控制: `store.activeConversationId` 决定 ChatPanel 显示哪个会话
- 模式切换: `FileBrowser` 内部 `mode` ref 切换 `'local'` / `'remote'`

无 URL 变化, 无前进/后退, 无页面跳转。

## 9. 组件 → IPC 交叉引用

| 组件 | 使用的 IPC 方法 |
|---|---|
| App.vue | `onForeground` (响应 `visibilitychange`) |
| IdentityPanel | `checkHasIdentity`, `register`, `login`, `GetPubKey` (原生), `getRecentHistory` (⚠ 过时残留) |
| TeamPanel | `joinTeamWithName`, `listTeams`, `getActiveTeam`, `switchTeam`, `leaveTeam` |
| ConversationList | `listConversations` (事件触发 + store 变更) |
| PeerStatus | `getPeersWithStatus`, `startPrivateConversation`, `connectToPeer` |
| ChatPanel | `getConversationMessages`, `sendTeamChat`, `sendPrivateChat` |
| FileBrowser | `getDefaultDownloadDir`, `shareDirectory`, `unshareDirectory`, `getDirectoryChildren`, `getRemoteDirectoryChildren`, `startDownload` |
| DownloadList | 无 (纯事件消费者) |
| LogPanel | `exportLogs` (原生 `window.go.app.App.ExportLogs`), 降级为客户端 blob 下载 |
| PeerList (孤儿) | `getPeers`, `connectToPeer` |
| CollapsibleSection | 无 (纯 UI 包装器) |
| LanguageToggle | 无 (纯 i18n 操作) |

## 10. 事件入站 ↔ 出站全图

```
                                ┌─────────────────────────────────────────┐
  Vue3 前端                      │          Go 后端 (内部)                  │
                                 │                                        │
  useBackend.js                  │                                        │
   ├─ sendTeamChat(text)  ───────┼─→ App.SendTeamChat(text)               │
   │                             │     ├─ GenerateHLC                     │
   │                             │     ├─ EncryptTeam                     │
   │                             │     ├─ db.InsertMessage                │
   │                             │     ├─ ui.Notify("ui_new_message")  ───┼─→ useEvents → store.messagesByConv
   │                             │     └─ netEng.BroadcastTeam(encrypted) │
   │                             │                                        │
   ├─ getConversationMessages ───┼─→ App.GetConversationMessages          │
   │                             │     └─ db.GetConversationMessages      │
   │                             │        + crypto.DecryptTeam/Private    │
   │                             │                                        │
  useEvents.js (EventsOn)                                               │
   ├─ ui_peer_joined  ←─────────┼── TopicPeerJoined (network → app → UI) │
   ├─ ui_peer_left    ←─────────┼── TopicPeerLeft                        │
   ├─ ui_peer_status  ←─────────┼── TopicPeerOnline / TopicPeerOffline   │
   ├─ ui_new_message  ←─────────┼── TopicMsgReceived                     │
   │                    ←────────┼── TopicPrivateMsgReceived              │
   ├─ ui_file_progress ←────────┼── TopicFileProgress                    │
   ├─ ui_file_completed ←───────┼── TopicFileCompleted                   │
   ├─ ui_conversation_updated ←─┼── TopicConvSyncRequest                 │
   └─ ui_log           ←────────┼── LogBroker callback (非 EventBus)     │
                                └─────────────────────────────────────────┘
```

## 11. 已知问题 / 注意事项

1. **`getRecentHistory` 过时代码**: `IdentityPanel.vue` 调用了 `getRecentHistory(200, 0)`, 但此方法**未在 `useBackend.js` 中导出**, 且在 Go 端 `App` 结构体中也无此方法 (当前使用 `GetConversationMessages`)。代码将在运行时抛出 ReferenceError。

2. **`PeerList.vue` 孤儿组件**: 已存在但**未挂载**, 已被 `PeerStatus.vue` 取代, 应清理。

3. **双重 IPC 路径**: `useBackend.js` 是标准封装, 但 `App.vue`, `IdentityPanel.vue`, `LogPanel.vue` 等多处绕过封装直接调用 `window.go.app.App.X`, 导致无 IPC 日志记录。

4. **`ui_conversation_updated` 不携带数据**: 这是一个明确的架构选择 — 事件触发后组件自行拉取数据, 避免事件负载过大和状态不一致。需要在文档中明确说明。

5. **HLC 排序**: 前端始终按 HLC 字符串字典序排序 (`localeCompare`), 依赖于 Go 端生成的字典序可排序格式。如果 HLC 格式变更, 前端排序将失效。