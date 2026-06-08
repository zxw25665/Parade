# Parade 项目设计变迁记录

> 本文档基于 Git 历史记录（77 次提交，2026-03-30 至 2026-06-08）与项目设计文档交叉分析，梳理 Parade（游行）项目的架构演进过程。按时间阶段组织，每个阶段分析动因、技术选择、迁移得失与演进方向。

---

## 阶段一：项目萌芽 ——「确定骨架」(2026-03-30 ~ 04-13)

### 背景

项目启动于 2026 年 3 月 30 日，由多人在 GitHub 上协作。最初的 12 天主要完成了基础架构的搭建。

### 动因与目标

目标是快速建立一套**可并行开发的模块骨架**——允许不同成员独立实现网络层、文件层、应用层，最后组装。这决定了早期架构的松散耦合设计。

### 技术选择

| 模块 | 选型 | 动机 |
|------|------|------|
| 数据库 | `modernc.org/sqlite`（纯 Go，无 CGO） | 无外部依赖部署；桌面应用单文件数据库；WAL 模式支持并发读写 |
| 加密 | Argon2id + AES-256-GCM + Curve25519 | 提供三层密钥体系（Master/Team/Session），覆盖本地存储、局域网群聊、一对一私聊 |
| 事件总线 | 内存 pub/sub | 解耦模块间通信，异步推送 UI 事件 |
| 应用层 | 接口驱动的依赖注入 | 通过 `NetworkEngine`、`FileEngine`、`Frontend` 接口抽象，支持 Mock 测试和后期替换实现 |
| 桌面壳 | Wails v2（Go ↔ Vue3 IPC） | 单一技术栈（Go），前后端 IPC 自动生成 |

### 架构特点

```
Core 层（eventbus / crypto / db）── 基础原语，无上层依赖
App 层（app）── 通过接口调用 Network / File，无具体实现依赖
Network / File 层 ── 实现 App 层接口，通过 EventBus 推送事件
```

这种**接口隔离 + 事件驱动**的架构为后续的多次重大重构提供了关键弹性：App 层不需要知道 Network 层是 gRPC 还是 libp2p，只要实现 `NetworkEngine` 接口即可。

### 阶段结束状态

- 4 个核心模块就绪（eventbus、crypto、db、app）
- 可运行的单元测试（MockNetwork、MockFile、MockUI）
- 标准测试流程：Register → Login → JoinTeam → SendTeamChat
- 网络层和文件层仅有占位代码

---

## 阶段二：gRPC 网络架构 ——「中心化思想的局域网实现」(2026-04-22 ~ 04-28)

### 背景

团队需要让多个节点在局域网内发现彼此、建立连接、传输消息和文件。由于团队有 gRPC/protobuf 经验，且项目最初以「服务器/客户端」概念设计，选择了 gRPC 作为通信协议。

### 技术选择

| 组件 | 选型 | 动机 |
|------|------|------|
| 通信协议 | gRPC（HTTP/2） | 成熟的 RPC 框架；protobuf 强类型；双向流开箱即用 |
| 服务发现 | mDNS（`hashicorp/mdns`） | 零配置局域网发现；广播团队哈希实现同队过滤 |
| 控制面 | 端口 4327，gRPC bidi streaming | 单长连接承载聊天、信令、心跳、元数据同步 |
| 数据面 | 端口 4328，gRPC server streaming | 独立端口隔离大文件传输，避免队头阻塞 |
| 连接管理 | `ConnMgr` 集中管理（1240 行） | 统一处理 gRPC 连接生命周期、重连、保活 |

### 架构形态

```
Node A                                    Node B
  │  4327: ChatService (bidi)              │
  │  ←→ Envelope (type=0 team chat)        │
  │  ←→ MetadataSync (HLC catchup)         │
  │  → Handshake (team challenge)          │
  │                                         │
  │  4328: FileTransferService (server stream)
  │  → FileRequest                          │
  │  ← FileChunk × N                        │
  │                                         │
  │  mDNS: _parade._tcp 广播 team_hash      │
```

