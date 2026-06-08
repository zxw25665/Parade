# Parade App 层架构设计文档

> 路径: `internal/app/`
> 范围: 业务编排核心，Wails IPC 唯一入口
> 状态: v0.2.0-libp2p

## 1. 目的（Purpose）

`internal/app` 是 Parade 项目的**业务编排核心**，也是后端五大引擎（Crypto、Database、EventBus、Network、File）唯一对外暴露的协调层。它的核心使命可归纳为四点：

1. **接口暴露**: 将 Go 端能力映射为 Wails 友好的方法，供 Vue3 前端通过 `window.go.app.App.MethodName()` 直接调用。
2. **业务编排**: 协调多个底层引擎完成复杂的多步骤流水线。典型如"发送消息"，需依次调度时钟、加密、存储、网络四个模块。
3. **异步推送**: 通过 `Frontend` 接口，把底层异步事件（新消息、节点上下线、文件进度等）实时推送到前端。
4. **时序维护**: 利用混合逻辑时钟（HLC）保证分布式网络中消息的全局因果序。

**依赖方向**:

```
Vue3 前端  ←Wails IPC→  App 层  ←持有句柄→  Crypto / DB / EventBus / Network / File
                            ↓
                    EventBus 订阅 ←── 底层引擎发布
```

App 层是 Vue3 前端**唯一**可以直接调用的 Go 包。其他所有内部模块都通过依赖注入由 App 持有，外部测试通过 mock 接口（见 `interfaces.go`）实现解耦。

---

## 2. 文件清单（File Inventory）

| 文件 | 行数 | 职责 |
|------|------|------|
| `app.go` | 1017 | `App` 结构体、30+ Wails 绑定方法、EventBus 订阅、9 个事件处理器 |
| `interfaces.go` | 39 | 三个 DI 契约: `NetworkEngine`（14 方法）、`FileEngine`（5 方法）、`Frontend`（1 方法）|
| `wails_ui.go` | 37 | `WailsUI` — 生产环境 `Frontend` 实现，封装 `runtime.EventsEmit` |
| `hlc.go` | 24 | `GenerateHLC(nodeUUID)` — 混合逻辑时钟生成器，原子计数器 `hlcCounter` 模 10000 |
| `derived_id.go` | 39 | UUIDv5 派生: `DeriveTeamConvID`、`DerivePrivateConvID`、命名空间常量 |

测试文件:

| 文件 | 包 | 说明 |
|------|------|------|
| `app_test.go` | `app` | 内部测试，MockNetwork/MockFile/MockUI，185 行 |
| `system_integration_test.go` | `app_test` | 外部包集成测试，含重启与去重验证，367 行 |
| `derived_id_test.go` | `app` | UUIDv5 派生单元测试，97 行 |

---

## 3. App 结构体（App Struct）

### 3.1 构造函数

```go
func NewApp(
    bus eventbus.EventBus,
    cry crypto.Engine,
    d   db.Database,
    net NetworkEngine,
    file FileEngine,
    ui  Frontend,
    logr logger.Logger,
) *App
```

**参数约束**: 七个参数按依赖类型分组 — 总线、加密、存储、网络、文件、UI、日志。除 `logr` 可为 `nil`（测试场景）外，其余均须非空。

### 3.2 字段

| 字段 | 类型 | 用途 |
|------|------|------|
| `ctx` | `context.Context` | Wails 启动时注入，所有 DB 调用的根上下文 |
| `evBus` | `eventbus.EventBus` | 事件总线句柄 |
| `crypto` | `crypto.Engine` | 加密引擎句柄（身份 + 队伍/私聊密钥）|
| `database` | `db.Database` | SQLite 存储句柄 |
| `netEng` | `NetworkEngine` | 网络引擎（libp2p）句柄 |
| `fileEng` | `FileEngine` | 文件引擎句柄 |
| `ui` | `Frontend` | UI 推送接口 |
| `logr` | `logger.Logger` | 日志记录器 |
| `isLoggedIn` | `bool` | 登录状态门控，控制敏感操作 |
| `subs` | `[]subscription` | 订阅句柄列表，Shutdown 时统一释放 |
| `peerJoinedAt` | `map[string]time.Time` | 节点加入事件去重时间戳（5 秒防抖）|
| `convUpdatedAt` | `map[string]time.Time` | 会话更新事件去重时间戳（3 秒防抖）|
| `peerJoinedMu` | `sync.Mutex` | 上述两个 map 的并发读写锁 |

