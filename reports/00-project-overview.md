# Parade (游行) — 项目总览设计文档

> Version: `v0.2.0-libp2p` · Module: `parade` · Go: `1.26.1`
> Status: 综合设计与架构索引文档 · Last updated: 2026-06-08

---

## 1. 项目身份 / Project Identity

| 字段 | 值 |
| :--- | :--- |
| 名称 | **Parade** (中文：游行) |
| 版本 | `v0.2.0-libp2p` |
| Go 模块名 | `parade` |
| Go 工具链 | `1.26.1` |
| 形态 | 桌面端单二进制应用 (Wails v2 打包) |
| 定位 | 局域网去中心化协作工具,集成 **E2E 加密群聊 / 私聊 / 文件共享** |
| 核心特征 | 零中心服务器、纯局域网、端到端加密、消息与文件双通道 |

**设计理念** : 在无互联网、无中心服务器的局域网环境(教室、临时会议、跨网段小团队)中,提供一种**可验证、可审计、可离线工作**的协作工具,所有数据既不离开本地,也不依赖任何云端。

---

## 2. 技术栈 / Technology Stack

### 2.1 桌面壳与运行时

| 组件 | 版本 | 作用 |
| :--- | :--- | :--- |
| Wails v2 | `2.12.0` | Go ↔ JS 桥接 + Webview 容器 |
| Webview | (Wails 内置) | 渲染 Vue3 界面 |
| 嵌入 FS | `embed.FS` | 打包 `frontend/dist` 资源 |

### 2.2 后端 (Go)

| 维度 | 数据 |
| :--- | :--- |
| 源文件数 | 32 个 (含 `proto/`) + 7 个测试文件 + 1 个 `proto/chat.pb.go` 陈旧副本 |
| 总行数 | 约 **10,852** 行 (含 `pb/` 生成代码) ;手写源约 **6,932** 行 |
| `app.go` | 1,017 行业务编排主文件 |

### 2.3 前端 (Vue 3)

| 依赖 | 版本 | 说明 |
| :--- | :--- | :--- |
| Vue | `3.5.13` | Composition API + `<script setup>` |
| Vite | `6.3.1` | 构建工具 |
| vue-i18n | `10.0.8` | 多语言 (中/英) |
| 状态管理 | **无 Pinia**,改用 **单例响应式 composable store** |
| 路由 | **无 vue-router**,固定三栏式外壳布局 |
| 组件数 | 11 个 `.vue` 文件,约 1,556 行 |
| 目录 | `frontend/src/{components, composables, i18n, lib}` |

### 2.4 P2P 网络层

| 组件 | 版本 | 作用 |
| :--- | :--- | :--- |
| libp2p | `v0.48.0` | 节点抽象 + 多路复用 + 身份 |
| go-libp2p-pubsub | `v0.16.0` | **GossipSub** 群组消息 |
| 多地址 | `multiformats/go-multiaddr v0.16.1` | multiaddr 编码 |
| gRPC | `google.golang.org/grpc v1.80.0` | **仅作为 protobuf 依赖**,`chat.proto` / `file.proto` 的运行时由 libp2p 自管 |
| Protobuf | `google.golang.org/protobuf v1.36.11` | IDL 与生成 |

**传输层** : `TCP` + `Noise` (握手加密) + `Yamux` (流复用)

### 2.5 持久化与加密

| 关注点 | 技术 | 版本 / 配置 |
| :--- | :--- | :--- |
| SQLite 驱动 | `modernc.org/sqlite` | `v1.48.2` (纯 Go,**无 CGO**) |
| 日志模式 | WAL | `cache=64MB`,`busy_timeout=5000ms` |
| 迁移 | 10 个版本,8 张表 | `messages` / `teams` / `channels` / `channel_members` / `conversations` / `file_logs` / `share_groups` / `share_group_dirs` / `shared_directories` / `schema_meta` |
| 加密套件 | `golang.org/x/crypto v0.50.0` | Argon2id (KDF) + Curve25519 (ECDH) |
| 对称加密 | AES-256-GCM | 落盘 + 队伍消息 + 私聊双重加密 |
| 文件哈希 | `zeebo/blake3 v0.2.4` | BLAKE3 |
| 文件监听 | `fsnotify v1.9.0` | 共享目录变更检测 |
| UUID | `google/uuid v1.6.0` | 实体 ID、订阅凭据、UUID v5 派生 |