### 此阶段的优势

1. **开发速度快**：protobuf 生成 Go 代码，类型安全，IDE 自动补全
2. **调试友好**：gRPC 生态工具链成熟（grpcurl、protobuf inspector）
3. **概念清晰**：控制面/数据面端口分离，职责明确
4. **流式传输**：bidi streaming 天然适合聊天，server streaming 天然适合文件下载

### 此阶段的隐患

1. **单点连接**：每个节点对之间只有一条 gRPC 连接，`ConnMgr` 成为单点瓶颈。连接断开时所有通信中断，重连逻辑复杂（1240 行中大量代码处理重连和状态同步）
2. **端口绑定**：4327/4328 硬编码，多实例冲突，NAT 穿透困难
3. **mDNS 跨平台问题**：Windows 上的 mDNS 逻辑最终被**留空**（`2c471d6 fix: 留空了windows的mdns逻辑`），暴露了平台兼容性问题
4. **中央化思维**：gRPC 的客户端/服务器模式暗示层次结构，但 P2P 网络本质是平等的
5. **连接数爆炸**：N 个节点需要 N×(N-1) 条 gRPC 连接（双向），每增加一个节点连接开销线性增长

### 同期完成的文件层设计

文件层在此阶段确立了至今仍在使用的基础设计：
- 2MB 固定分块（`sync.Pool` 缓冲池）
- BLAKE3 内容哈希（两级缓存：mtime → 内容指纹）
- `ChunkTracker` 位图支持乱序到达（`slotMaxEnd` 精确字节覆盖追踪）
- `SaveChunk` 并发安全（per-task 互斥锁）
- 原子重命名 `<target>.parade_tmp` → `<target>` 作为下载完成点

这些设计在后续的 libp2p 迁移中**完全保留**，因为它们是纯文件层逻辑，与传输层无关。

---

## 阶段三：前端接入 ——「从纯后端到可交互应用」(2026-05-02 ~ 05-28)

### 背景

核心后端稳定后，团队接入 Vue3 前端，让项目成为一个可实际操作的桌面应用。

### 动因

- 需要一个可视化界面来验证后端功能
- Wails v2 提供了 Go↔JS IPC，前后端沟通零成本

### 技术选择

| 组件 | 选型 | 动机 |
|------|------|------|
| 框架 | Vue 3.5（Composition API） | 轻量，`<script setup>` 语法简洁 |
| 构建 | Vite 6 | 快速 HMR，Wails 兼容 |
| 状态 | `reactive()` 单例（无 Pinia） | 避免引入额外依赖；3 个 composable 覆盖全局状态 |
| 路由 | 无 | 固定三栏布局（身份/会话/文件/日志），不需要页面跳转 |
| 样式 | 手写 CSS（无框架） | 自定义暖色编辑风格主题 |
| 国际化 | vue-i18n（en/zh） | 轻量，localStorage 持久化 |

### 前端架构特点

- **IPC 双向绑定**：`useBackend.js` 包装所有 30 个 Go 方法，自动记录调用日志
- **事件消费者**：`useEvents.js` 订阅 8 个后端事件，归一化 snake_case/PascalCase
- **HLC 排序**：前端按 HLC 字符串字典序排序，依赖 Go 端的可排序格式
- **去重策略**：按消息 ID 去重，按 HLC 排序

### 前端设计变迁

1. **第一版**（5 月 5 日）：简单实现，功能可用但不美观
2. **第二版**（5 月 7 日）：文件层重新实现 + 新 UI
3. **第三版**（5 月 24 日）：**完整前端重设计**——这是本阶段最大的一次前端变更
4. **日志监控器**（5 月 28 日）：新增 `LogBroker` 回调，后端日志实时推送前端

### 遗留下的技术债务