### 3.3 内部辅助结构

```go
type subscription struct {
    topic string
    id    eventbus.SubscriptionID
}
```

订阅凭据。`Shutdown` 时遍历此切片调用 `evBus.Unsubscribe`，避免 Wails 窗口被关闭后回调仍持有 App 引用造成内存泄漏。

---

## 4. 接口契约（Interface Contracts）

依赖注入通过三个接口完成。所有生产实现位于其他包，所有测试实现为 `*MockXxx`。

### 4.1 `NetworkEngine`（14 方法）

`internal/app/interfaces.go` 定义。覆盖网络引擎的全部外部可观察行为:

| 方法 | 签名 | 用途 |
|------|------|------|
| `Start` | `(port int) error` | 启动 gRPC 监听 + mDNS 广播 |
| `Stop` | `() error` | 关闭网络引擎 |
| `BroadcastTeam` | `(payload []byte) error` | 队伍群发加密负载 |
| `UnicastPrivate` | `(targetUUID string, payload []byte) error` | 私聊单播 |
| `Peers` | `() []map[string]string` | 返回所有已知节点（pubkey, ip）|
| `StartDownload` | `(targetUUID, virtualPath, localSavePath string) error` | 触发文件下载任务 |
| `ConnectToPeer` | `(ipAddress string) (*network.PeerConnectResult, error)` | 三阶段连接测试 |
| `BrowseRemoteDirectory` | `(targetUUID, path string) ([]*pb.BrowseEntry, error)` | 浏览远端共享目录 |
| `OnForeground` | `()` | 窗口激活时回调（如刷新 mDNS）|
| `SendConvSyncRequest` | `(targetUUID, convID, sinceHLC string) error` | 发起会话同步请求 |
| `SendConvSyncResponse` | `(targetUUID, convID string, messagesJSON []byte) error` | 回复会话同步 |
| `SavePeers` | `() error` | 持久化对等节点列表 |
| `PeersWithStatus` | `() []network.PeerStatus` | 含心跳与在线状态 |
| `ResolveUUID` | `(uuid string) (string, error)` | 把 Parade UUID 解析为 Curve25519 公钥，供加密层使用 |

### 4.2 `FileEngine`（5 方法）

| 方法 | 签名 | 用途 |
|------|------|------|
| `GetVirtualTree` | `(rootPath string) (interface{}, error)` | 构建虚拟文件树 |
| `ShareDirectory` | `(absPath string) error` | 注册共享根目录 |
| `UnshareDirectory` | `(absPath string) error` | 取消共享 |
| `GetDirectoryChildren` | `(absPath string) (interface{}, error)` | 列出子节点（受共享根约束）|
| `GetSharedRoots` | `() []string` | 获取当前所有共享根路径 |

### 4.3 `Frontend`（1 方法）

```go
type Frontend interface {
    Notify(eventName string, data interface{})
}
```

**生产实现** `WailsUI`（`wails_ui.go`）: 内部用 `sync.Mutex` 保护 `ctx context.Context`，调用 `runtime.EventsEmit(ctx, name, data)` 推送事件到前端。`SetContext` 在 Wails `OnStartup` 钩子里注入。

**测试实现** `MockUI`: 记录最后一次调用的 `EventName` 和 `Payload`，断言脚本直接读取。

---

## 5. Wails 绑定 API 表面（30+ 方法）

所有方法经 Wails 反射绑定到 JS，JS 端通过 `window.go.app.App.MethodName(...)` 调用。**所有方法在入口处用 `a.log(logger.Debug, "ipc", ...)` 记录**。