---

## 3. 仓库布局 / Repository Layout

```
Parade/
├── main.go                       # 入口:装配五大引擎 → wails.Run
├── go.mod / go.sum               # Go 1.26.1, module parade
├── wails.json                    # Wails 构建配置
├── .gitattributes / .gitignore   # LF/CRLF 策略 + test*.db 忽略
│
├── proto/                        # Protobuf 源 (3 文件)
│   ├── chat.proto                #   ChatService 3 RPCs (parade.chat.v1)
│   ├── file.proto                #   FileTransferService 3 RPCs (parade.file.v1)
│   └── chat.pb.go                #   ⚠ STALE DUPLICATE,已迁移到 internal/network/pb/chatpb/
│
├── internal/
│   ├── app/                      # 业务编排层 (Wails 绑定)
│   │   ├── app.go                #   1,017 行 — Register/Login/JoinTeam/SendTeamChat 等
│   │   ├── interfaces.go         #   NetworkEngine / FileEngine / Frontend
│   │   ├── hlc.go                #   HLC 生成器 (24 行)
│   │   ├── derived_id.go         #   UUID v5 派生 (39 行)
│   │   ├── wails_ui.go           #   Wails runtime.EventsEmit 包装
│   │   └── * _test.go            #   7 个测试文件 (含集成测试)
│   │
│   ├── core/
│   │   ├── eventbus/             # 异步 pub/sub,256 容量 channel
│   │   │   ├── eventbus.go       #   内存总线,每 topic 一条独立 goroutine,FIFO
│   │   │   ├── topics.go         #   13 个 Topic 常量 + 4 个 Payload 类型
│   │   │   └── eventbus_test.go
│   │   ├── crypto/               # 身份 + AES-GCM + Curve25519
│   │   │   ├── interface.go      #   Engine 接口 (46 行)
│   │   │   ├── keystore.go       #   Argon2id 加密的 .parade_identity
│   │   │   ├── cipher.go         #   三层加密:Local / Team / Private
│   │   │   └── crypto_test.go
│   │   ├── db/                   # SQLite 持久化
│   │   │   ├── interface.go      #   Database / DBTx 接口
│   │   │   ├── models.go         #   Message / FileLog 结构
│   │   │   ├── sqlite.go         #   10 个迁移,事务 API
│   │   │   └── sqlite_test.go
│   │   └── logger/               # 异步 JSONL 日志代理 (⚠ AGENTS.md 未记录)
│   │       ├── broker.go         #   环形缓冲 + 文件追加
│   │       ├── logger.go         #   等级 / 结构化日志
│   │       └── broker_test.go
│   │
│   ├── network/                  # libp2p P2P 层 (⚠ 实际为 libp2p,非 gRPC)
│   │   ├── libp2p_engine.go      #   引擎主结构 + 生命周期 (592 行)
│   │   ├── libp2p_host.go        #   libp2p Host (TCP+Noise+Yamux)
│   │   ├── libp2p_connect.go     #   3 阶段握手 (185 行)
│   │   ├── libp2p_chat.go        #   GossipSub 群聊 + 私聊流
│   │   ├── libp2p_file.go        #   文件元数据 / 下载 / 浏览
│   │   ├── libp2p_sync.go        #   会话增量同步
│   │   ├── interfaces.go         #   FileTransferEngine
│   │   ├── types.go              #   PeerConnectResult / PhaseResult
│   │   ├── README.md             #   ⚠ 文档陈旧,仍指 grpc_*.go
│   │   ├── FILE_TRANSFER_HANDOFF.md
│   │   └── pb/                   #   生成代码
│   │       ├── chatpb/           #     chat.proto → chatpb
│   │       │   ├── chat.pb.go
│   │       │   └── chat_grpc.pb.go
│   │       ├── file.pb.go        #     file.proto → pb
│   │       └── file_grpc.pb.go
│   │
│   └── file/                     # 虚拟文件树 + 分块 I/O
│       ├── vfs.go                #   FileNode 树 + fsnotify (519 行)
│       ├── chunk.go              #   2MB chunk reader + sync.Pool
│       ├── hash.go               #   BLAKE3 + 路径/size/mtime 缓存
│       ├── chunk_tracker.go      #   位图 OOO 跟踪 (243 行)
│       ├── transfer.go           #   断点续传 + 原子重命名
│       ├── README.md
│       └── file_test.go
│
├── frontend/                     # 独立 Vue3 工作空间
│   ├── package.json              #   vue 3.5.13 / vite 6.3.1 / vue-i18n 10.0.8
│   └── src/
│       ├── main.js               #   入口 (9 行)
│       ├── App.vue               #   三栏外壳
│       ├── components/           #   11 个 .vue 组件
│       │   ├── ChatPanel.vue              # 276
│       │   ├── FileBrowser.vue            # 315
│       │   ├── ConversationList.vue       # 153
│       │   ├── PeerList.vue               # 142
│       │   ├── PeerStatus.vue             # 147
│       │   ├── LogPanel.vue               # 115
│       │   ├── IdentityPanel.vue          #  94
│       │   ├── TeamPanel.vue              #  87
│       │   ├── DownloadList.vue           #  55
│       │   ├── LanguageToggle.vue         #  46
│       │   └── CollapsibleSection.vue     #  22
│       ├── composables/          #   useStore / useEvents / useBackend / useLogStore
│       ├── i18n/                 #   多语言 (含 locales/)
│       └── lib/wailsjs/          #   Wails 自动生成 IPC 绑定
│
├── build/                        # Wails 编译产物
├── logsfordebug/                 # 调试日志
├── reports/                      # 📍 本文档所在
├── .parade_data.db               # 运行时 SQLite (WAL)
├── .parade.log                   # 运行时 JSONL 日志
└── .parade_peers                 # 已发现对端列表 (网络层)
```