- `PeerList.vue` 孤儿组件（存在但未挂载）
- `getRecentHistory` 过时调用（`IdentityPanel.vue` 调用但方法不存在）
- 部分组件绕过 `useBackend.js` 直接调用 `window.go.app.App.X`

---

## 阶段四：稳定化 ──「打补丁，修 Bug」(2026-05-28 ~ 06-01)

### 背景

前后端功能基本完整后，团队进入集中修复阶段。

### 修复范围

- **网络层**：`connmgr.go` 增加洪水去重（`recentHLCs` map 防止消息重复转发）
- **网络层**：Windows mDNS 逻辑留空（承认跨平台困难）
- **文件层**：端到端下载编排修复
- **EventBus**：per-topic goroutine 架构重构，数据竞争修复
- **聊天组件**：更新聊天 UI 组件
- **前后端联动**：多个衔接问题修复

### 信号

这一阶段的特点是**大量「fix」前缀提交**：`fix: major fix on network & file layer`、`fix: multiple issues, now this works at least`、`fix: update chat components`。这预示了 gRPC 网络架构的复杂度已经到了靠补丁维持的地步。

**关键信号**：`connmgr.go` 已经膨胀到 1240 行，增加的洪水去重逻辑（`recentHLCs map`、`recentHLCsMu`、`stopCh`）说明基本功能（消息不重复）需要额外机制来保证。这在 P2P 网络中本应是传输层自带的能力。

---

## 阶段五：libp2p 迁移 ——「从模拟 P2P 到真正 P2P」(2026-06-07 ~ 06-08)

### 背景

这是整个项目历史上**最大的一次架构重构**。在两天内，团队决定放弃 gRPC 网络架构，迁移到 libp2p。

### 动因分析

综合前几个阶段的信号，迁移的驱动力来自多个方面：

1. **gRPC 不是为 P2P 设计的**
   - gRPC 是客户端/服务器模型。让两个对等节点互相扮演客户端和服务器，导致每个节点对需要两条 gRPC 连接。这本质上是在 gRPC 之上「模拟」P2P。
   - `ConnMgr` 的 1240 行代码大部分在解决 gRPC 不擅长的问题：对等发现、双向认证、连接恢复、消息路由。

2. **NAT 穿透是现实需求**
   - 局域网内的桌面应用最终需要跨子网通信。gRPC 没有 NAT 穿透能力。libp2p 内置 hole punching、relay、AutoNAT。

3. **连接管理复杂度失控**
   - N 个节点需要 N×(N-1) 条 gRPC 连接。libp2p 的 GossipSub 使用单 topic 即可覆盖全部团队通信。
   - gRPC 的心跳和重连需要应用层实现（`ConnMgr` 中大量代码）。libp2p 自带连接管理和保活。

4. **mDNS 跨平台失败**
   - Windows 上的 mDNS 逻辑被**显式留空**（`2c471d6 fix: 留空了windows的mdns逻辑`），暴露了自建服务发现的脆弱性。
   - libp2p 内置多种发现机制（mDNS、DHT、rendezvous），且已处理跨平台兼容。

5. **多路复用的缺失**
   - gRPC 的设计意图是控制面 4327 + 数据面 4328 分离端口。但两个端口的独立管理、独立的连接失败处理、独立的重连逻辑，增加了整体复杂度。
   - libp2p 的 Yamux 在一个 TCP 连接上多路复用所有协议流。

### 迁移过程

**第一天（6 月 7 日）：**
- 删除 mDNS 服务发现代码（`servicebrowser.go` 等 4 个文件，512 行）
- 删除 `discovery_test.go`（240 行）
- 从 `go.mod` 移除 `hashicorp/mdns` 依赖
- 开始引入 libp2p 核心组件

**第二天（6 月 8 日）：**
- 一次性删除旧网络层全部代码（3,061 行）：
  - `connmgr.go`（1240 行）
  - `discovery.go`（293 行）
  - `grpc_chat.go`（625 行）
  - `grpc_file.go`（379 行）
  - 所有 `connmgr_*_test.go`（597 行）
  - 旧 `interfaces.go`（27 行）
