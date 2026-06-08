# Parade 网络层架构设计文档

> 路径: `internal/network/`
> 范围: libp2p 节点生命周期、协议流、聊天/文件/同步通道
> 状态: `v0.2.0-libp2p`
> 设计 vs 运行时: 本文档严格区分**设计意图**（`游行-设计书.md` 中的 gRPC 4327/4328 双平面）与**运行时现实**（libp2p 单端口 4327 + TCP 识别服务 4328）

---

## 1. 目的 (Purpose)

`internal/network` 是 Parade 项目五大引擎中负责**节点间通信**的一个。它的核心职责有四点:

1. **节点生命周期管理** — 启动/停止 libp2p 主机,维护已发现节点的本地副本(`./.parade_peers`)。
2. **协议流多路复用** — 在单一 TCP 端口上同时承载识别、握手、群聊 (GossipSub)、私聊、文件元数据、文件下载、目录浏览、会话同步七种协议。
3. **3 阶段连接握手** — 在上层 App 触发 `ConnectToPeer(ip)` 时,串行完成 TCP 识别 → libp2p 连接 → 队伍密钥挑战 → 测试消息,产出 `PeerConnectResult`。
4. **EventBus 桥接** — 把节点上下线、收到的消息、文件进度等异步事件翻译为 `eventbus` 主题,供 App 层订阅并推送到前端。

依赖方向:

```
App 层  ──持有──>  NetworkEngine (libp2pEngine)  ──持有──>  host / chat / file / sync
                            │
                            ├──>  internal/core/crypto  (EncryptTeam / EncryptPrivate / TeamKeyHash)
                            ├──>  internal/core/eventbus (发布与订阅)
                            └──>  internal/file          (FileTransferEngine 注入)
```

---

## 2. 设计意图 vs 运行时现实

> 这是阅读本模块时最容易踩坑的地方。原设计书与当前运行行为存在系统性偏差,必须显式标注。

| 维度 | 设计意图 (游行-设计书.md) | 运行时现实 (v0.2.0-libp2p) |
|------|--------------------------|--------------------------|
| 控制面 | gRPC 4327 端口,双向流 `StreamChat` | libp2p 主机 4327 端口,GossipSub 群聊 + 多条自定义协议流 |
| 数据面 | gRPC 4328 端口,服务器流 `DownloadFile` | libp2p 主机 4327 多路复用 `/parade/file-download/1.0.0`;**4328 仅是明文 TCP 识别服务**(返回 JSON) |
| 节点识别 | gRPC `Handshake` 一次性握手 | TCP 4328 明文 JSON `{peer_id,uuid,pubkey}`,启动后持续监听 |
| 消息加密 | 控制面长连接 + 应用层加密 | Noise 传输层 (ed25519) + Yamux 多路复用 + 应用层 EncryptTeam/EncryptPrivate |
| 文件传输 | gRPC server streaming | libp2p 流,4 字节长度前缀 + JSON (复用 `pb.FileChunk` 类型) |
| 消息格式 | protobuf `Envelope` | GossipSub payload 是加密字节流;`Envelope` 类型在运行时**未使用**,仅在生成代码中存在 |
| 文件格式 | protobuf `FileRequest`/`FileChunk` | 运行时是 JSON;`pb.FileChunk` 类型作为结构体被复用,经 JSON 序列化传输 |

> 关键结论: `proto/chat.proto` 与 `proto/file.proto` 定义的 protobuf 服务和消息类型**仍是系统的接口契约**,但当前 libp2p 运行时通过 JSON over streams 实现同样的功能,字段语义与 proto 保持一致。这保留了未来切换到原生 gRPC 的可能性。

---

## 3. 文件清单 (File Inventory)