---

## 4. 架构总览 / Five-Engine Wiring

`main.go` 严格按以下顺序装配五大引擎,**不可重排**——后续引擎依赖前面注入的依赖。

```text
main.go (line 39)
 │
 ├── 1. eventBus := eventbus.New()                  [无依赖]
 │       └─ 内存总线,每 topic 一条 256 容量 channel + goroutine
 │
 ├── 2. cry := crypto.NewEngine()                   [无依赖]
 │       └─ Curve25519 身份 + Argon2id KDF 框架
 │
 ├── 3. database, _ := db.NewSQLiteDB("./.parade_data.db")
 │       └─ modernc.org/sqlite,WAL 模式,busy_timeout=5s
 │
 ├── 4. logBroker, _ := logger.NewLogBroker("./.parade.log", 5000)
 │       └─ 5000 条环形缓冲,异步 JSONL 落盘
 │
 ├── 5. fileEngine := file.NewEngine()
 │         .WithDatabase(database)
 │         .WithEventBus(eventBus)
 │         .WithLogger(logBroker)
 │      → fileEngine.LoadSharedDirectories()        [阻塞,恢复共享根]
 │      → defer fileEngine.Close()
 │
 ├── 6. netEngine := network.NewLibp2pEngine(eventBus, cry, logBroker)
 │      → netEngine.AttachFileEngine(fileEngine)    [后置注入,打破循环]
 │      → defer netEngine.Stop()
 │
 ├── 7. wailsUI := app.NewWailsUI()
 │      └─ 持有 wails.Context,负责 Notify → runtime.EventsEmit
 │
 ├── 8. appInstance = app.NewApp(
 │         eventBus, cry, database,
 │         netEngine, fileEngine,
 │         wailsUI, logBroker,
 │      )
 │
 └── 9. wails.Run(&options.App{
          Title:  "Parade v0.2.0-libp2p",
          Width:  1024, Height: 768,
          AssetServer:  &assetserver.Options{Assets: assets},
          OnStartup:    wailsUI.SetContext + appInstance.Startup,
          OnShutdown:   appInstance.Shutdown,
          SingleInstanceLock: {UniqueId: "com.parade.app-7f3a9c2e"},
          Bind:         []interface{}{ appInstance },  // ⚡ 暴露给 JS
      })
```