- 一次性添加新 libp2p 网络层（1,869 行）：
  - `libp2p_engine.go`（592 行）—— 主机生命周期、身份持久化
  - `libp2p_host.go`（115 行）—— TCP + Noise + Yamux 工厂
  - `libp2p_connect.go`（185 行）—— 3 阶段对等连接
  - `libp2p_chat.go`（345 行）—— GossipSub 团队聊天 + 私聊流
  - `libp2p_file.go`（472 行）—— 文件元数据/下载/浏览协议
  - `libp2p_sync.go`（160 行）—— 对话同步协议
- 在 `go.mod` 中添加 `libp2p/go-libp2p v0.48.0` 和 `libp2p/go-libp2p-pubsub v0.16.0`
- 更新应用层接口（`interfaces.go`、`app.go`）适配新网络引擎 API
- 版本号升级：`v0.2.0-libp2p`

### 技术对比

| 维度 | gRPC 架构（旧） | libp2p 架构（新） |
|------|----------------|-------------------|
| 传输协议 | HTTP/2（gRPC） | TCP + Noise + Yamux |
| 身份认证 | 应用层握手（team key challenge） | 传输层 Noise（ed25519 绑定 Curve25519）+ 应用层握手 |
| 团队聊天 | gRPC bidi streaming | GossipSub（pub/sub） |
| 私聊 | gRPC bidi streaming 多路复用 | 专用 libp2p 流 `/parade/private-chat/1.0.0` |
| 文件传输 | gRPC server streaming（4328） | libp2p 流 `/parade/file-download/1.0.0`（同一 4327 端口） |
| 服务发现 | mDNS（`hashicorp/mdns`） | libp2p 内置发现 + TCP 识别服务器（4328） |
| 端口数量 | 2（4327 控制 + 4328 数据） | 1（4327，4328 仅作识别用途） |
| 代码行数 | 3,061（网络层） | 1,869（网络层） |
| 连接模型 | N×(N-1) 条 gRPC 连接 | 1 条 TCP 连接 per peer + GossipSub topic |
| NAT 穿透 | 无 | libp2p 内置 AutoNAT / hole punching |

### 迁移后的好处

1. **代码量减少 39%**：3,061 → 1,869 行。并不是功能减少，而是 libp2p 承担了连接管理、发现、安全传输的职责，不再需要应用层实现。
2. **单一端口**：4327 承载所有协议流（Yamux 多路复用），4328 降级为简单的识别辅助端口。消除了双端口的状态同步问题。
3. **GossipSub 替代 bidi streaming**：团队消息不再需要逐条发送给每个对等节点。一次 publish，GossipSub 完成全网广播。**连接数从 O(N²) 降为 O(N)**。
4. **传输层安全**：Noise 协议在传输层提供身份绑定加密，应用层不再需要为每个流手动加密头部。
5. **未来可扩展**：libp2p 支持 DHT、AutoNAT、hole punching、circuit relay，这些是 gRPC 架构无法触及的。
6. **App 层零改动**：由于 App 层通过 `NetworkEngine` 接口调用，接口变更仅限于方法签名调整，业务逻辑（消息流水线、事件订阅）完全不变。

### 迁移后的坏处

1. **proto 文件失去运行时意义**：`chat.proto` 和 `file.proto` 仍然存在于 `proto/` 目录中，生成的 gRPC 桩代码也在 `internal/network/pb/` 中，但**不再被运行时使用**。它们退化为「接口合约文档」。
2. **遗留代码碎片**：`proto/chat.pb.go` 是过时重复文件；`internal/network/README.md` 仍然描述 gRPC 架构（引用不存在的 `grpc_chat.go`）；`游行-设计书.md` 仍然描述 4327/4328 双端口方案。
3. **文档与实现脱节**：AGENTS.md 和 CLAUDE.md 中的网络层文件清单、端口描述均已过时。
4. **弃用的依赖**：`google.golang.org/grpc` 和 `google.golang.org/protobuf` 仍在 `go.mod` 中（proto 文件类型在文件传输中被复用），但如果未来文件传输协议也替换为纯 JSON，这些依赖将完全废弃。
5. **调试复杂度上升**：GossipSub 的消息传播不可预测（mesh 拓扑），调试时难以追踪消息路径。gRPC 的直接连接模型更容易理解。
6. **学习曲线**：libp2p 的概念密度远高于 gRPC（multiaddr、peerstore、host、protocol handler、GossipSub），新人上手成本增加。