| 文件 | 行数 | 职责 |
|------|------|------|
| `libp2p_engine.go` | 592 | `libp2pEngine` 主体: 主机生命周期、`./.parade_peers` 持久化、识别协议、上下线状态、连接入口 |
| `libp2p_host.go` | 115 | `libp2pHost` 工厂: 从 Curve25519 私钥派生 ed25519 libp2p 身份、构造 TCP+Noise+Yamux 主机 |
| `libp2p_connect.go` | 185 | 3 阶段 `connectToPeer(ip)`: 4328 识别 → 4327 libp2p 连接 → 队伍密钥挑战 → 测试消息 |
| `libp2p_chat.go` | 345 | `libp2pChat`: GossipSub 群聊 (`parade-chat-<teamHash>`) + 私聊协议流 `/parade/private-chat/1.0.0` |
| `libp2p_file.go` | 472 | `libp2pFile`: 元数据/下载/浏览三协议 + 客户端重试编排 (3 次 600ms 退避) |
| `libp2p_sync.go` | 160 | `libp2pSync`: 会话同步协议 `/parade/sync/1.0.0` (请求/响应 JSON) |
| `interfaces.go` | 15 | `FileTransferEngine` 接口契约 (供 file 包实现,网络层通过此接口读取/写入) |
| `types.go` | 39 | `PeerInfo`、`PeerStatus`、`PhaseResult`、`PeerConnectResult` 数据结构 |
| `FILE_TRANSFER_HANDOFF.md` | 73 | 给 file 包的对接说明;**部分内容已过时**(仍引用 `grpc_file.go` 路径) |
| `README.md` | 37 | 模块说明;**已过时** — 引用了不存在的 `discovery.go`、`grpc_chat.go`、`grpc_file.go` |

生成代码与 proto:

| 路径 | 状态 | 说明 |
|------|------|------|
| `proto/chat.proto` | 权威源 | `ChatService` 三 RPC |
| `proto/file.proto` | 权威源 | `FileTransferService` 三 RPC |
| `proto/chat.pb.go` | **陈旧副本** | 与 `internal/network/pb/chatpb/chat.pb.go` 内容重复,目录位置错误 |
| `internal/network/pb/chatpb/chat.pb.go` + `chat_grpc.pb.go` | 权威生成 | 包名 `chatpb` |
| `internal/network/pb/file.pb.go` + `file_grpc.pb.go` | 权威生成 | 包名 `pb` |

> ⚠️ **过期/陈旧产物**:
> - `internal/network/README.md` 引用的 `discovery.go`、`grpc_chat.go`、`grpc_file.go` 三个文件均不存在,实际实现是 `libp2p_*.go`。
> - `FILE_TRANSFER_HANDOFF.md` 引用了 `grpc_file.go` 路径,实际实现位于 `libp2p_file.go`。
> - `proto/chat.pb.go` 是 `internal/network/pb/chatpb/chat.pb.go` 的副本,应删除以避免混淆 (导入路径不一致)。
> - 实际运行时 gRPC 客户端/服务端代码**未被使用**;`chat_grpc.pb.go` 和 `file_grpc.pb.go` 是契约快照,不是可执行代码。

---

## 4. Proto 包拆分: chatpb vs pb

`chat.proto` 和 `file.proto` **生成到不同的 Go 包**;这一拆分从 `go_package` 选项固化下来,误导入会导致编译失败。

| Proto 文件 | `go_package` | Go 包名 | 标准导入路径 |
|------------|---------------|---------|---------------|
| `proto/chat.proto` | `parade/internal/network/pb/chatpb;chatpb` | `chatpb` | `parade/internal/network/pb/chatpb` |
| `proto/file.proto` | `parade/internal/network/pb;pb` | `pb` | `parade/internal/network/pb` |

```go
// 正确导入示例
import chatpb "parade/internal/network/pb/chatpb"  // 用于 Envelope / HandshakeRequest
import pb      "parade/internal/network/pb"         // 用于 FileChunk / BrowseEntry
```

---

## 5. 协议流清单 (Protocol Stream Inventory)

libp2p 自定义协议流与 GossipSub 主题构成网络层的全部对外接口。所有协议流复用同一 TCP 端口 (4327),由 libp2p 多路复用器 (Yamux) 解析。

