# Core Layer 设计文档

> 范围：`parade/internal/core/`
> 子模块：`eventbus`、`crypto`、`db`、`logger`
> 状态：四个子模块均为**一等公民**，在 `main.go` 中按顺序实例化（`logger` → `db` → `eventbus` → `crypto`）。

Core Layer 是整个 Parade 系统的"地基"。它不依赖任何业务模块，只依赖 Go 标准库与少量经过审计的第三方密码学 / SQLite 驱动。它向下不持有线程，向上为 App / Network / File / Discovery 层提供无副作用的纯基础设施 API。

```
┌──────────────────────────────────────────────────────────┐
│                       App Layer                          │
│           (orchestrates engines, exposes Wails)          │
├──────────────┬───────────────┬───────────────┬───────────┤
│   Network    │     File      │   Discovery   │  ...      │
├──────────────┴───────────────┴───────────────┴───────────┤
│                       Core Layer                         │
│  ┌────────┐  ┌────────┐  ┌────────┐  ┌──────────────┐    │
│  │ event  │  │ crypto │  │  db    │  │   logger     │    │
│  │ bus    │  │        │  │ (SQLite│  │ (JSONL ring  │    │
│  │        │  │        │  │  WAL)  │  │  buffer)     │    │
│  └────────┘  └────────┘  └────────┘  └──────────────┘    │
├──────────────────────────────────────────────────────────┤
│        stdlib │ argon2 │ curve25519 │ modernc/sqlite      │
└──────────────────────────────────────────────────────────┘
```

---

## 1. EventBus — 异步发布 / 订阅 (`internal/core/eventbus/`)

### 1.1 设计目标

- **解耦**：网络层、文件层、发现层在完全不知道彼此存在的情况下，把状态变更推给总线。
- **可观测**：所有事件按 topic 进入缓冲通道，方便日志 / 调试 UI 接入。
- **健壮性**：单个 handler 抛 panic 不影响其他 handler，不影响总线本身。

### 1.2 实现机制

核心类型 `localEventBus`（`eventbus.go`）：

| 组件 | 行为 |
|---|---|
| `handlers map[string]map[SubscriptionID]EventHandler` | topic → 订阅者集合 |
| `topicChs map[string]chan Event` | 每个 topic 一条容量 256 的缓冲通道 |
| `cancelFns map[string]context.CancelFunc` | 每个 topic 关联一个 `context.CancelFunc` |
| `wg sync.WaitGroup` | 跟踪 topic 后台 goroutine 生命周期 |
| `handlerTimeout time.Duration` | 默认 5 秒，可通过 `NewWithTimeout` 覆盖 |

**生命周期规则**：

1. **首次 Subscribe**：在 `bus.mu` 写锁下创建 channel + cancel，启动 `runTopicLoop` goroutine。
2. **后续 Subscribe**：只追加 handler 引用，不新建 goroutine。
3. **Unsubscribe**：当 topic 的最后一个订阅者离开时，`cancel()` 触发 `ctx.Done()`，goroutine 在下一轮 `select` 退出。channel 不显式 `close()`，由 GC 回收。
4. **Publish**：非阻塞。`select` 命中 `default` 即丢弃，并打 `WARN` 日志（容量满）。
5. **PublishSync**：跳过 channel，直接在调用者 goroutine 中同步执行所有 handler（用于测试与关键路径，避免 `time.Sleep` 竞态）。

**Handler 包装**（`dispatch` 方法）：

```go
for _, h := range handlers {
    func() {
        defer func() {
            if r := recover(); r != nil {
                log.Printf("[EventBus] CRITICAL: Recovered from handler panic in topic [%s]: %v", topic, r)
            }
        }()
        hCtx, cancel := context.WithTimeout(ctx, bus.handlerTimeout)
        defer cancel()
        h(hCtx, ev)
    }()
}
```

- 每个 handler 拿到一个带超时的 `context.Context`。
- panic 被 `recover()` 吞掉，仅记 `CRITICAL` 日志。
- handler 之间顺序串行（同一 topic 内 FIFO），不并发。