**关键设计点**

- `fileEngine` 必须在 `netEngine` 之前构造,因为 `netEngine.AttachFileEngine` 注入后,n 才能反向查询 `file.Engine`。
- `wailsUI.SetContext(ctx)` 在 `OnStartup` 回调中执行,确保推送通道在 Webview 就绪后启用。
- `defer` 顺序遵循 LIFO:DB → LogBroker → File → Network,保证 Wails 关闭后底层资源按正确顺序释放。
- 单一实例锁 `com.parade.app-7f3a9c2e` 防止用户重复打开 Parade。

---

## 5. 运行时数据流 / Runtime Data Flow

### 5.1 调用方向 (Vue → Go)

```text
┌──────────────┐  Wails IPC (反射绑定)  ┌──────────────────────┐
│  Vue3 Frontend│ ─────────────────────▶ │  app.App (Bind target)│
│              │  window.go.app.App.X()  │   ├─ 业务编排         │
│              │                         │   ├─ 调用 engines     │
└──────────────┘                         │   └─ 写 DB / 加密     │
                                        └──────────┬───────────┘
                                                   │
                                ┌──────────────────┼──────────────────┐
                                ▼                  ▼                  ▼
                          eventBus.Publish   crypto.Encrypt*    db.InsertMessage
                                │                  │                  │
                                ▼                  ▼                  ▼
                         (异步事件流)        (网络出站前)        (本地落盘)
```

### 5.2 推送方向 (Go → Vue)

```text
┌─────────────┐  Publish()  ┌─────────────┐  Subscribe()  ┌────────────┐
│ file / net  │ ─────────▶ │ EventBus    │ ────────────▶ │  app.App   │
│ (发布方)    │             │ (channel)   │               │ (订阅方)   │
└─────────────┘             └─────────────┘               └─────┬──────┘
                                                                │ 解析后
                                                                ▼
                                                        WailsUI.Notify()
                                                                │
                                                                ▼
                                                  runtime.EventsEmit(ctx, name, data)
                                                                │
                                                                ▼
                                              ┌──────────────────────────┐
                                              │ Vue: EventsOn(name, cb)  │
                                              │ → reactive store 刷新    │
                                              └──────────────────────────┘
```

### 5.3 典型事件链：发送一条队伍消息

```text
1. 用户在 ChatPanel 输入 "hello"
2. frontend → appInstance.SendTeamChat("hello")
3. app.App:
   a. hlc = GenerateHLC(myUUID)                  // hlc.go
   b. ciphertext, _ = cry.EncryptLocal([]byte)   // 落盘加密
   c. db.InsertMessage({HLC, SenderID, Content: ciphertext, ...})
   d. env = buildEnvelope(plaintext, sig, Type=0)
   e. payload = cry.EncryptTeam(env.Payload)     // 队伍加密
   f. netEngine.BroadcastTeam(payload)           // GossipSub publish
4. 本节点的 GossipSub 收到自己消息 (loopback) → 触发 TopicMsgReceived
5. app.App 订阅 TopicMsgReceived → 解析 → WailsUI.Notify("ui_new_message", msg)
6. Vue 端 useEvents 监听到事件 → store 新增 → ChatPanel 渲染
```

---

## 6. 模块依赖图 / Module Dependency Graph