| 协议 ID / 主题 | 方向 | 数据格式 | 用途 |
|----------------|------|----------|------|
| `/parade/identify/1.0.0` | 请求/响应 (单条 JSON) | JSON `{peer_id,uuid,pubkey}` | 节点身份交换 (TCP 4328 实际是此协议的明文镜像) |
| `/parade/handshake/1.0.0` | 请求/响应 | 队伍加密的 nonce (字节流) | 队伍密钥挑战:写 nonce,读 EncryptTeam(nonce) |
| `/parade/test/1.0.0` | 请求/响应 | `"test"` → `0x01` | 活性探针 (Phase 3) |
| `/parade/private-chat/1.0.0` | 客户端→服务端 (单条后 ACK) | 4 字节大端长度前缀 + JSON `{u,k,p}` (256KB 上限) | 一对一私聊 |
| `/parade/file-meta/1.0.0` | 客户端→服务端 | UTF-8 路径 (裸字节) → JSON `{file_path,total_size,error?}` | 文件元数据查询 |
| `/parade/file-download/1.0.0` | 客户端→服务端 | JSON `{task_id,file_path,offset}` → 流式 JSON `pb.FileChunk` | 可断点续传分块下载 |
| `/parade/browse/1.0.0` | 客户端→服务端 | UTF-8 路径 (裸字节) → JSON `[]pb.BrowseEntry` (或 `{error}`) | 远程目录浏览 |
| `/parade/sync/1.0.0` | 客户端→服务端 | JSON `syncMessage` (`{type,conv_id,since_hlc?,messages?}`) | 会话历史同步 |
| `parade-chat-<teamHash>` (GossipSub) | 发布/订阅 | 加密字节 (由 `crypto.EncryptTeam` 封装) | 群聊广播 |

> **设计 vs 现实**:
> - 表中前 4 行 (identify / handshake / test / private-chat) 在设计书中并未单独列出协议 ID,而是作为 gRPC 服务的一部分;运行时它们被拆为独立 libp2p 协议。
> - 文件类三协议 (file-meta / file-download / browse) 在设计书中是 gRPC `FileTransferService` 暴露的 RPC;运行时通过同名 libp2p 协议流实现,字段语义与 `file.proto` 一致。
> - GossipSub 群聊**不**使用任何协议 ID;主题名是 `parade-chat-<teamHash>`,由 `crypto.TeamKeyHash()` 计算。

---

## 6. 主机构造 (Host Construction)

### 6.1 身份派生

libp2p 主机需要 ed25519 密钥,但 Parade 用户的根身份是 Curve25519 私钥 (由 Argon2 从口令派生)。`deriveLibp2pKey` 把两者连接起来:

```
Curve25519 私钥 (32 字节)
    │
    ▼  sha256.Sum256()
ed25519 种子 (32 字节)
    │
    ▼  ed25519.NewKeyFromSeed()
ed25519 私钥 (64 字节)
    │
    ▼  libp2pCrypto.UnmarshalEd25519PrivateKey()
libp2p PrivKey
```

> 这是**确定性派生** — 同一 Curve25519 私钥永远产生同一 libp2p PeerID,简化了节点持久化。`pubKeyToPeerID` 走对称流程从公钥派生 PeerID。

### 6.2 传输栈

`NewLibp2pHost(curvePriv, port, bus, crypto, logr)` 在 `0.0.0.0:<port>/tcp` 监听,装配:

| 层级 | 库 | 选项 |
|------|----|------|
| 传输 | `tcp.NewTCPTransport` | 单 TCP 端口 |
| 安全 | `noise.ID` + `noise.New` | Noise 协议,身份绑定到 ed25519 密钥 |
| 多路复用 | `yamux.DefaultTransport` | 流式多路复用,允许多个协议流共存 |

监听地址固定为 `/ip4/0.0.0.0/tcp/<port>`,**没有启用 IPv6 监听**。重启后若端口冲突,`libp2p.New` 会失败,`Start()` 返回错误。

### 6.3 `AttachFileEngine`

App 层在调用 `Start(port)` 之前先调用 `engine.AttachFileEngine(fileEngine)`。网络层在 `Start` 中检查此字段,若非 nil 才注册 `/parade/file-meta/1.0.0`、`/parade/file-download/1.0.0`、`/parade/browse/1.0.0` 三个处理函数并创建 `libp2pFile` 实例。

---

## 7. 连接生命周期 (Connection Lifecycle)

### 7.1 启动序列 `Start(port)`