### 5.1 认证与身份（Auth & Identity，3 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `CheckHasIdentity` | `() bool` | `os.Stat("./.parade_identity")` 是否存在 |
| `Register` | `(password string) error` | 创建 Curve25519 密钥对 + Argon2 加密写入文件 |
| `Login` | `(password string) error` | 解锁身份进内存；若有队伍密钥则自动 `netEng.Start(4327)` |

### 5.2 队伍（Teams，6 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `JoinTeam` | `(secret string) error` | 包装 `JoinTeamWithName("", secret)`，回退到 "Default Team" |
| `JoinTeamWithName` | `(name, secret string) (string, error)` | 从 secret 派生 teamUUID，写入 DB，启动网络，返回 teamID |
| `LeaveTeam` | `(teamID string) error` | DB 删队伍 + 清理密钥缓存 |
| `SwitchTeam` | `(teamID string) error` | 调用 `crypto.SetActiveTeam` |
| `ListTeams` | `() ([]map[string]interface{}, error)` | 列出全部队伍并标注 `active` |
| `GetActiveTeam` | `() (string, error)` | 取当前活跃 teamID |
| `GetPubKey` | `() (string, error)` | 取 base64 编码的公钥 |

### 5.3 会话与消息（Conversations，5 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `ListConversations` | `() ([]map[string]interface{}, error)` | 列出活跃队伍的全部会话 |
| `GetConversationMessages` | `(convID string, limit, offset int) ([]map[string]interface{}, error)` | 分页拉取，自动解密；私聊会话从 `conv.PeerCryptoKey` 取密钥 |
| `StartPrivateConversation` | `(peerUUID string) (string, error)` | 派生私聊 convID 并 upsert |
| `SendTeamChat` | `(text string) error` | 见第 8 节"发送消息流水线" |
| `SendPrivateChat` | `(targetUUID, text string) error` | 先 `ResolveUUID`，再走相同流水线 |

### 5.4 对等节点（Peers，3 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `GetPeers` | `() ([]map[string]string, error)` | 简单 pubkey/ip 列表 |
| `GetPeersWithStatus` | `() ([]map[string]interface{}, error)` | 含心跳与最后在线时间 |
| `ConnectToPeer` | `(ipAddress string) (map[string]interface{}, error)` | 三阶段握手，结果按阶段展开 |

### 5.5 文件与目录（Files & Directories，5 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `ShareDirectory` | `(path string) error` | 注册共享根 |
| `UnshareDirectory` | `(path string) error` | 取消共享根 |
| `GetDirectoryChildren` | `(path string) (interface{}, error)` | **路径校验**: 必须落在 `GetSharedRoots()` 之下 |
| `GetRemoteDirectoryChildren` | `(targetUUID, path string) ([]map[string]interface{}, error)` | 远端浏览，`filepath.Clean` 清洗 |
| `StartDownload` | `(targetUUID, virtualPath, localSavePath string) error` | 触发下载任务 |

### 5.6 共享组（Share Groups，6 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `CreateShareGroup` | `(name string) (string, error)` | 派生 groupID 并写 DB |
| `ListShareGroups` | `() ([]map[string]interface{}, error)` | 列出当前队伍下所有组 |
| `AddDirectoryToShareGroup` | `(groupID, dirPath string) error` | 关联目录到组 |
| `RemoveDirectoryFromShareGroup` | `(groupID, dirPath string) error` | 解除关联 |
| `DeleteShareGroup` | `(groupID string) error` | 删除组 |
| `GetShareGroupDirs` | `(groupID string) ([]map[string]interface{}, error)` | 列出组内目录 |

### 5.7 工具方法（Utility，4 个）

| 方法 | 签名 | 行为 |
|------|------|------|
| `GetDefaultDownloadDir` | `() (string, error)` | 返回 `~/Downloads`，不存在则创建 |
| `OnForeground` | `()` | 透传到 `netEng.OnForeground()` |
| `ExportLogs` | `() (map[string]interface{}, error)` | 从 `LogBroker.Entries()` 导出 JSON |
| `WriteLogFile` | `(filePath, content string) error` | 写文件到磁盘（用户保存日志用）|