### 1.3 主题字典（实际 11 个）

> ⚠️ **README 与代码不同步**：`internal/core/eventbus/README.md` 只列出了 4 个 topic（`TopicPeerJoined`、`TopicMsgReceived`、`TopicFileProgress`、`TopicFileCompleted`）。代码中实际定义了 11 个 topic，新增的 7 个是为了支持私聊、目录监听、日志桥接与对话对账等场景。

| 常量 | 字符串主题 | 载荷类型 | 触发场景 |
|---|---|---|---|
| `TopicPeerJoined` | `network:peer_joined` | `PeerEventPayload` | mDNS 发现新邻居 |
| `TopicPeerLeft` | `network:peer_left` | `PeerEventPayload` | 心跳超时或主动离开 |
| `TopicMsgReceived` | `network:msg_received` | `MsgReceivedPayload` | 收到群聊 / 系统信令 |
| `TopicPrivateMsgReceived` | `network:private_msg_received` | `MsgReceivedPayload` | 收到 1-on-1 私聊 |
| `TopicFileProgress` | `file:progress` | `FileProgressPayload` | Chunk 写入磁盘成功 |
| `TopicFileCompleted` | `file:completed` | `string` (TaskID) | 传输彻底结束 |
| `TopicDirChanged` | `fs:dir_changed` | `string` (root path) | 共享目录文件变化 |
| `TopicLogEvent` | `system:log_event` | (none) | 内部日志桥接（预留） |
| `TopicPeerOnline` | `network:peer_online` | `PeerEventPayload` | 节点状态从离线→在线 |
| `TopicPeerOffline` | `network:peer_offline` | `PeerEventPayload` | 节点状态从在线→离线 |
| `TopicConvSyncRequest` | `network:conv_sync_request` | `ConversationSyncPayload` | 对话对账请求 / 响应 |

### 1.4 载荷结构

```go
// PeerEventPayload — 节点加入 / 离开 / 在线状态
type PeerEventPayload struct {
    PeerUUID  string
    IPAddress string
}

// MsgReceivedPayload — 收到消息（明文 Content，载荷内已经是 crypto 层解密结果）
type MsgReceivedPayload struct {
    HLC            string // 混合逻辑时钟
    SenderID       string
    Content        []byte // plaintext
    Type           int
    ReceiverID     string
    TeamID         string
    ChannelID      string
    ConversationID string
    SenderIP       string `json:",omitempty"`
    SenderPubKey   string `json:",omitempty"`
}

// FileProgressPayload — 文件块进度
type FileProgressPayload struct {
    TaskID      string
    FilePath    string
    Transferred int64
    TotalSize   int64
    IsUpload    bool
}

// ConversationSyncPayload — 对话对账载荷
type ConversationSyncPayload struct {
    RequesterUUID  string
    ConversationID string
    SinceHLC       string
    Messages       []byte // JSON-serialized []*db.Message
}
```

### 1.5 订阅身份

`SubscriptionID = uuid.New().String()`，由 `google/uuid` 生成。`Unsubscribe(topic, subID)` 必须传入原始 ID 才能精确注销。

---

## 2. Crypto — 身份与加密 (`internal/core/crypto/`)

### 2.1 算法栈

| 算法 | 角色 | 关键参数 / 调用 |
|---|---|---|
| **Argon2id** | 主密钥派生（KDF） | `argon2.IDKey(pw, salt, time=1, mem=64*1024 KiB, threads=4, keyLen=32)` |
| **AES-256-GCM** | 对称加密（落盘 / 队伍 / 私聊） | `aes.NewCipher` + `cipher.NewGCM`，`NonceSize = 12` 字节；线缆格式 `[Nonce 12B] ++ [Ciphertext++Tag]` |
| **Curve25519 (X25519)** | 私聊 ECDH | `curve25519.ScalarMult(&shared, &myPriv, &theirPub)` |
| **SHA-256** | 队伍密钥派生 + ECDH 后 KDF | `sha256.Sum256(input)` |

### 2.2 三级密钥体系