```
1. 取出 crypto.GetPrivateKey();若为空 → 返回 "identity not loaded" 错误
2. NewLibp2pHost(priv, port, ...)  → 派生 ed25519,创建主机
3. 注册 NotifyBundle.ConnectedF / DisconnectedF
4. NewLibp2pChat(...)             → 创建 GossipSub,挂载 /parade/private-chat/1.0.0
5. 注入 chat.onPubkeyLookup / onAutoRegister / onPeerInfoReceived 回调
6. 挂载 /parade/identify/1.0.0 处理函数
7. chat.JoinTeam(crypto.TeamKeyHash())  → 订阅群聊主题
8. 若 fileEngine != nil: NewLibp2pFile + 挂载三协议
9. 挂载 /parade/handshake/1.0.0 处理函数
10. 挂载 /parade/test/1.0.0 处理函数
11. NewLibp2pSync + 挂载 /parade/sync/1.0.0
12. startIdentifyServer(port+1)  → 在 4328 启动明文 JSON 识别服务
13. e.started = true
14. go e.loadAndReconnect()       → 读取 ./.parade_peers 并尝试重连
```

### 7.2 3 阶段 `ConnectToPeer(ipAddress)`

`App` 层在收到前端"添加邻居"指令时调用,产出 `PeerConnectResult` 供 UI 显示每一阶段的状态。

**ASCII 时序图**:

```
Caller              libp2pEngine           Remote Peer (ip:4328)   Remote Peer (ip:4327)
  │                       │                          │                        │
  │ ConnectToPeer(ip)     │                          │                        │
  │──────────────────────>│                          │                        │
  │                       │  Phase 1: TCP 识别        │                        │
  │                       │  dial ip:4328             │                        │
  │                       │─────────────────────────>│                        │
  │                       │  JSON {peer_id,uuid,pub}  │                        │
  │                       │<─────────────────────────│                        │
  │                       │  parse JSON, derive addr  │                        │
  │                       │                          │                        │
  │                       │  Phase 1: libp2p dial     │                        │
  │                       │  host.Connect(/ip4/.../tcp/4327)                  │
  │                       │─────────────────────────────────────────────────>  │
  │                       │  Noise handshake + multistream-open              │
  │                       │<─────────────────────────────────────────────────  │
  │                       │  result.Phase1 = "正常"    │                        │
  │                       │                          │                        │
  │                       │  Phase 2: 队伍密钥挑战     │                        │
  │                       │  nonce = "parade-handshake-<uuid8>"                │
  │                       │  challenge = EncryptTeam(nonce)                    │
  │                       │  open /parade/handshake/1.0.0                     │
  │                       │─────────────────────────────────────────────────>  │
  │                       │  write challenge                                   │
  │                       │<─────────────────────────────────────────────────  │
  │                       │  remote: DecryptTeam → EncryptTeam → reply        │
  │                       │  read reply                                       │
  │                       │<─────────────────────────────────────────────────  │
  │                       │  DecryptTeam(reply) == nonce?                      │
  │                       │  result.Phase2 = "队伍相同" / "队伍密钥不匹配"      │
  │                       │                          │                        │
  │                       │  Phase 3: 测试消息         │                        │
  │                       │  open /parade/test/1.0.0                          │
  │                       │─────────────────────────────────────────────────>  │
  │                       │  write "test"                                     │
  │                       │<─────────────────────────────────────────────────  │
  │                       │  read 1-byte ack                                  │
  │                       │<─────────────────────────────────────────────────  │
  │                       │  result.Phase3Send / Phase3Recv                    │
  │                       │                          │                        │
  │                       │  成功: setPeer → savePeers → publish PeerJoined    │
  │<──────────────────────│                          │                        │
  │ PeerConnectResult     │                          │                        │
```

**阶段标签映射** (来自 `types.go` 与实际日志):

| 字段 | 中文 Label | 成功条件 |
|------|-----------|----------|
| `Phase1` | "无法连接" / "正常" | `host.Connect` 在 5 秒内成功 |
| `Phase2` | "队伍相同" | `DecryptTeam(reply) == nonce` |
| `Phase3Send` | "消息发送" / "消息已发送" | `Write("test")` 成功 |
| `Phase3Recv` | "收到消息" / "已收到对方确认" | `ReadFull(ack)` 读到 1 字节 |

### 7.3 身份持久化

`savePeers()` 序列化 `peerMap` 到 `./.parade_peers` (JSON,权限 0600):

```json
[
  {
    "peer_id": "12D3KooW...",
    "uuid": "f1a2b3c4-...",
    "pubkey": "BASE64_CURVE25519_PUB",
    "ip": "192.168.1.42"
  }
]
```