```text
                            ┌────────────┐
                            │   main.go  │
                            └─────┬──────┘
                                  │ wires all
            ┌─────────────────────┼─────────────────────────┐
            ▼                     ▼                         ▼
      ┌──────────┐         ┌──────────────┐         ┌──────────────┐
      │ eventbus │         │   logger     │         │     db       │
      │  (无依赖)│         │ (无外部依赖) │         │ (无内部依赖) │
      └────┬─────┘         └──────┬───────┘         └──────┬───────┘
           │                      │                       │
           │ Publish/Subscribe    │ 注入 logger.Logger    │ 注入 db.Database
           │                      │                       │
           ▼                      ▼                       ▼
  ┌─────────────────────────────────────────────────────────────┐
  │         ┌──────────┐         ┌──────────┐                   │
  │         │  crypto  │         │   file   │                   │
  │         │ (无依赖) │         │(db,bus)  │                   │
  │         └────┬─────┘         └────┬─────┘                   │
  │              │                    │                         │
  │              │ cry.Encrypt*       │ fileEngine 接口          │
  │              ▼                    ▼                         │
  │         ┌─────────────────────────────────┐                │
  │         │           network (libp2p)      │                │
  │         │  deps: bus, cry, logBroker      │                │
  │         │  + post-attach: fileEngine      │                │
  │         └────────────────┬────────────────┘                │
  │                          │ NetworkEngine 接口              │
  │                          ▼                                 │
  │                   ┌────────────┐                           │
  │                   │   app.App  │ ─── 持有全部 5 引擎 ──── │
  │                   │  Wails Bind │                          │
  │                   └──────┬─────┘                           │
  │                          │                                 │
  └──────────────────────────┼─────────────────────────────────┘
                             │ runtime.EventsEmit (Go → JS)
                             ▼
                       ┌──────────────┐
                       │  Vue3 + Wails│
                       │   frontend   │
                       └──────────────┘
```

### 6.1 依赖矩阵

| 导入方 ＼ 被依赖方 | eventbus | crypto | db | logger | file | network | app |
| :--- | :-: | :-: | :-: | :-: | :-: | :-: | :-: |
| **eventbus** | — | · | · | · | · | · | · |
| **crypto** | · | — | · | · | · | · | · |
| **db** | · | · | — | · | · | · | · |
| **logger** | · | · | · | — | · | · | · |
| **file** | ✓ | · | ✓ | ✓ | — | · | · |
| **network** | ✓ | ✓ | · | ✓ | ✓(post) | — | · |
| **app** | ✓ | ✓ | ✓ | ✓ | ✓(interface) | ✓(interface) | — |
| **main** | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

✓ = 直接 import,· = 不依赖

### 6.2 接口反转避免循环

`network` → `file` 的耦合通过 **后置 attach** 解决:

```go
// network.NewLibp2pEngine(bus, cry, logBroker)  // 构造时不需 file
// netEngine.AttachFileEngine(fileEngine)        // 主流程中后置注入
```

`app` 持有 `NetworkEngine` / `FileEngine` / `Frontend` 三个**接口**(非具体类型),允许测试中替换为 `MockNetwork` / `MockFile` / `MockUI`。

---

## 7. 关键设计决策 / Key Design Decisions

### 7.1 三层加密套娃

| 加密层 | 算法 | 触发时机 | 密钥来源 |
| :--- | :--- | :--- | :--- |
| **Local** | AES-256-GCM | 落盘前 (`db.InsertMessage` 前) | 主密钥 (Argon2id 从密码派生) |
| **Team** | AES-256-GCM | 出站广播前 (网络层) | 队伍口令 → SHA-256 |
| **Private** | Curve25519 ECDH → AES-GCM | 私聊出站前 (可选) | 双方公钥协商出的临时会话密钥 |

**双重加密 (私聊场景)** : 明文先经 `EncryptPrivate` (只有目标可解),再经 `EncryptTeam` (同队中转)。这样在不影响群组路由的前提下,实现了"同队不可旁听"。

### 7.2 确定性 UUID 派生

`derived_id.go` 用 **UUID v5 (SHA-256)** 从命名空间 + 数据派生 ID,保证多设备上同一实体产生同一 ID:

```go
identityNS     = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
teamNS         = uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
shareGroupNS   = uuid.MustParse("6ba7b814-9dad-11d1-80b4-00c04fd430c8")
conversationNS = uuid.MustParse("6ba7b815-9dad-11d1-80b4-00c04fd430c8")

DeriveTeamConvID(teamID)    // SHA256("team:"+teamID) → UUIDv5
DerivePrivateConvID(a, b)   // 排序后 SHA256 → 双方一致
```

**好处** : A 和 B 加入同一队伍后,无需协商即可得到相同的 `conversation_id`,直接通过该 ID 同步消息历史。

### 7.3 HLC (混合逻辑时钟) 格式

`hlc.go` 生成字符串:

```text
2006-01-02T15:04:05.000Z_<4位计数器>_<NodeUUID前8位>
```