### 5.8 生命周期（Lifecycle，3 个）

以下方法**不绑定到 JS**，由 Wails 框架直接调用:

| 方法 | 签名 | 触发时机 |
|------|------|------|
| `Startup` | `(ctx context.Context)` | Wails `OnStartup` 钩子；注册订阅、注入 ctx、设置日志回调 |
| `GetContext` | `() context.Context` | 单实例锁中唤醒已存在窗口时使用 |
| `Shutdown` | `()` | Wails `OnShutdown` 钩子；持久化 + 取消订阅 |

---

## 6. 初始化序列（Initialization Sequence）

初始化流程在 `main.go` 完成，按以下顺序串联:

```
┌────────────────────────────────────────────────────────────────────────┐
│ Step 1  eventBus := eventbus.New()                                     │
│         · 默认 handler 超时 5s                                         │
│         · 每个 topic 独立 goroutine + 256 缓冲 channel                 │
├────────────────────────────────────────────────────────────────────────┤
│ Step 2  cry := crypto.NewEngine()                                      │
│         · Curve25519 + Argon2 身份管理                                  │
├────────────────────────────────────────────────────────────────────────┤
│ Step 3  database, _ := db.NewSQLiteDB("./.parade_data.db")             │
│         · WAL 模式，64MB cache，busy_timeout 5000ms                     │
├────────────────────────────────────────────────────────────────────────┤
│ Step 4  logBroker, _ := logger.NewLogBroker("./.parade.log", 5000)     │
│         · 环形缓冲 5000 条日志                                         │
├────────────────────────────────────────────────────────────────────────┤
│ Step 5  fileEngine := file.NewEngine().                                │
│             WithDatabase(database).WithEventBus(eventBus).             │
│             WithLogger(logBroker)                                      │
│         · 流式 builder 模式                                            │
│         · 立即调用 fileEngine.LoadSharedDirectories()                  │
├────────────────────────────────────────────────────────────────────────┤
│ Step 6  netEngine := network.NewLibp2pEngine(eventBus, cry, logBroker) │
│         netEngine.AttachFileEngine(fileEngine)                         │
│         · libp2p host 尚未启动（等 JoinTeam/Start(4327)）              │
├────────────────────────────────────────────────────────────────────────┤
│ Step 7  wailsUI := app.NewWailsUI()                                     │
│         · 此时 ctx 仍为 nil                                            │
├────────────────────────────────────────────────────────────────────────┤
│ Step 8  appInstance = app.NewApp(                                       │
│             bus, cry, database, netEngine, fileEngine, wailsUI, logBr) │
├────────────────────────────────────────────────────────────────────────┤
│ Step 9  wails.Run({                                                    │
│             OnStartup:   wailsUI.SetContext + appInstance.Startup     │
│             OnShutdown:  appInstance.Shutdown                          │
│             Bind:        [appInstance]                                 │
│             SingleInstanceLock: { ... }  // 单实例锁                   │
│         })                                                             │
└────────────────────────────────────────────────────────────────────────┘
```

**关键点**:
- 网络引擎 `Start(4327)` 不会在 `NewApp` 时调用，而是延迟到 `Login`（恢复场景）或 `JoinTeam`（首启场景）。
- 单实例锁防止用户多次启动应用，第二次启动会通过 `appInstance.GetContext()` 唤醒原窗口。

---

## 7. EventBus 接线（EventBus Wiring — 9 个主题）

`App.registerEventSubscribers()` 在 `Startup` 时调用，注册 9 个主题订阅。所有订阅通过 `a.subscribe(topic, handler)` 加入跟踪表，`Shutdown` 时统一释放。

### 7.1 主题与处理器映射

