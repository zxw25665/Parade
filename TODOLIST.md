# Parade 待办与修复清单

## 🔴 阻塞性问题

### 1. main.go 为空 — 无应用入口

| 字段 | 内容 |
|------|------|
| **描述** | `main.go` 仅有一个空文件，没有 Wails 初始化、没有服务实例化、没有 gRPC 服务器设置、没有依赖注入。整个应用无法启动。 |
| **层级** | 应用入口 |
| **代码位置** | `main.go:1` |
| **后果** | 应用无法运行。所有功能代码均处于"无法被调用"状态。 |
| **修复方向** | 编写 `main.go`：初始化各引擎（eventbus, crypto, db, file, network），进行依赖注入，创建 `grpc.Server` 并监听 4327（控制面）和 4328（数据面），启动 Wails 应用，注册 `signal.Notify` 实现优雅关闭。 |

### 2. 网络层为本地桩模块

| 字段 | 内容 |
|------|------|
| **描述** | `BroadcastTeam()` 和 `UnicastPrivate()` 仅做本地解密再重新发布到本地 EventBus，无实际网络 I/O。`Discovery.Run()` 为 `<-ctx.Done()` 空操作。无 mDNS 广播、无 gRPC 服务器监听。 |
| **层级** | 网络层（`internal/network/`） |
| **代码位置** | `grpc_chat.go:55-101`（BroadcastTeam/UnicastPrivate 本地回环）、`discovery.go:80-82`（Run 空转） |
| **后果** | 多节点无法发现彼此，消息无法在局域网传输，所有"网络通信"实际为单机自循环。 |
| **修复方向** | 1) 实现 `Discovery.Run()` 集成 `hashicorp/mdns` 广播 `_parade._tcp` 服务；2) 启动 gRPC server 监听 4327/4328；3) `BroadcastTeam` 通过 gRPC 双向流 `StreamChat` 向所有已连接对等节点发送；4) 集成 `memberlist` 做心跳和 Gossip 维护。 |

### 3. chat.proto 为空

| 字段 | 内容 |
|------|------|
| **描述** | `proto/chat.proto` 为零字节文件，未定义任何 protobuf 消息或 gRPC 服务。虽然网络层引用聊天功能，但没有对应的 protobuf schema。 |
| **层级** | 协议定义（`proto/`） |
| **代码位置** | `proto/chat.proto:1`（空文件） |
| **后果** | 控制面信令（聊天流、元数据同步）无协议定义，无法生成 gRPC 代码，网络层无法实现真正的双向通信。 |
| **修复方向** | 定义 `Envelope` 消息（sender_id, payload, signature）、`StreamChat` 双向流 RPC、`SyncMetadata` 双向流 RPC，运行 protoc 生成 pb.go。 |

---

## 🟠 功能缺失

### 4. 文件共享 API 未暴露到应用层

| 字段 | 内容 |
|------|------|
| **描述** | `file.Engine` 实现了 `ShareDirectory`、`UnshareDirectory`、`GetLocalTree`、`GetDirectoryChildren` 等方法，但 `app.App` 没有对应的 Wails 绑定方法。前端完全无法调用文件共享功能。 |
| **层级** | 应用层（`internal/app/`） → 文件层（`internal/file/`） |
| **代码位置** | `app/app.go:83-148`（现有前端 API 方法列表，无文件相关方法）、`app/interfaces.go:12-14`（FileEngine 接口仅定义了 GetVirtualTree 和 StartDownload） |
| **后果** | 前端无法共享目录、查看虚拟文件树、浏览目录内容。文件共享功能完全不可用。 |
| **修复方向** | 在 `App` 上添加 `ShareDirectory`、`UnshareDirectory`、`GetLocalTree`、`GetDirectoryChildren` 等 Wails 绑定方法，更新 `FileEngine` 接口匹配 file 层完整能力。 |

### 5. 对等节点管理未暴露到前端

| 字段 | 内容 |
|------|------|
| **描述** | `Discovery.Snapshot()` 存在但未连接到 `app.App`。前端没有任何方式获取在线对等节点列表。 |
| **层级** | 应用层（`internal/app/`） → 网络层（`internal/network/`） |
| **代码位置** | `network/discovery.go:68-76`（Snapshot 实现）、`app/app.go:49-79`（registerEventSubscribers 未订阅/转发 peer 事件到 UI） |
| **后果** | 用户无法看到局域网内的其他成员，无法选择传输目标。 |
| **修复方向** | 在 `App` 上添加 `GetPeers()` 方法调用 `Discovery.Snapshot()`，确认 `registerEventSubscribers` 中已将 `TopicPeerJoined`/`TopicPeerLeft` 转发到 `ui.Notify`。 |

### 6. 文件下载端到端编排未接通