触发时机:
- `ConnectedF` 处理首次识别成功的远端 (异步 setPeer 后立即保存)
- `DisconnectedF` 移除条目 (保存)
- `Stop()` 主动保存
- `ConnectToPeer` 成功 (保存)
- 私聊/GossipSub 中 `onAutoRegister` / `onPeerInfoReceived` 触发 (保存)

`loadAndReconnect()` 在 `Start()` 后台协程中启动:
1. 跳过自己的 UUID
2. 对每条记录先把 pubkey 注入 `peerMap` (让 `ResolveUUID` 在重连前就能工作)
3. 优先按 IP 调用 `ConnectToPeer`;失败后回退到 `host.Connect(AddrInfo{ID: pid})` (让 libp2p 通过其他 peer 路由)

### 7.4 节点上下线通知

| 事件 | 触发点 | 载荷 |
|------|--------|------|
| `TopicPeerJoined` (`network:peer_joined`) | `ConnectedF` 识别完成 / `onAutoRegister` 私聊/GossipSub 首次接触 | `PeerEventPayload{PeerUUID, IPAddress}` |
| `TopicPeerLeft` (`network:peer_left`) | `DisconnectedF` | `PeerEventPayload{PeerUUID, IPAddress}` |
| `TopicMsgReceived` | GossipSub `consumeMessages` 成功解密 | `MsgReceivedPayload` (明文已解密) |
| `TopicPrivateMsgReceived` | `handlePrivateStream` 成功解密 | `MsgReceivedPayload` (明文已解密,`ReceiverID` 填本地 UUID) |
| `TopicConvSyncRequest` | `handleSync` 收到 `request` 或 `response` | `ConversationSyncPayload` |
| `TopicFileProgress` / `TopicFileCompleted` | file 层发布 (由网络事件驱动) | `FileProgressPayload` / `string` (TaskID) |

---

## 8. 传输安全 (Transport Security)

Parade 实施**三层加密**:

| 层 | 算法 | 用途 |
|----|------|------|
| 1. libp2p Noise | XX Noise 握手,ed25519 身份 | 所有 libp2p 流的传输层机密性与节点认证 |
| 2. libp2p Yamux | — | 多路复用 (无加密,但被 Noise 包裹) |
| 3a. 应用层 - 队伍 | AES-GCM (`EncryptTeam`) | GossipSub 群聊 payload / `/parade/handshake/1.0.0` challenge |
| 3b. 应用层 - 私聊 | Curve25519 ECDH + AES-GCM (`EncryptPrivate`) | 私聊内层 (嵌套在 `EncryptTeam` 之外) |

> **私聊双重封装** (`libp2p_chat.go`):
> 1. App 层 `crypto.EncryptPrivate(plain, remotePubKey)` → 私聊密文 A
> 2. 网络层 `crypto.EncryptTeam(A)` → 队伍密文 B
> 3. 通过 `/parade/private-chat/1.0.0` 发送 B
>
> 接收方反向:
> 1. `DecryptTeam(B)` → A
> 2. `DecryptPrivate(A, senderPubKey)` → 明文
>
> 第二层 (`EncryptTeam`) 的存在保证:在 GossipSub 群聊与私聊流中流通的字节流对**非本队成员**也是不可读的;只有本队成员才能解开第一层,再各自用对应 Curve25519 公钥解开第二层。

### 8.1 文件分块为何不重加密

`/parade/file-download/1.0.0` 上的 `pb.FileChunk.Data` 是**明文**字节,原因:
- 256KB 帧已经受 4 字节长度前缀保护
- libp2p Noise 流本身提供机密性 (传输层)
- 对大文件二次 AES-GCM 会显著增加 CPU 开销
- 队伍/私聊加密仅在元信息/信令层有意义,文件本体共享给全队成员

> 妥协: 队伍外的成员如果能截获 libp2p 流 (理论上不可能,因 Noise 端到端),仍能看到明文。**当前威胁模型假设攻击者无法降级 Noise**。

---

## 9. Protobuf 消息模式 (Protobuf Message Schemas)

### 9.1 `chat.proto` — `ChatService`

`package parade.chat.v1`,3 个 RPC:

```protobuf
service ChatService {
  rpc StreamChat(stream Envelope) returns (stream Envelope);
  rpc SyncMetadata(stream MetadataSyncRequest) returns (stream MetadataSyncResponse);
  rpc Handshake(HandshakeRequest) returns (HandshakeResponse);
}
```

| 消息 | 字段 | 说明 |
|------|------|------|
| `Envelope` | `sender_id` / `payload` (bytes,加密后) / `signature` / `type` (0=team, 1=private, 2-7=file-browse/meta/download, 8=peer-info, 9=heartbeat, 99=test) / `receiver_id` / `team_id` / `channel_id` | 统一消息外壳,**运行时未使用**;实际 GossipSub 载荷是裸加密字节 |
| `MetadataSyncRequest` | `sender_id` / `hlc` (int64) | 增量元数据同步请求 |
| `MetadataSyncResponse` | `sender_id` / `hlc` / `payload` (bytes) | 同步响应 |
| `HandshakeRequest` | `sender_id` / `team_challenge` (bytes) | 队伍密钥挑战 |
| `HandshakeResponse` | `remote_pubkey` / `team_match` (bool) / `team_reply` (bytes) | 挑战响应 |

> **运行时**:`/parade/handshake/1.0.0` 复用了 `HandshakeRequest.team_challenge` 语义但不再走 gRPC;挑战在请求侧产生,响应通过 `EncryptTeam(nonce)` 写出。`Handshake` RPC 在生成代码中存在,无调用方。

### 9.2 `file.proto` — `FileTransferService`

`package parade.file.v1`,3 个 RPC:

```protobuf
service FileTransferService {
  rpc DownloadFile(FileRequest) returns (stream FileChunk);
  rpc GetFileMeta(FileMetaRequest) returns (FileMetaResponse);
  rpc BrowseDirectory(BrowseRequest) returns (BrowseResponse);
}
```

| 消息 | 字段 | 运行时用途 |
|------|------|------------|
| `FileRequest` | `task_id` / `peer_id` / `file_path` / `offset` / `chunk_size` (默认 2MB) / `team_id` | gRPC 契约;运行时用 JSON `{task_id,file_path,offset}` 表达 |
| `FileChunk` | `task_id` / `peer_id` / `file_path` / `offset` / `data` (≤2MB) / `total_size` / `eof` / `file_hash` | **复用结构体,经 JSON 序列化传输** |
| `FileMetaRequest/Response` | `peer_id` / `file_path` / `team_id` → `file_path` / `total_size` / `file_hash` / `chunk_size` | gRPC 契约;运行时响应是简化 JSON `{file_path,total_size}` |
| `BrowseRequest/Response` | `peer_id` / `path` / `team_id` → `path` / `entries[]` | gRPC 契约;运行时响应是 `[]BrowseEntry` JSON |
| `BrowseEntry` | `name` / `path` / `is_directory` / `size` / `hash` | 目录条目;**运行时复用此类型** |

> **设计 vs 现实**:
> - `chunk_size` 字段运行时**不传**,固定 2MB 块大小 (`file` 包硬编码)。
> - `file_hash` 当前仅作占位 (BLAKE3 哈希由 file 层管理),运行时未填充到 `FileChunk`。
> - `team_id` 字段运行时**不参与授权** — 队伍隔离依赖 GossipSub 主题命名约定;`/parade/file-download/1.0.0` 对所有能连上的 peer 开放,前提是路径在共享根目录下。

---

## 10. 文件传输协议 (File Transfer Protocol)

完整流程源自 `FILE_TRANSFER_HANDOFF.md`,由 `libp2pFile.StartDownload` 实现,见 `libp2p_file.go:278-334`。

### 10.1 步骤详解