| Topic | 处理器职责 | 推送事件 |
|-------|-----------|---------|
| `TopicPeerJoined` | 5 秒防抖 → 后台 goroutine: `StartPrivateConversation` + `syncAllConversationsWithPeer` | `ui_peer_joined` |
| `TopicPeerLeft` | 仅日志 | `ui_peer_left` |
| `TopicMsgReceived` | 自发自收过滤（`SenderID == 本节点`）→ `EncryptTeam` 二次加密落库 → `UpsertConversation` → `UpdateConversationLastHLC` → 推 `ui_new_message`（明文）| `ui_new_message`（明文）|
| `TopicPrivateMsgReceived` | 自发自收过滤 → `ResolveUUID` 拿公钥 → `EncryptPrivate` → `ensureConversation` + 落库 | `ui_new_message`（明文）|
| `TopicFileProgress` | 日志 | `ui_file_progress` |
| `TopicFileCompleted` | 日志 | `ui_file_completed` |
| `TopicPeerOnline` | 推 `ui_peer_status: online` + 后台 `syncAllConversationsWithPeer` | `ui_peer_status: online` |
| `TopicPeerOffline` | 推 `ui_peer_status: offline` | `ui_peer_status: offline` |
| `TopicConvSyncRequest` | **双分支**: ① `payload.Messages != nil` → 事务里批量 `InsertMessageTx` + 3 秒防抖推 `ui_conversation_updated`；② 否则用 `GetConversationMessagesSinceHLC` 查询并 `SendConvSyncResponse` 回包 | `ui_conversation_updated` |

### 7.2 特殊类型 99 旁路

`TopicMsgReceived` 处理器对 `Type == 99` 的消息**跳过 DB 持久化**，直接以 `[握手测试]` 前缀推送 `ui_new_message`，用于 mDNS/连接握手时的临时探测。

### 7.3 日志回调（独立于 EventBus）

`Startup` 中检查 `a.logr` 是否为 `*logger.LogBroker`，若是则注册回调:

```go
broker.SetCallback(func(entry logger.LogEntry) {
    a.ui.Notify("ui_log", map[string]interface{}{
        "time":    entry.Timestamp.Format("15:04:05.000"),
        "level":   int(entry.Level),
        "source":  entry.Source,
        "message": entry.Message,
    })
})
```

每条日志 → `ui.Notify("ui_log", ...)`。这与 EventBus 主题 `TopicLogEvent` 是两条独立路径。

### 7.4 完整数据流

```
Network Engine  ──publish──→  EventBus  ──dispatch──→  App 订阅处理
                                                          ↓
                                                    ui.Notify(...)
                                                          ↓
                                                  WailsUI.SetContext
                                                          ↓
                                                runtime.EventsEmit
                                                          ↓
                                                       Vue3 前端
```

---

## 8. 发送消息流水线（Send-Message Pipeline）

这是 App 层**最核心的业务流**。`SendTeamChat` 和 `SendPrivateChat` 复用同一个内部函数 `sendConversationMessage`，仅在加密函数和网络发送函数上有差异。

### 8.1 十二步流程