```
                  ┌─────────────────────────────┐
                  │   User Password (明文)      │
                  └────────────┬────────────────┘
                               │  + 16B 随机 salt
                               ▼
                  ┌─────────────────────────────┐
                  │ Argon2id(time=1, mem=64MB,  │
                  │   threads=4) → Master Key  │   ← 仅存内存
                  │            32B              │
                  └──┬───────────────────┬──────┘
                     │                   │
        AES-GCM 加密  │                   │  AES-GCM 加密
        .parade_identity priv            │  .parade_teams blob
                     │                   │
                     ▼                   ▼
            ┌─────────────────┐  ┌──────────────────────┐
            │ Curve25519 Priv │  │  队伍密钥环 (多 team) │
            │      32B        │  │  map[teamID][]byte   │
            └─────────────────┘  └──────────┬───────────┘
                                           │ SHA-256(teamPassword)
                                           │   (无 salt)
                                           ▼
                              ┌─────────────────────────┐
                              │   Team Key (32B)        │   ← 持久化到
                              │   按 teamID 索引        │     .parade_teams
                              └─────────────────────────┘

   私聊密钥（每次会话动态派生）:
   ┌────────────────┐                ┌─────────────────┐
   │ My Curve25519  │ ──ScalarMult──▶│ Their Curve25519 │
   │     Priv       │                │       Pub        │
   └────────────────┘                └─────────────────┘
              │                              │
              └───── SHA-256(shared) ────────┘
                            │
                            ▼
                  ┌─────────────────────┐
                  │  Session Key (32B)  │   ← 仅存内存
                  └─────────────────────┘
```

| 密钥 | 派生方式 | 存储 | 作用域 |
|---|---|---|---|
| **Master Key (32B)** | `Argon2id(password, salt[16B])` | 内存 | 加密 `.parade_identity` priv + `.parade_teams` blob + 落盘 `Content` |
| **Team Key (32B)** | `sha256(teamPassword)` (无 salt) | 持久化到 `.parade_teams` (用 master 加密) | LAN 群聊 + 系统信令 |
| **Session Key (32B)** | `sha256(curve25519.ScalarShared(myPriv, theirPub))` | 内存，每消息 | 1-on-1 私聊（双重加密内层） |

### 2.3 身份文件格式 (`.parade_identity`)

```json
{
  "salt": "<16 random bytes, base64>",
  "encrypted_priv": "<AES-GCM(master, Curve25519 privKey)>",
  "pub_key": "<32 bytes, base64, 明文以便查阅>"
}
```

- **文件权限**：`0600`（仅当前用户可读写）。
- **生成流程**（`RegisterIdentity`）：
  1. `rand.Read(salt[16])` 生成盐。
  2. `rand.Read(privKey[32])` → `curve25519.ScalarBaseMult(&pubKey, &privKey)` 生成密钥对。
  3. `deriveMasterKey(password, salt)` 计算主密钥。
  4. `aesGCMEncrypt(masterKey, privKey[:])` 加密私钥。
  5. 写 JSON 落盘，**自动调用 `LoadIdentity`**。
- **加载流程**（`LoadIdentity`）：
  1. 读 JSON。
  2. `deriveMasterKey(password, salt)`。
  3. `aesGCMDecrypt` 尝试解出私钥。
  4. 解密失败 → `errors.New("invalid password")`。
  5. 成功 → 设置 `privKey / pubKey / personalUUID`，并以**非致命**方式尝试 `loadTeamKeys()`，错误进入 `loadWarnings`。

### 2.4 确定性 Personal UUID

```go
personalUUID = uuid.NewHash(
    sha256.New(),
    uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8"), // UUIDv5 namespace
    pubKey, // 32B
    5,     // UUID 版本
).String()
```

同一公钥在任意设备上加载都得到**完全一致**的 UUID，便于跨设备身份匹配。

### 2.5 多队伍密钥管理

内部状态：

```go
type paradeCrypto struct {
    masterKey    []byte
    privKey      []byte
    pubKey       []byte
    personalUUID string
    teamKeys     map[string][]byte // teamID → 32B team key
    activeTeam   string
    loadWarnings []error
}
```