### 为什么最终选择了 libp2p

决定性的因素是 **gRPC 架构与 P2P 网络的根本矛盾**：

- gRPC 的客户端/服务器模型要求一方主动发起连接。在 P2P 网络中，每对节点互为客户端和服务器，导致 **连接数爆炸** 和 **角色混淆**。
- libp2p 的 `host.Connect()` 抽象消解了这个问题：节点之间是平等的 `host`，连接建立后双方可以对称地打开流。
- 之前 1240 行的 `ConnMgr` 实际上是在 gRPC 之上**重新发明了 libp2p 已经提供的功能**：连接管理、保活、协议多路复用、对等路由。
- 团队在 6 月 7 日已经删除了 mDNS（`hashicorp/mdns`），说明对「自建基础设施」的信心已经动摇。libp2p 提供了经过考验的基础设施。

### 阶段结束状态

- 网络层完全重写（6 个 `libp2p_*.go` 文件，1,869 行）
- 版本号 `v0.2.0-libp2p`
- 旧网络代码全部删除，但 proto 文件和生成的 gRPC 桩代码保留（作为合约参考）
- 文件层、加密层、数据库层、EventBus、App 层核心逻辑**完全不变**

---

## 总体演进趋势

```
阶段一 (3/30-4/13)    阶段二 (4/22-4/28)     阶段三 (5/2-5/28)     阶段五 (6/7-6/8)
项目骨架              gRPC 网络               前端接入              libp2p 迁移
  │                     │                       │                     │
  ├─ DB (SQLite)        ├─ gRPC bidi/stream    ├─ Vue3 + Vite        ├─ 删除 3061 行 gRPC 代码
  ├─ Crypto             ├─ mDNS 发现           ├─ Wails IPC           ├─ 新增 1869 行 libp2p 代码
  │  (三层密钥体系)      ├─ ConnMgr (1240行)    ├─ 3 composable        ├─ GossipSub 替代 bidi
  ├─ EventBus           ├─ chat.proto          │  状态管理            ├─ Noise 提供传输安全
  │  (内存 pub/sub)     ├─ file.proto          ├─ i18n (en/zh)        ├─ 单端口 4327
  └─ App (接口驱动)     └─ File layer          └─ 日志监视器           └─ 版本 v0.2.0-libp2p
                         (2MB chunk + BLAKE3)

稳定层 (始终不变) ─────────────────────────────────────────────────────
  crypto / eventbus / db ── 从阶段一至今接口和实现完全兼容
  File layer 核心设计 ── chunk I/O / BLAKE3 / ChunkTracker 从阶段二沿用至今

废弃层 ─────────────────────────────────────────────────────────────────
  gRPC ConnMgr (1240行) ── 被 libp2p 取代
  mDNS servicebrowser ── 被 libp2p 内置发现取代
  proto 运行时使用 ── 退化为合约文档
```

### 核心设计原则的演变

| 原则 | 初期 | 当前 | 变化原因 |
|------|------|------|----------|
| 模块解耦 | 接口 + DI | 接口 + DI（不变） | 从一开始就是正确的架构选择 |
| 网络模型 | gRPC 客户端/服务器 | libp2p 对等主机 | P2P 网络不应使用客户端/服务器模型 |
| 服务发现 | mDNS 广播 | libp2p 内置发现 + TCP 识别 | Windows 兼容性 + 未来跨子网需求 |
| 消息广播 | gRPC bidi × N | GossipSub topic × 1 | O(N²) → O(N) 连接复杂度 |
| 传输安全 | 应用层 AES-GCM | 传输层 Noise + 应用层 AES-GCM | 双层防护，Noise 防元数据泄露 |
| 端口策略 | 双端口（4327+4328） | 单端口（4327 多路复用） | 简化部署，消除端口同步问题 |
| 状态管理 | Mock + DI | Mock + DI（不变） | 测试设计从一开始就是正确的 |