```
┌──────────────────────────────────────────────────────────────────────┐
│  1. myUUID  := a.crypto.GetPersonalUUID()                            │
│     取本节点 Parade UUID                                              │
├──────────────────────────────────────────────────────────────────────┤
│  2. hlc     := GenerateHLC(myUUID)                                   │
│     格式: 2006-01-02T15:04:05.000Z_0001_<NodeID8>                     │
│     计数器: 原子自增 hlcCounter % 10000                               │
├──────────────────────────────────────────────────────────────────────┤
│  3. raw     := []byte(text)                                          │
│     明文负载                                                         │
├──────────────────────────────────────────────────────────────────────┤
│  4. teamID  := a.crypto.GetActiveTeam()                              │
│     取当前活跃队伍                                                   │
├──────────────────────────────────────────────────────────────────────┤
│  5. enc     := encryptFn(raw)                                        │
│     群聊: a.crypto.EncryptTeam(raw)                                  │
│     私聊: a.crypto.EncryptPrivate(raw, peerPubkey)                   │
│     ⚠ 第一次加密：用于本地存储                                        │
├──────────────────────────────────────────────────────────────────────┤
│  6. msgID   := uuid.New().String()                                   │
│     生成消息唯一 ID                                                  │
├──────────────────────────────────────────────────────────────────────┤
│  7. db.InsertMessage(ctx, &db.Message{                               │
│        ID, HLC, SenderID, Content: enc,                              │
│        TeamID, ConversationID, CreatedAt: now                        │
│     })                                                               │
│     落库（密文）                                                     │
├──────────────────────────────────────────────────────────────────────┤
│  8. db.UpdateConversationLastHLC(ctx, convID, hlc)                    │
│     更新会话最大 HLC 游标                                            │
├──────────────────────────────────────────────────────────────────────┤
│  9. ui.Notify("ui_new_message", { id, hlc, sender, content: text,    │
│                                  timestamp })                        │
│     ⚠ 推给前端的是明文（text），不是 enc                              │
│     本地回显                                                         │
├──────────────────────────────────────────────────────────────────────┤
│ 10. netPayload := json.Marshal(eventbus.MsgReceivedPayload{          │
│         HLC, SenderID, Content: raw,                                 │
│         Type: 0,                                                     │
│         TeamID, ConversationID,                                      │
│         SenderIP:       getLocalIP(),                                │
│         SenderPubKey:   a.crypto.GetPublicKeyBase64()                │
│     })                                                               │
│     构造网络负载（明文打包）                                          │
├──────────────────────────────────────────────────────────────────────┤
│ 11. encrypted := encryptFn(netPayload)                               │
│     群聊: EncryptTeam                                                 │
│     私聊: EncryptPrivate                                              │
│     ⚠ 第二次加密：用于网络传输                                        │
├──────────────────────────────────────────────────────────────────────┤
│ 12. sendFn(encrypted)                                                │
│     群聊: netEng.BroadcastTeam(encrypted)                            │
│     私聊: netEng.UnicastPrivate(targetUUID, encrypted)                │
│     写线                                                             │
└──────────────────────────────────────────────────────────────────────┘
```

### 8.2 双加密设计要点

| 用途 | 加密对象 | 密钥 |
|------|---------|------|
| 第一次（DB） | `raw` | 队伍对称密钥 / Curve25519 共享密钥 |
| 第二次（网络） | 完整的 `MsgReceivedPayload` JSON | 同上 |

这样设计的好处:
- DB 密文独立，索引查询（HLC/ConvID）不依赖负载结构
- 网络密文包含 SenderIP/SenderPubKey 等元信息，接收方先解密 JSON 再分发到 EventBus

### 8.3 私聊流程的差异

`SendPrivateChat` 在调用 `sendConversationMessage` 之前多做三步:

1. `netEng.ResolveUUID(targetUUID)` — 把 Parade UUID 解析为 Curve25519 公钥
2. `DerivePrivateConvID(myUUID, targetUUID)` — 派生会话 ID（交换律）
3. `ensureConversation(convID, "private", targetUUID, pubkey)` — 写入本地会话表

`sendConversationMessage` 内部用闭包注入私聊特有的加密和发送函数:

```go
a.sendConversationMessage(convID, text,
    func(payload []byte) ([]byte, error) {
        return a.crypto.EncryptPrivate(payload, pubkey)
    },
    func(payload []byte) error {
        return a.netEng.UnicastPrivate(targetUUID, payload)
    },
)
```

---

## 9. 关键设计观察（Key Design Observations）

### 9.1 确定性 ConvID 派生

`derived_id.go` 用 UUIDv5（SHA-256）从命名空间 UUID + 哈希输入派生会话 ID:

```go
DeriveTeamConvID(teamID) →
    sha256("team:" + teamID) → uuid.NewHash(conversationNS, hash, 5)

DerivePrivateConvID(myUUID, peerUUID) →
    sort(myUUID, peerUUID) → sha256("private:" + a + ":" + b) → uuid.NewHash(conversationNS, hash, 5)
```