**`.parade_teams` 文件**（AES-GCM 加密于 master 之下，权限 `0600`）：

```json
{
  "keys": {
    "<teamID-1>": "<32B raw team key>",
    "<teamID-2>": "<32B raw team key>"
  },
  "active_team": "<teamID-1>"
}
```

- `SetTeamKey(pw)` → 内部以 `teamID = ""` 调用 `SetTeamKeyForTeam`（向后兼容老代码）。
- `RemoveTeamKey(teamID)`：若被删除的是 `activeTeam`，则回退到 `teamKeys` 中第一个非空 key。
- `SetActiveTeam(teamID)`：切换活跃队伍，并持久化。
- `TeamKeyHash()` / `TeamKeyHashFor(teamID)`：返回 `fmt.Sprintf("%x", sha256(teamKey))`，用作 mDNS TXT 记录中的"同队指纹"。

### 2.6 加解密入口

| 方法 | 密钥 | 失败时错误 |
|---|---|---|
| `EncryptLocal / DecryptLocal` | `masterKey` | `"identity not loaded"` |
| `EncryptTeam / DecryptTeam` | `teamKeys[activeTeam]` | `"team key not set"` |
| `DecryptTeamForTeam(teamID, …)` | `teamKeys[teamID]` | `"team key not found for team: …"` |
| `EncryptPrivate / DecryptPrivate` | `sha256(ECDH(myPriv, theirPub))` | `"invalid remote public key"` / `"identity not loaded"` |

**私聊会话密钥派生**（`getSessionKey`）：

```go
remotePubKey := base64.StdEncoding.DecodeString(remotePubKeyBase64)
curve25519.ScalarMult(&sharedSecret, &myPriv, &theirPub)
sessionKey := sha256.Sum256(sharedSecret[:])
```

**双重加密模式**（典型私聊出站）：

```
raw message
   │  EncryptPrivate(raw, peerPubBase64)        // 内层：只对端能解
   ▼
intermediate envelope
   │  EncryptTeam(intermediate)                  // 外层：全队可解（用于局域网转发）
   ▼
wire payload (sent via 4327 / 4328)
```

外层 `EncryptTeam` 让局域网中其他同队节点（即便不是对话目标）也能解密外层信封，从而进行中继 / 缓存；只有真正的对端才能解开内层。

### 2.7 Engine 接口 (21 方法)

```text
身份:        RegisterIdentity, LoadIdentity, GetPublicKeyBase64,
             GetPrivateKey, GetPersonalUUID, IdentityLoadWarnings

队伍:        SaveTeamKeys, SetTeamKey, TeamKeyHash,
             SetTeamKeyForTeam, RemoveTeamKey, SetActiveTeam,
             GetActiveTeam, GetTeamIDs, TeamKeyHashFor, DecryptTeamForTeam

加解密:      EncryptLocal, DecryptLocal,
             EncryptTeam, DecryptTeam,
             EncryptPrivate, DecryptPrivate
```

---

## 3. DB — SQLite 持久化 (`internal/core/db/`)

### 3.1 驱动与连接配置

驱动：`modernc.org/sqlite`（**纯 Go 实现，零 CGO**），注册名 `"sqlite"`。

**PRAGMA 调优**（在 `NewSQLiteDB` 中按顺序执行）：

| PRAGMA | 取值 | 目的 |
|---|---|---|
| `journal_mode` | `WAL` | 多读单写并发，消除 `database is locked` |
| `synchronous` | `NORMAL` | WAL 配合下的速度 / 安全性折中 |
| `busy_timeout` | `5000` ms | 写冲突自动重试 |
| `temp_store` | `MEMORY` | 临时表 / 索引放内存 |
| `cache_size` | `-64000` KB (64 MB) | 页面缓存 |
| `foreign_keys` | `ON` | 外键约束启用 |

**Go 连接池**：

```go
db.SetMaxOpenConns(5)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(time.Hour)
```

WAL 模式下多读单写，5 个连接是兼顾并发与不超 SQLite 写锁上限的经验值。