例: `2026-06-08T12:30:45.123Z_0042_a1b2c3d4`

- **物理时间**保证大致可读
- **原子计数器**(`% 10000`)保证同毫秒内不冲突
- **节点前缀**打破分布式同时性
- **字典序 = 因果序**,无需 Lamport 矩阵,SQL 索引 `idx_messages_hlc` 即可 `ORDER BY hlc`

### 7.4 Fluent Builder 注入

`file.NewEngine()` 不在构造期吃满所有依赖,允许上层按需装配:

```go
fileEngine := file.NewEngine().
    WithDatabase(database).
    WithEventBus(eventBus).
    WithLogger(logBroker)
```

类似模式可推广到 `crypto.Engine`、`logger.LogBroker`,但目前仅 `file` 完整使用此模式。

### 7.5 前端状态: 单例 Reactive Store

**不用 Pinia**,改用 `composables/useStore.js` 维护模块级 `reactive()` 对象。Vue3 的 `reactive` 自身即可作为状态容器,Pinia 在此场景属于过度抽象。

`composables/` 目录:
- `useStore.js` — 全局响应式状态 (peers / messages / conversations / files)
- `useEvents.js` — `EventsOn` 包装 + 自动清理
- `useBackend.js` — `window.go.app.App.*` Promise 包装
- `useLogStore.js` — 日志面板专用

### 7.6 无路由: 固定三栏外壳

`App.vue` 104 行,采用 CSS Grid 划分:

```text
┌──────────────────────────────────────────────────┐
│  Header  (status bar, language toggle)          │
├──────────┬───────────────────────┬───────────────┤
│ Sidebar  │  Main Content Area    │  Right Panel  │
│ (Teams / │  (Chat / Files / Logs) │  (Peer list / │
│ Convos)  │                       │   Downloads)  │
└──────────┴───────────────────────┴───────────────┘
```

### 7.7 EventBus 的 FIFO + 隔离 panic

`eventbus.go` 为每个有订阅者的 topic 启动**独立 goroutine**,从 256 容量的 channel 中 FIFO 取事件:

- **顺序保证** : 同一 topic 内的所有 handler 按发布顺序串行调用
- **panic 隔离** : `dispatch()` 用 `defer recover()` 包裹每个 handler,避免一条 handler panic 导致后续或总线瘫痪
- **超时保护** : 每个 handler 调用包在 `context.WithTimeout(5s)` 中,防止某 handler 永久阻塞

### 7.8 临时下载文件原子重命名

`transfer.go` 在下载过程中写入 `<target>.parade_tmp` 文件,完成后执行 `os.Rename` 原子替换为正式文件名。这一选择避免了大文件下载中途崩溃导致目标文件半残的问题。

### 7.9 共享目录的 fsnotify 失效

`vfs.go` 在调用 `ShareDirectory` 时为每个根路径创建 fsnotify watcher,任何写入/删除/重命名事件都会清除该路径的树缓存,下次 `GetLocalTree` 触发重新扫描。

### 7.10 后置注入打破循环依赖

`network` 与 `file` 存在双向引用需求 (网络要查询文件元数据,文件事件要触发网络下载),通过两步解决:

1. `NewLibp2pEngine(...)` 时不传 fileEngine
2. 主流程中调用 `AttachFileEngine(fileEngine)`,内部保存到 `fileEngine` 字段

`network` 内部使用 `FileTransferEngine` 接口(在 `network/interfaces.go`)而非具体 `*file.Engine`,完全解耦。

---

## 8. 与 AGENTS.md / CLAUDE.md 的差异 / Notable Discrepancies

> 当前文档为**实测结果**;AGENTS.md / CLAUDE.md / `internal/network/README.md` 部分描述已过时,以下逐条说明。