**交换律**: 无论 `DerivePrivateConvID(A, B)` 还是 `DerivePrivateConvID(B, A)`，结果相同。这保证不同节点独立计算也能收敛到同一个会话 ID。

**命名空间隔离**: `identityNS`、`teamNS`、`shareGroupNS`、`conversationNS` 使用 RFC 4122 标准的四个连续命名空间 UUID，确保不同实体类型不会碰撞。

### 9.2 防御性路径校验

`GetDirectoryChildren` 在转发到 File 引擎前强制做前缀校验:

```go
cleanPath := filepath.Clean(path)
sharedRoots := a.fileEng.GetSharedRoots()
allowed := false
for _, root := range sharedRoots {
    if strings.HasPrefix(cleanPath, root+string(os.PathSeparator)) || cleanPath == root {
        allowed = true
        break
    }
}
if !allowed {
    return nil, fmt.Errorf("path %s is not within any shared directory", cleanPath)
}
```

这防止前端（即使是合法 Vue 组件）误传越界路径。

### 9.3 三道防抖

| 防抖对象 | 阈值 | 目的 |
|---------|------|------|
| `peerJoinedAt[peerUUID]` | 5 秒 | 防止节点 mDNS 反复 announce 时重复触发 `StartPrivateConversation` |
| `convUpdatedAt[convID]` | 3 秒 | 同步响应大量涌入时，避免前端收到洪水般的 `ui_conversation_updated` |
| `isLoggedIn` 门控 | 永久 | 所有需要解密能力的 IPC 都先 `checkLoggedIn()`，未登录直接拒绝 |

### 9.4 订阅清理

`Shutdown` 顺序:

1. `netEng.SavePeers()` — 持久化节点列表
2. `crypto.SaveTeamKeys()` — 持久化队伍密钥
3. 遍历 `a.subs`，逐个 `evBus.Unsubscribe(topic, id)` — 切断回调链
4. `a.subs = nil` — 释放切片

若 Wails 窗口被关闭但订阅未释放，App 仍会被事件总线强引用，阻碍 GC。

### 9.5 一致的 IPC 日志

所有 Wails 绑定方法在入口处用 `a.log(logger.Debug, "ipc", ...)` 输出，错误时升级为 `Warning` 或 `Error`。Source tag 统一为 `ipc`，方便日志聚合时按来源过滤。

---

## 10. 测试约定（Test Conventions）

### 10.1 双包测试策略

| 文件 | 包 | 视角 | 用途 |
|------|------|------|------|
| `app_test.go` | `app` | 内部 | 直接访问 `a.crypto`、`a.database` 等私有字段 |
| `system_integration_test.go` | `app_test` | 外部 | 强制使用公共 API，模拟真实调用 |
| `derived_id_test.go` | `app` | 内部 | 纯函数测试，不需要 mock |

### 10.2 Mock 实现模式

三个 Mock 都集中在测试文件顶部:

```go
type MockNetwork struct {
    LastPayload []byte
}
// 实现所有 14 个 NetworkEngine 方法
// BroadcastTeam 时保存 payload 用于断言

type MockFile struct{}
// 空实现，仅满足接口

type MockUI struct {
    EventName string
    Payload   interface{}
}
// Notify 时记录最后一次调用的字段
```

### 10.3 标准 setup 流程

`app_test.go` 中的 `setup(t)` 工厂:

```go
func setup(t *testing.T) (*App, *MockNetwork, *MockUI, func()) {
    dbP, idP := "./test.db", "./test.id"
    _ = os.Remove(dbP)  // 隔离
    _ = os.Remove(idP)

    eb   := eventbus.New()
    cr   := crypto.NewEngine()
    d, _ := db.NewSQLiteDB(dbP)
    net  := &MockNetwork{}
    file := &MockFile{}
    ui   := &MockUI{}

    app := NewApp(eb, cr, d, net, file, ui, nil)
    app.Startup(context.Background())

    return app, net, ui, func() {
        d.Close()
        os.Remove(dbP)
        os.Remove(idP)
    }
}
```