```
1. f.getFileMeta(peerID, remotePath)
   └─ open /parade/file-meta/1.0.0 stream
   └─ write UTF-8 path → read JSON {file_path, total_size, error?}
   └─ totalSize 必填

2. taskID = uuid.NewString()        (每次重试复用同一 taskID)
   peerIDStr = peerID.String()

3. PrepareDownload(ctx, taskID, localPath, peerIDStr, totalSize)
   └─ 若返回 ErrDownloadCompleted → 立即返回 nil (幂等)
   └─ 否则返回起始 offset (用于断点续传)

4. downloadChunks(ctx, peerID, ...)  // 单次尝试
   └─ open /parade/file-download/1.0.0 stream
   └─ write JSON {task_id, file_path, offset}
   └─ 循环 readMsgRaw → 4字节长度 + JSON pb.FileChunk
   └─ 检查 task_id 匹配 / eof / data 长度
   └─ f.fileEngine.SaveChunk(...) 落盘
   └─ 收到 eof=true 或 readMsgRaw 返 io.EOF → 返回 nil

5. 重试策略 (步骤 3+4 的外层循环)
   └─ 最多 3 次尝试
   └─ 失败间隔 600ms
   └─ 每次重试重新调 PrepareDownload (获得最新 offset)
   └─ context.Canceled / DeadlineExceeded → 立即终止,不再重试
```

### 10.2 ASCII 时序图

```
Client (libp2pFile)                 Server (libp2pFile.handleFileDownload)         file pkg
       │                                       │                                       │
       │  StartDownload(peerID, path, local)    │                                       │
       │  ─── 1. getFileMeta ────────────────> │                                       │
       │  write UTF-8 path                     │                                       │
       │  ─────────────────────────────────>   │  GetFileMeta(path)                    │
       │                                       │  ─────────────────────────────────>   │
       │  JSON {file_path, total_size}         │  ←────────────────────────────────  │
       │  <─────────────────────────────────   │                                       │
       │                                       │                                       │
       │  ─── 2. PrepareDownload ───────────── (calls file pkg directly) ───────────>  │
       │  PrepareDownload(taskID, local, peer, totalSize)                              │
       │  ────────────────────────────────────────────────────────────��─────────────>  │
       │  startOffset                                                                 │
       │  <──────────────────────────────────────────────────────────────────────────  │
       │                                       │                                       │
       │  ─── 3. open /parade/file-download/1.0.0 ─>                                  │
       │  write JSON {task_id, file_path, offset: startOffset}                         │
       │  ─────────────────────────────────>   │                                       │
       │                                       │  loop {                               │
       │                                       │    GetChunk(path, offset)             │
       │                                       │    ─────────────────────────────>    │
       │                                       │    chunk (≤2MB)                       │
       │                                       │    <─────────────────────────────     │
       │                                       │    writeMsg(FileChunk{task,offset,    │
       │                                       │              data,total_size})        │
       │  read 4-byte len + JSON FileChunk    │    ─────────────────────────────>     │
       │  <────────────────────────────────   │    offset += len(chunk)                │
       │  SaveChunk(task, local, data, offset, total)                                   │
       │  ──────────────────────────────────────────────────────────────────────────>  │
       │                                       │  }                                    │
       │                                       │  GetChunk → io.EOF                    │
       │                                       │  writeMsg(FileChunk{..., Eof: true})  │
       │  <────────────────────────────────   │  return                               │
       │  read eof=true → return nil          │                                       │
       │                                       │                                       │
       │  若失败 → retry (max 3, 600ms 间隔), 重新 PrepareDownload                       │
```

### 10.3 错误处理

| 错误 | 行为 |
|------|------|
| `context.Canceled` / `context.DeadlineExceeded` | 立即返回,**不重试** |
| 网络层瞬时错误 (流断开) | 计入重试计数,等 600ms 后重入 |
| `task_id` 不匹配 | 立即报错 (防止错位写入) |
| `ErrDownloadCompleted` | 视为成功 (幂等保护) |
| `total_size == 0` | 使用服务端 `FileChunk.total_size` 字段值 |
| 路径不在 `GetSharedRoots()` | 服务端拒绝,返回 `{error: "path not in shared directories"}` |

### 10.4 路径授权

服务端 `handleFileDownload` 与 `handleFileBrowse` 都会校验:

```go
cleanPath := filepath.ToSlash(filepath.Clean(req.FilePath))
allowed := false
for _, root := range sharedRoots {
    prefix := strings.TrimRight(filepath.ToSlash(root), "/") + "/"
    if strings.HasPrefix(cleanPath, prefix) || cleanPath == strings.TrimRight(prefix, "/") {
        allowed = true
        break
    }
}
```

即请求路径必须落在 `FileEngine.GetSharedRoots()` 至少一个根目录下;否则返回 `{error: "path not in shared directories"}`。