### 遗留的未解决问题

1. ~~**proto 桩代码的去留**~~ ✅ **已完成** (2026-06-08): 已删除 `proto/chat.pb.go`、`internal/network/pb/parade/` 陈旧重复代码。保留 `internal/network/pb/chatpb/` 和 `internal/network/pb/file*.go` 作为接口契约。
2. **文件传输协议**：目前文件传输使用 libp2p 流 + JSON 编码的 `pb.FileChunk`（复用 proto 类型）。如果未来完全脱离 proto，需要定义纯 JSON 文件传输协议。
3. ~~**文档同步**~~ ✅ **已完成** (2026-06-08): 已创建 `游行-设计书v2.md` 全面反映 libp2p 架构；已清理 `proto/chat.pb.go` 和孤儿组件 `PeerList.vue`；AGENTS.md 和 CLAUDE.md 更新待后续完成。
4. **跨子网验证**：libp2p 的 NAT 穿透和 relay 能力尚未在实际多子网环境中验证。
5. **GossipSub 调优**：心跳参数、mesh 大小、消息缓存窗口需要根据实际部署规模调优。

---

## 阶段六：残旧遗物清理 ——「打扫战场」(2026-06-08)

### 背景

在各模块设计文档的交叉审查中，识别出一批完全脱离项目运行时的残留文件。这些文件不参与编译、不参与运行时、不参与渲染，是 v0.1 (gRPC) → v0.2 (libp2p) 迁移中遗落的碎片。

### 清理清单

| 序号 | 文件 | 性质 | 判定依据 |
|------|------|------|---------|
| 1 | `proto/chat.pb.go` | 陈旧重复副本 (13,532 字节) | 实际编译使用 `internal/network/pb/chatpb/chat.pb.go` (`chatpb` 包)，此文件与项目编译完全无关，import 路径不一致 |
| 2 | `frontend/src/components/PeerList.vue` | 孤儿组件 (4,873 字节) | 未在 `App.vue` 中挂载，不被任何组件 import。使用旧版 4 阶段连接 UI，已被 `PeerStatus.vue` 取代 |
| 3 | `internal/network/pb/parade/` | 陈旧重复生成代码 | AGENTS.md 已标注 "ignore it"，磁盘上已不存在（此前已清理） |

**验证**: 删除后 `go build ./...` 通过，无任何编译错误。

### 同步更新

- 创建 `reports/游行-设计书v2.md`，全面反映 libp2p 运行时现实：
  - 网络: libp2p TCP+Noise+Yamux (端口 4327)，GossipSub 群聊，7 条自定义协议流
  - 端口: 4327 (全部多路复用) + 4328 (明文 TCP 识别)
  - 前端: Vue3 `reactive()` 单例存储，无 Pinia，无 vue-router
  - 传输安全: Noise 传输层 + 应用层三层 AES-GCM 加密
  - 新一等模块: Logger (异步 JSONL 环形缓冲)
  - HLC 格式: `2006-01-02T15:04:05.000Z_0001_NodeID`
  - 附录依赖清单: libp2p v0.48.0 / GossipSub v0.16.0 / 移除 hashicorp/mdns 与 memberlist
- 文档标注 proto 文件作为“接口契约”保留，gRPC 生成代码运行时未使用

---

> **编写方法**：本文档通过分析 77 次 Git 提交的时序、diff 内容和 commit message，结合已完成的 7 篇模块设计文档，交叉比对得出各阶段的演进动因和技术决策。