| 字段 | 内容 |
|------|------|
| **描述** | `file/transfer.go:196` 的 `StartDownload()` 返回 `errors.New("start download is not connected yet")`。虽然 `network/grpc_file.go` 实现了完整的 `StartDownloadWithRetry` 编排，但 app 层未调用它。 |
| **层级** | 应用层（`internal/app/`） → 网络层（`internal/network/`） + 文件层（`internal/file/`） |
| **代码位置** | `file/transfer.go:196-198`（StartDownload 桩）、`network/grpc_file.go:175-227`（StartDownloadWithRetry 实现，但未被 app 层调用） |
| **后果** | 文件下载功能完全断开。即使网络层实现完成，文件也无法从远端下载到本地。 |
| **修复方向** | 在 `App` 上添加 `StartDownload` Wails 绑定，内部调用 `network.StartDownloadWithRetry`；在 `registerEventSubscribers` 中订阅 `TopicFileProgress` 和 `TopicFileCompleted` 并推送到前端 UI。 |

### 7. 私聊 E2E 加密流程未接入

| 字段 | 内容 |
|------|------|
| **描述** | `crypto` 层实现了 ECDH 密钥协商（`cipher.go`），但 app 层未暴露任何私聊 API。`UnicastPrivate` 在网络接口中存在，但前端的 `SendTeamChat` 只支持群聊。 |
| **层级** | 应用层（`internal/app/`） + 加密层（`internal/core/crypto/`） |
| **代码位置** | `app/app.go:109-126`（SendTeamChat 仅做群聊）、`app/interfaces.go:8`（UnicastPrivate 在接口中存在但未暴露给前端） |
| **后果** | 无法进行点对点加密私聊。所有聊天仅限于团队广播。 |
| **修复方向** | 暴露 `SendPrivateChat(targetPubKey, text)` 到前端，内部调用 ECDH 会话密钥协商 + 双层加密（团队加密 + E2E 加密），通过 `UnicastPrivate` 发送。 |

### 8. 元数据同步机制缺失

| 字段 | 内容 |
|------|------|
| **描述** | 对等节点重连后，错过的消息和文件元数据没有任何同步机制。设计文档中提到的 `SyncMetadata` RPC 用于 HLC 对账，但未实现。 |
| **层级** | 网络层（`internal/network/`） + 应用层 |
| **代码位置** | `proto/chat.proto`（空文件，无 SyncMetadata 定义）、设计文档 `TODO.md:39` 提到 SyncMetadata 但未实现 |
| **后果** | 节点离线期间产生的消息永久丢失，文件元数据不一致。 |
| **修复方向** | 在 `chat.proto` 中定义 `SyncMetadata` 双向流 RPC，接收对方 HLC，查询本地 DB 中更新的记录并回传。 |

---

## 🟡 代码 Bug 与质量问题

### 9. 测试中使用 time.Sleep 导致竞态条件

| 字段 | 内容 |
|------|------|
| **描述** | 测试依赖 `time.Sleep(100ms)` 等待异步 EventBus 分发，而非使用同步原语。在 CI 或低性能机器上会不稳定。 |
| **层级** | 测试（`internal/app/`） |
| **代码位置** | `app/app_test.go:96`、`app/system_integration_test.go:111` |
| **后果** | 测试间歇性失败，降低了 CI 可靠性。 |
| **修复方向** | 使用 channel 等待或 `sync.WaitGroup` 替代 `time.Sleep`：让 MockUI 在 `Notify` 调用时向 channel 发送信号，测试在超时内等待 channel。 |

### 10. GetMissingChunks 传递 totalSize=0

| 字段 | 内容 |
|------|------|
| **描述** | `GetMissingChunks` 调用 `getOrCreateTracker(taskID, bitmapPath, 0)` 时传入 `totalSize=0`，导致 ChunkTracker 计算出 0 个 slot。 |
| **层级** | 文件层（`internal/file/`） |
| **代码位置** | `file/transfer.go:299-306` |
| **后果** | `GetMissingChunks` 返回空列表或错误结果，断点续传时无法正确恢复缺失块。 |
| **修复方向** | 从 file_log 获取 `totalSize`，或从 bitmap 文件反序列化 tracker 时恢复总数。 |

### 11. SaveChunk 临时文件打开标志不完整

| 字段 | 内容 |
|------|------|
| **描述** | `os.OpenFile(tmpPath, os.O_CREATE\|os.O_WRONLY, 0o644)` 缺少 `O_RDWR`。虽然 Linux 上 `WriteAt` + `O_WRONLY` 工作，但不符合 POSIX 常规。 |
| **层级** | 文件层（`internal/file/`） |
| **代码位置** | `file/transfer.go:131` |
| **后果** | 在某些系统或边界场景下文件操作可能失败。 |
| **修复方向** | 改为 `os.O_CREATE\|os.O_RDWR`。 |

### 12. 事件总线订阅永不取消

| 字段 | 内容 |
|------|------|
| **描述** | `EventBus.Subscribe()` 返回 `Subscription` 对象，但 `registerEventSubscribers()` 忽略返回值。在应用重启或热重载时，旧的 goroutine 仍然运行，导致泄漏。 |
| **层级** | 应用层（`internal/app/`） + 事件总线（`internal/core/eventbus/`） |
| **代码位置** | `app/app.go:49-79`（subscribe 返回值被忽略） |
| **后果** | 多次重启后 EventBus handler goroutine 泄漏，消息被重复处理或出现未定义行为。 |
| **修复方向** | 在 `App` 结构体中保存 `[]eventbus.Subscription`，在 `Shutdown()` 或 `Stop()` 方法中遍历调用 `Unsubscribe()`。 |