---

## 11. 关键设计观察 (Key Design Observations)

1. **`NetworkEngine` 接口在 `internal/app/interfaces.go` 中定义,共 14 个方法**。`libp2pEngine` 是唯一生产实现,`app_test.go` 中的 `MockNetwork` 满足所有 14 个方法。App 层永远不直接依赖 `libp2p*` 类型,只依赖此接口。

2. **gRPC 生成代码 ( `chat_grpc.pb.go` / `file_grpc.pb.go` ) 当前未被运行时调用**。它们存在是为了:
   - 保留 proto 文件作为接口契约的权威源
   - 未来若需要切换到原生 gRPC (或 gRPC-over-libp2p 之类),无需重写 wire format
   - 帮助开发者通过 proto IDE 插件理解消息字段

3. **端口 4327 / 4328 的语义在运行时已被重写**:
   - 4327: libp2p TCP 端口,所有协议流在此多路复用
   - 4328: 明文 TCP 识别服务,仅返回 JSON `{peer_id, uuid, pubkey}`,无加密
   - **没有任何 gRPC 流量使用 4328**

4. **`/parade/private-chat/1.0.0` 的 4 字节长度前缀上限 256KB**。超过此值直接丢弃,防止恶意 peer 推送大包耗尽内存。`/parade/file-download/1.0.0` 的 4 字节前缀理论上限是 4GB,实际块大小由 file 层固定 2MB。

5. **GossipSub 群聊 payload 完全由 `crypto.EncryptTeam` 决定**。`consumeMessages` 直接 `DecryptTeam(msg.Data)`,若失败整条消息丢弃。GossipSub 自身不保证顺序,但 HLC 字段在 `MsgReceivedPayload` 中,App 层按 HLC 排序后入库。

6. **`loadAndReconnect` 的双路径策略**是健壮性关键: 优先 IP 直连,失败后用 PeerID 让 libp2p 路由器通过其他已知 peer 中转。这覆盖了"目标 IP 变更但 PeerID 仍可达" (mDNS/NAT 重映射) 的场景。

7. **私聊与群聊的去耦**:
   - 群聊走 GossipSub (一对多,异步,最终一致)
   - 私聊走单播流 (一对一,同步确认 — 等待 1 字节 ACK)
   - 会话同步走单播流 (请求/响应)
   - 文件走单播流 (长连接,服务器流式)
   - 三类单播共用同一 Yamux 多路复用层,不会互相阻塞

8. **`extractIPFromPeer` 是 IPv4/IPv6 兼容的** — 它从 `multiaddr` 提取 IP,但 `NewLibp2pHost` 只监听 IPv4 `/ip4/0.0.0.0`,所以运行时实际拿到的都是 IPv4 地址。

9. **停止顺序** (`Stop()`): `identifyLn.Close()` → `chat.Close()` (取消订阅) → `host.Close()` (关闭所有 libp2p 流)。这三步按"上层先关"原则:先关入口,再关持久会话,最后关底层主机。`savePeers` 在释放锁后调用,避免 host 关闭竞态。

10. **`onAutoRegister` 与 `onPeerInfoReceived` 的差异**:
    - `onAutoRegister` 在 GossipSub/私聊中**首次**接触到一个 peer.ID 时触发,把 `pid` 绑定到 `SenderUUID`。它**不更新 pubkey**。
    - `onPeerInfoReceived` 在消息携带 `SenderPubKey` 时触发,**回填**此前空缺的 pubkey 字段,并尝试用 `msg.SenderIP` 建立直连。
    - 两者协作保证 `peerMap` 中的每个 peer.ID 最终都拥有 UUID + Curve25519 pubkey + IP 三元组。

11. **`.parade_peers` 的安全性**: 权限 0600,仅 owner 可读写。存储明文 UUID + pubkey + IP。**私钥从不落盘** — 私钥仅在内存中,经 Argon2 从口令派生,通过 `.parade_identity` 加密存储。

12. **libp2p 与 gRPC 的兼容性边界**: 当前所有运行时逻辑在 `libp2p_*.go`,**没有 `grpc_chat.go` / `grpc_file.go` / `discovery.go`**。`README.md` 中对这些文件的引用是历史遗留,应清理。