### 3.2 Schema 迁移 (10 个版本)

迁移通过 `schema_meta` 表的 `key='version'` 记录当前版本，按 `Version` 升序逐个执行，每个迁移包裹在独立事务中：

| Version | Name | 主要变更 |
|---|---|---|
| 1 | `baseline` | 建 `messages` + `file_logs` + 索引 `idx_messages_hlc` |
| 2 | `add_team_id_to_messages` | `messages.team_id TEXT NOT NULL DEFAULT ''` + `idx_messages_team_hlc` |
| 3 | `add_teams_table` | 建 `teams(id, name, team_hash)` + `idx_teams_hash` |
| 4 | `add_shared_directories_table` | 建 `shared_directories(id, path UNIQUE, team_id)` |
| 5 | `add_channels_tables` | 建 `channels` + `channel_members` + `messages.channel_id` (在 v8 被回滚) |
| 6 | `add_share_groups_tables` | 建 `share_groups` + `share_group_dirs` |
| 7 | `add_conversations_table` | 建 `conversations` + `messages.conversation_id` + `idx_messages_conv_hlc` |
| 8 | `drop_channels_tables` | `DROP TABLE channel_members, channels`（频道模型被会话模型取代） |
| 9 | `unique_hlc` | 去重 + 建 `UNIQUE INDEX idx_messages_hlc_unique` |
| 10 | `add_peer_crypto_key` | `conversations.peer_crypto_key`（用于 E2E 私聊 ECDH） |

### 3.3 最终表清单 (8 张)

| 表 | 用途 |
|---|---|
| `messages` | 聊天 / 信令 (`Content` 为加密 BLOB，HLC 唯一) |
| `file_logs` | 传输状态 (TaskID PK, transferred, status 0/1/2/3) |
| `teams` | 团队注册 (id, name, team_hash 索引) |
| `shared_directories` | 虚拟共享目录 (path UNIQUE) |
| `share_groups` | 命名目录组 |
| `share_group_dirs` | 组 ↔ 目录 多对多 |
| `conversations` | DM / 群聊会话实体 (ID 确定性) |
| `schema_meta` | 迁移版本号 |

### 3.4 关键常量

```go
const ReceiverIDGroupChat = "" // 空字符串哨兵：表示群聊消息
```

### 3.5 Database 接口 (~30 方法)

按域分组：

- **消息**：`InsertMessage` (`ON CONFLICT(hlc) DO NOTHING`)、`GetMessagesSinceHLC`、`GetRecentMessages`、`GetRecentMessagesByTeam`、`GetRecentMessagesByChannel`、`GetMessagesSinceHLCByTeam`。
- **文件日志**：`UpsertFileLog` (`ON CONFLICT(task_id) DO UPDATE`)、`GetFileLog`。
- **团队**：`InsertTeam` (`INSERT OR IGNORE`)、`GetTeam`、`GetTeamByHash`、`ListTeams`、`DeleteTeam`。
- **共享目录**：`InsertSharedDirectory`、`DeleteSharedDirectory`、`ListSharedDirectories`。
- **共享组**：`CreateShareGroup`、`GetShareGroup`、`ListShareGroupsByTeam`、`DeleteShareGroup`（事务内先删子表再删主表）、`AddDirectoryToShareGroup`、`RemoveDirectoryFromShareGroup`、`ListShareGroupDirs`。
- **会话**：`UpsertConversation`（用 `COALESCE(NULLIF(excluded.X, ''), conversations.X)` 保留旧值）、`GetConversation`、`ListConversations`（`LEFT JOIN` 最新消息 → `ConversationView`）、`GetConversationMessages`、`GetConversationMessagesSinceHLC`、`UpdateConversationLastHLC`、`ListAllConversations`。
- **生命周期**：`Close`、`RunInTx`。

### 3.6 事务封装

`RunInTx(ctx, fn)` 把 `*sql.Tx` 包成 `DBTx` 接口，仅暴露三个事务内安全方法：