| # | 文档描述 | 实际实现 | 备注 |
| :-: | :--- | :--- | :--- |
| 1 | "Control plane 端口 4327 gRPC bidi + Data plane 4328 gRPC server stream" | 全部由 **libp2p v0.48.0** 承载,使用 **GossipSub** 群发,**新流协议**承载文件分块 (`/parade/identify/1.0.0` 等) | 4327/4328 端口与 gRPC 服务在 v0.2 中已废弃 |
| 2 | "AGENTS.md / CLAUDE.md 未提及 `internal/core/logger`" | 实际存在 `broker.go` / `logger.go` / `broker_test.go`,被 `file`、`network`、`app` 全部注入 | 应作为正式核心模块补充进 AGENTS.md |
| 3 | "`proto/chat.proto` 与 `proto/file.proto` 唯一生成源" | `proto/chat.pb.go` 是陈旧副本 (13,532 字节,与 `internal/network/pb/chatpb/chat.pb.go` 内容不一致) | 实际编译期使用的是 `internal/network/pb/chatpb/` |
| 4 | "AGENTS.md 列 5 个网络层文件" | 实际有 **6 个** libp2p 文件: `libp2p_engine.go` / `libp2p_host.go` / `libp2p_connect.go` / `libp2p_chat.go` / `libp2p_file.go` / `libp2p_sync.go`,加上 `interfaces.go` / `types.go` 共 8 个 | 缺 `libp2p_sync.go` 的描述 |
| 5 | "`internal/network/README.md` 引用 `discovery.go` / `grpc_chat.go` / `grpc_file.go`" | 三个文件均不存在;实际是 libp2p_* 前缀 | 该 README **整体需要重写** |
| 6 | "Frontend: Vue3 + Pinia" | **未使用 Pinia**,改用 `composables/useStore.js` 单例 reactive | 旧说明残留 |
| 7 | "Vue3 路由" | **无 vue-router**;`App.vue` 是固定三栏外壳 | 旧说明残留 |
| 8 | "App tests require CGO for some deps" | **整个项目无 CGO** (`modernc.org/sqlite` 是纯 Go) | AGENTS.md 错误 |
| 9 | "网络层联调模式: BroadcastTeam 解密本地 + 重发到 EventBus" | 联调逻辑已替换为真实 libp2p GossipSub,本地回环由 libp2p 自身处理 | 旧实现已删除 |

### 8.1 推荐的文档修正

- **AGENTS.md** : 第 26-28 行将"Network: Control plane (port 4327, gRPC bidi) + Data plane (port 4328, gRPC server streaming)"改为"**Network: libp2p v0.48.0 (TCP+Noise+Yamux),GossipSub v0.16.0 for chat,custom stream protocols for files**";新增"**Logger** : 异步 JSONL 日志代理"
- **CLAUDE.md** : 删除所有 `grpc_*.go` 文件名,替换为 `libp2p_*.go`;删除 "Vue3 + Pinia" 字样
- **`internal/network/README.md`** : 重写整篇,改用 6 个 libp2p 文件的描述

---

## 9. 数据资产清单 / Runtime Artifacts

应用在用户主目录产生以下文件:

| 文件路径 | 用途 | 持久格式 |
| :--- | :--- | :--- |
| `.parade_identity` | Curve25519 密钥对 (密码 + Argon2 派生密钥加密) | 二进制 |
| `.parade_data.db` | SQLite 主库 | WAL: `.parade_data.db-wal` / `-shm` |
| `.parade.log` | 应用运行日志 | JSONL 追加 |
| `.parade_peers` | 已发现对端列表 | 文本 |
| `<target>.parade_tmp` | 下载中临时文件 | 二进制 |
| `<target>.parade_tmp.bitmap` | OOO 块接收位图 | 二进制 |

**所有运行时文件**与测试文件 (`test*.db`, `test*.db-wal`, `test*.id`) 都在 `.gitignore` 中,不会污染版本库。

---

## 10. 后续可深入阅读的子文档

| 主题 | 推荐文件 |
| :--- | :--- |
| 业务编排入口 | `internal/app/app.go` (1,017 行) |
| 网络握手协议 | `internal/network/libp2p_connect.go` (3 阶段) |
| 文件分块协议 | `internal/file/transfer.go` + `internal/network/libp2p_file.go` |
| 加密接口契约 | `internal/core/crypto/interface.go` |
| 事件主题字典 | `internal/core/eventbus/topics.go` |
| 引擎装配 | `main.go` (100 行) |

---

**End of Project Overview**