### 10.4 业务测试模板

任何 `TestApp_X` 测试都遵循 "Register → Login → JoinTeam → 操作" 四步:

```go
func TestApp_FullFlow(t *testing.T) {
    a, net, ui, cleanup := setup(t)
    defer cleanup()

    _ = a.Register("123")
    _ = a.Login("123")
    _ = a.JoinTeam("team")

    _ = a.SendTeamChat("Hello World")

    // 断言 DB 内容
    hist, _ := a.GetConversationMessages(DeriveTeamConvID(a.crypto.GetActiveTeam()), 1, 0)
    if hist[0]["content"] != "Hello World" { ... }

    // 断言网络负载
    dec, _ := a.crypto.DecryptTeam(net.LastPayload)
    var netPayload eventbus.MsgReceivedPayload
    _ = json.Unmarshal(dec, &netPayload)
    if string(netPayload.Content) != "Hello World" { ... }

    // 模拟远端消息
    a.evBus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{...})
    time.Sleep(100 * time.Millisecond)  // 等待异步分发

    // 断言 UI 推送
    if ui.EventName != "ui_new_message" { ... }
}
```

### 10.5 集成测试场景

`system_integration_test.go` 用外部包 `app_test` 视角，覆盖四个关键场景:

1. **`TestSystem_CompleteUserFlow`** — 注册、登录、加入队伍、发消息、收消息、关闭 DB、重启、验证消息恢复
2. **`TestSystem_JoinTeamReusesUUID`** — 用相同 secret 重复加入，应派生相同 teamUUID
3. **`TestSystem_LoginAutoStartsNetwork`** — 重启后 `Login` 检测到存在队伍密钥时自动 `Start(4327)`
4. **`TestSystem_NoDuplicateOnSelfSender`** — 自发自收不会因 EventBus 重投导致重复入库

---

## 11. 关键依赖常量

| 常量 | 值 | 出处 | 用途 |
|------|------|------|------|
| `IdentityFile` | `"./.parade_identity"` | `app.go:24` | 身份文件路径 |
| `hlcCounter` | `uint32` 全局原子 | `hlc.go:10` | HLC 毫秒内排序 |
| `identityNS` | `6ba7b811-...` | `derived_id.go:12` | UUIDv5 命名空间 |
| `teamNS` | `6ba7b812-...` | `derived_id.go:13` | 队伍 ID 命名空间 |
| `shareGroupNS` | `6ba7b814-...` | `derived_id.go:14` | 共享组命名空间 |
| `conversationNS` | `6ba7b815-...` | `derived_id.go:15` | 会话 ID 命名空间 |
| `db.ReceiverIDGroupChat` | `""` | `db` 包 | 群聊接收方 ID 哨兵值 |

---

## 12. 故障排查速查

| 现象 | 优先检查点 |
|------|-----------|
| 前端收不到 `ui_new_message` | Wails 启动顺序: `SetContext` 是否在 `Startup` 之前？`MockUI` 替换时是否被旧 `*App` 持有？ |
| `SendTeamChat` 报 "not logged in" | `Login` 之前调用？`Register` 落盘失败导致 `Login` 未设置 `isLoggedIn = true`？ |
| ConvID 双方不一致 | `DerivePrivateConvID` 双方 UUID 顺序无关，teamID 必须 sha256 派生，是否被手工篡改？ |
| 私聊解密失败 | `conv.PeerCryptoKey` 是否被填充？`ResolveUUID` 是否能查到对方公钥？ |
| 节点反复 join 不触发同步 | 检查 `peerJoinedAt[peerUUID]` 时间戳，5 秒窗口内第二次 join 会被防抖丢弃 |
| 同步响应淹死前端 | `convUpdatedAt` 防抖正常工作？批量 `InsertMessageTx` 是否在事务里？ |
| Shutdown 后事件仍触发 | `a.subs` 切片是否被正确填充？`subscribe()` 是否被绕过？ |