```go
type DBTx interface {
    InsertMessageTx(ctx, *Message) error
    UpsertFileLogTx(ctx, *FileLog) error
    UpsertConversationTx(ctx, *Conversation) error
}
```

典型用途：网络对账时一次性写入上千条缺失消息，避免每条都触发一次磁盘 I/O。`dbTxWrapper` 内部仍走 `*sql.Tx.ExecContext`，错误时整体回滚。

### 3.7 数据模型 (`models.go`)

```go
type Message struct {
    ID, HLC, SenderID, ReceiverID, TeamID,
    ChannelID, ConversationID string
    Content                    []byte    // 加密 BLOB
    Type                       int
    CreatedAt                  time.Time
}

type FileLog struct {
    TaskID, FilePath, PeerID  string
    Direction                 int       // 0=上传, 1=下载
    TotalSize, Transferred    int64
    Status                    int       // 0=传输中, 1=完成, 2=暂停, 3=中断
    UpdatedAt                 time.Time
}

type Team struct {
    ID, Name, TeamHash string
    CreatedAt          time.Time
}

type SharedDirectory struct {
    ID, Path, TeamID string
    CreatedAt        time.Time
}

type ShareGroup struct {
    ID, TeamID, Name, CreatedBy string
    CreatedAt                   time.Time
}

type ShareGroupDir struct {
    GroupID, DirPath string
    AddedAt          time.Time
}

type Conversation struct {
    ID, TeamID, Type, DisplayName             string
    PeerPubkey, MyPubkey                       string // Parade UUID（不是 Curve25519）
    PeerCryptoKey                              string // Curve25519 Base64 公钥，用于 ECDH 解密
    LastHLC                                    string
    CreatedAt, UpdatedAt                       time.Time
}

type ConversationView struct {
    Conversation
    LastMessage string
    LastMsgTime time.Time
}
```

> **注**：v10 之后 `PeerPubkey` 与 `MyPubkey` 存的是 Parade UUID（与 `crypto.personalUUID` 一致），`PeerCryptoKey` 才是 Curve25519 Base64 公钥。会话 ID 由 `DeriveTeamConvID` / `DerivePrivateConvID` 确定性推导，同一队伍 / 同一对端在不同设备上得到相同 ID。

---

## 4. Logger — 异步 JSONL Broker (`internal/core/logger/`)

> **此模块在 AGENTS.md / CLAUDE.md 中未被文档化**，但属于一等模块：`main.go` 第 51 行 `logBroker, err := logger.NewLogBroker("./.parade.log", 5000)` 即其实例化位置。

### 4.1 组件拆解

| 类型 | 角色 |
|---|---|
| `LogLevel` (int 枚举) | `Trace=1, Debug=2, Info=3, Warning=4, Error=5`，自带 `String()` 返回小写名 |
| `LogEntry` | 一条日志记录，4 字段：Timestamp / Level / Source / Message，全部 JSON 标签 |
| `Logger` 接口 | 5 个方法 `Trace / Debug / Info / Warn / Error(source, msg string)` |
| `LogBroker` | 接口的并发安全实现 |

### 4.2 LogBroker 行为

- **环形缓冲**：`buf []LogEntry` 容量 = `maxEntries`（`main.go` 传 5000）。`head` 是下一个写入位置，`count` 是当前已写数量。`Entries()` 按时间顺序拷贝一份（处理 wrap-around）。
- **JSONL 文件**：`os.OpenFile(path, O_APPEND|O_CREATE|O_WRONLY, 0644)`。`writeToFile` 在 `fileMu` 锁保护下逐行 `fmt.Fprintf` JSON + `\n`，**写入单行是原子的**（Linux 下 `PIPE_BUF` 内）。
- **回调**：`SetCallback(func(LogEntry))` 启用后，每条新日志会同步触发回调（用于 UI 实时流式显示）。
- **过滤**：`SetMinLevel(LogLevel)`，低于阈值的 entry 不进 buffer、不写文件、不触发回调。
- **默认**：`minLevel = Debug`。
- **生命周期**：`Clear()` 清空 buffer（不动文件）；`Close()` 关闭 file 句柄并清空引用（不 `Sync()`，留给 OS 缓冲刷写）。