### 13. 身份文件路径硬编码

| 字段 | 内容 |
|------|------|
| **描述** | `IdentityFile = "./.parade_identity"` 使用当前工作目录作为路径。从不同目录启动应用时会导致身份文件丢失或冲突。 |
| **层级** | 应用层（`internal/app/`） |
| **代码位置** | `app/app.go:18` |
| **后果** | 多实例运行时身份文件互相覆盖，用户登录失败或身份丢失。 |
| **修复方向** | 使用 `os.UserConfigDir()` 或 `os.UserHomeDir()` 拼接固定路径，例如 `filepath.Join(configDir, ".parade_identity")`。 |

### 14. 哈希缓存基于 modTime 不可靠

| 字段 | 内容 |
|------|------|
| **描述** | hash.go 中 BLAKE3 哈希缓存以 `path + size + modTime` 为 key。`modTime` 在某些文件系统（WSL2、网络挂载、FUSE）上精度不足或更新不及时，导致缓存陈旧。 |
| **层级** | 文件层（`internal/file/`） |
| **代码位置** | `file/hash.go`（hashCacheEntry 结构体和缓存逻辑） |
| **后果** | 文件修改后哈希仍返回旧值，导致块完整性校验失败或传输错误。 |
| **修复方向** | 增加内容变更检测（如文件内容指纹或 inode 变更序号）作为缓存 key 的一部分，或在文件修改事件发生时直接清除对应的哈希缓存。 |

---

## 🔵 安全加固

### 15. 消息无签名验证

| 字段 | 内容 |
|------|------|
| **描述** | `Envelope.Signature` 字段已定义但从未填充或验证。任何持有团队密码的节点可以伪造任意消息源的群聊消息。 |
| **层级** | 网络层（`internal/network/`） + 加密层（`internal/core/crypto/`） |
| **代码位置** | `network/grpc_chat.go:13-17`（Envelope 有签名字段但未使用）、`crypto/interface.go`（无签名/验签方法） |
| **后果** | 内部攻击者可以伪造其他成员的消息，无防抵赖能力。 |
| **修复方向** | 在 `crypto.Engine` 接口中添加 `Sign(data)` 和 `Verify(data, signature, pubKey)` 方法（Ed25519 或使用 X25519 密钥对做签名），发送时签名，接收时验签。 |

### 16. 无结构化日志，敏感数据可能泄漏

| 字段 | 内容 |
|------|------|
| **描述** | 全仓使用 `log.Printf` 无级别区分。`app/app.go:140` 中 `content = string(dec)` 可能在日志中暴露解密后的消息内容。 |
| **层级** | 全局 |
| **代码位置** | `app/app.go:140`（解密内容可能被日志记录）、所有 `log.Printf` 使用处 |
| **后果** | 日志中可能包含用户明文消息、文件路径等敏感信息，且无法按级别过滤。 |
| **修复方向** | 引入 `log/slog`（Go 1.21+）替代 `log.Printf`，区分 debug/info/warn/error 级别，确保敏感数据仅以 `%x` 或脱敏形式记录。 |

---

## 🟣 测试与杂项

### 17. 测试数据库文件泄漏

| 字段 | 内容 |
|------|------|
| **描述** | `integration_test.db-shm` 和 `integration_test.db-wal` 来自集成测试运行后残留在代码仓库中。 |
| **层级** | 仓库根目录 |
| **代码位置** | `integration_test.db-shm`、`integration_test.db-wal`（应被 gitignore 覆盖） |
| **后果** | 仓库中残留测试临时文件，可能被误提交。 |
| **修复方向** | 确保 `.gitignore` 包含 `*.db-shm`、`*.db-wal`，删除已跟踪的残留文件并提交。 |

### 18. 网络层代码无单元测试

| 字段 | 内容 |
|------|------|
| **描述** | `grpc_file.go`、`grpc_chat.go`、`discovery.go` 没有任何单元测试。 |
| **层级** | 网络层（`internal/network/`） |
| **代码位置** | `network/` 目录下无 `*_test.go` 文件 |
| **后果** | 网络层逻辑无法通过测试验证，重构风险高。 |
| **修复方向** | 添加 gRPC 服务端/客户端本地 loopback 测试（使用 `grpc.NewServer` + 内存连接），添加 Discovery 状态管理测试。 |

---

## 优先级建议

| 优先级 | 任务编号 | 预估工作量 |
|--------|----------|-----------|
| P0（阻塞） | 1, 2, 3 | 大 |
| P1（功能缺失） | 4, 5, 6, 7 | 中 |
| P1（元数据同步） | 8 | 中 |
| P2（Bug 修复） | 9, 10, 11, 12, 13 | 小 |
| P2（安全加固） | 15, 16 | 中 |
| P3（测试与清理） | 17, 18 | 小 |
| P4（哈希缓存） | 14 | 小 |