### 4.3 调用约定

```go
// main.go
logBroker, _ := logger.NewLogBroker("./.parade.log", 5000)
defer logBroker.Close()
logBroker.SetCallback(func(e logger.LogEntry) {
    runtime.EventsEmit(ctx, "log:new", e) // 推给 Vue 前端
})
// 业务侧拿到 Logger 接口后：
logBroker.Info("network", "peer joined: "+peerUUID)
```

---

## 5. 跨模块依赖矩阵

| 模块 | 外部依赖 |
|---|---|
| `eventbus` | `github.com/google/uuid`（订阅 ID）+ stdlib |
| `crypto` | `github.com/google/uuid`（v5 personal UUID）<br>`golang.org/x/crypto/argon2`（KDF）<br>`golang.org/x/crypto/curve25519`（ECDH）<br>stdlib（`crypto/aes`、`crypto/cipher`、`crypto/sha256`、`encoding/base64`、`encoding/json`） |
| `db` | `modernc.org/sqlite`（纯 Go SQLite 驱动）<br>stdlib（`database/sql`、`context`） |
| `logger` | stdlib only（`encoding/json`、`os`、`sync`、`time`、`fmt`） |

四个子模块之间**不相互依赖**（Core Layer 内部无横向引用）。它们只被 `app` 层聚合，`app` 层把 `eventbus` 注入到 `crypto / db / file / network` 等引擎中。

---

## 6. 文件清单

```
internal/core/
├── eventbus/
│   ├── eventbus.go        # 247 行 — localEventBus, Subscribe/Publish/PublishSync
│   ├── topics.go          #  55 行 — 11 个 topic 常量 + 4 个 payload 结构
│   ├── eventbus_test.go
│   └── README.md          # ⚠️ 已过时（只列 4 个 topic）
│
├── crypto/
│   ├── interface.go       #  46 行 — Engine 21 方法
│   ├── keystore.go        # 145 行 — IdentityFile, RegisterIdentity, LoadIdentity
│   ├── cipher.go          # 264 行 — aesGCM*, Encrypt*/Decrypt*, SaveTeamKeys
│   ├── crypto_test.go
│   └── README.md
│
├── db/
│   ├── interface.go       #  69 行 — Database + DBTx
│   ├── models.go          #  87 行 — Message/FileLog/Team/.../ConversationView
│   ├── sqlite.go          # 828 行 — 10 迁移 + 全部实现
│   ├── sqlite_test.go
│   └── README.md
│
└── logger/
    ├── logger.go          #  49 行 — LogLevel, LogEntry, Logger 接口
    ├── broker.go          # 134 行 — LogBroker 实现（环形缓冲 + JSONL + 回调）
    └── broker_test.go
```

---

## 7. 已知问题 / 后续 TODO

1. **eventbus README 过期**：仅 4 个 topic，缺 7 个；建议同步至 11 个。
2. **eventbus 无 Close 方法**：当前依赖最后一个 `Unsubscribe` 触发 goroutine 退出；如需显式停机，需要在 `localEventBus` 上加 `Close()` 统一取消所有 topic。
3. **crypto 内存中的主密钥与私钥**：当前没有 `mlock` 保护，也没有在 `Close` / 退出时主动清零。理论上 `core dump` 存在泄露风险。后续可考虑在退出路径中 `runtime.SetFinalizer` 触发 `memzero`。
4. **db 无自动 vacuum**：随消息累积，WAL 文件可能持续增长。可在后续版本加入周期性的 `PRAGMA wal_checkpoint(TRUNCATE)` 与 `incremental_vacuum`。
5. **logger 无文件 rotate**：`./.parade.log` 单一 append 模式。生产环境需要按大小 / 日期切割（可考虑引入 `gopkg.in/natefinch/lumberjack.v2`）。
6. **logger 与 eventbus 未联动**：可考虑在 `SetCallback` 中同时 `bus.Publish(TopicLogEvent, entry)`，让 UI 通过 eventbus 而非直接回调订阅日志流，便于将来引入多消费者。
