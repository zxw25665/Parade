# Parade (游行)

去中心化、端到端加密的局域网协作工具。文件共享、群聊、私聊——无需服务器、无需云端、无需互联网。

[English](README/en.md)

## 快速开始

```bash
go build -o parade ./cmd/parade/
./parade daemon --debug
```

需要 Go 1.26+。无 CGO，无外部依赖。

## 功能

- **群聊** — 基于 libp2p GossipSub 的加密群组消息
- **私聊** — ECDH 加密的一对一消息
- **文件共享** — 虚拟共享目录，2MB 分块传输，支持断点续传
- **离线优先** — 完全本地运行，节点重连时自动同步
- **无服务器** — 纯 P2P 局域网，无需注册、云端或任何基础设施

## 架构

```
cmd/parade/           CLI 入口（daemon、version）
├── main.go
└── daemon/
    ├── daemon.go         引擎组装、模式控制、信号处理
    └── lockfile.go       单实例锁（flock）

internal/
├── app/               业务编排层，JSON-RPC API（32 个方法）
│   ├── app.go             Register、Login、JoinTeam、SendTeamChat 等
│   ├── interfaces.go      NetworkEngine、FileEngine、Frontend 接口定义
│   ├── hlc.go             混合逻辑时钟生成器
│   ├── derived_id.go      确定性 UUID 派生
│   ├── jsonrpc.go         方法注册表（反射分发）
│   ├── uds_ui.go          UDS 广播推送到前端
│   └── uds_listener.go    UDS 接收循环 + JSON-RPC 分发
├── core/
│   ├── sync/              稀疏时间桶默克尔树同步算法
│   │   ├── timebucket.go      HLC → 桶路径推导
│   │   ├── merkle.go          默克尔树构建（BLAKE3）
│   │   ├── freeze.go          每日桶冻结、14 天窗口
│   │   ├── sync.go            逐层比较、双向交换
│   │   └── testdata.go        确定性数据集生成器
│   ├── eventbus/          内存异步发布/订阅
│   ├── crypto/            身份 + AES-256-GCM / Curve25519 / Argon2
│   └── db/                SQLite（WAL 模式，modernc.org/sqlite，无 CGO）
├── network/            libp2p P2P 网络层
│   ├── libp2p_engine.go     主机设置、对端管理
│   ├── libp2p_chat.go       GossipSub + 私聊流
│   ├── libp2p_connect.go    三阶段连接握手
│   ├── libp2p_file.go       文件元数据/下载/浏览
│   ├── libp2p_sync.go       传统线性 HLC 同步（回退方案）
│   ├── libp2p_merklesync.go 默克尔同步协议处理器
│   └── libp2p_host.go       libp2p 主机配置
└── file/               虚拟文件树、2MB 分块 I/O、BLAKE3 哈希
    ├── vfs.go、chunk.go、hash.go、chunk_tracker.go、transfer.go
```

### 数据流

```
前端（待定）←UDS/JSON-RPC→ parade 守护进程 ←EventBus→ 网络 / 文件 / 加密 / 数据库 / 同步
```

## CLI

```bash
parade daemon [选项]

  --headless     不启动 UDS 监听（自动化/CI）
  --debug        允许多实例、自定义 P2P 接口
  --production   强制 P2P 回环、单实例锁
  --data-dir     数据目录（默认 ./.parade_data）
  --uds          UDS 套接字路径（默认 /tmp/parade.sock）
  --port         P2P 监听端口（默认 4327）
  --listen       P2P 监听地址（默认 127.0.0.1）
```

## IPC 协议

基于 Unix 域套接字的 JSON-RPC 2.0，换行分隔。

```json
{"jsonrpc":"2.0","id":1,"method":"SendTeamChat","params":["hello"]}
{"jsonrpc":"2.0","id":1,"result":null}
{"jsonrpc":"2.0","method":"event","params":{"event":"ui_new_message","data":{...}}}
```

RPC 客户端：`tests/rpc_client.py`（Python 3，无额外依赖）。

## 同步算法

Parade 使用**稀疏时间桶默克尔树**进行会话同步：

```
第 0 层：年（YYYY）
第 1 层：月（YYYY-MM）
第 2 层：日（YYYY-MM-DD）
第 3 层：小时（YYYY-MM-DDTHH）
第 4 层：消息（单个 HLC）
```

- 仅在有消息的时间段创建桶（稀疏）
- 每层形成一颗 BLAKE3 哈希的默克尔树
- 同步时比较根哈希 → 不匹配则逐层下钻 → 小时级别双向交换
- 每日 00:00 UTC 冻结前一天的桶，避免重复比较
- 14 天窗口外的冻结桶可裁剪
- 默克尔同步失败时自动回退到传统线性 HLC 同步

协议：`/parade/merklesync/1.0.0`（6 种消息类型，30 秒超时）

## 测试套件

```bash
./tests/test_all.sh          # 30 项测试，6 个阶段
```

| 阶段 | 内容 | 数量 |
|------|------|------|
| 构建 | 编译二进制 | 1 |
| 单元测试 | `go test ./...`（7 个包） | 7 |
| 性能基准 | 9 个同步基准测试 | 9 |
| 正确性 | 3 节点/5 节点同步、边界情况 | 18 |
| 集群 | 5 节点集成测试（分区容错） | ~80 步 |
| 架构 | 文件存在性、模型、vet | 12 |

### 关键正确性测试

| 测试 | 验证内容 |
|------|----------|
| `Test3Node_DatasetA_FullSync` | 3 节点、500 条消息、1 会话、全网格 |
| `Test3Node_DatasetB_FullSync` | 3 节点、500 条消息、2 会话 |
| `Test3Node_PartialSync` | 100%/60%/30% 子集收敛 |
| `Test3Node_IdempotentSync` | 第二次同步零传输 |
| `Test5Node_ChainedSync` | 链式 0→1→2→3→4 收敛 |
| `Test5Node_StarSync` | 星型 0→1,2,3,4 收敛 |
| `Test5Node_GradualConvergence` | 20%-60% 子集、全网格 |
| `TestEmptySync` | 空会话同步 |
| `TestSyncWithFrozenBuckets` | 冻结桶被信任 |
| `TestSync_ContentTamperingDetection` | 篡改内容 → 根哈希不同 |
| `TestConcurrentSync_Safety` | 并发同步安全 |
| `TestLargeDataset_TreeSize` | 1 万条消息 → 77 个树节点 |

### 性能基准

| 基准 | 时间 | 内存 |
|------|------|------|
| 构建树（500 条消息） | ~500 µs | 266 KB |
| 3 节点全同步 | ~2.0 ms | 1.4 MB |
| 5 节点全同步 | ~3.9 ms | 2.8 MB |
| 计算消息哈希 | ~320 ns | 112 B |
| 桶哈希（100 子节点） | ~2.6 µs | 128 B |

## 技术栈

| 组件 | 选型 | 理由 |
|------|------|------|
| 语言 | Go 1.26 | 单二进制、交叉编译、优秀并发模型 |
| 数据库 | modernc.org/sqlite | 纯 Go、无 CGO、WAL 模式 |
| P2P | libp2p | 久经考验、协议流、NAT 穿透 |
| 加密 | AES-256-GCM、Curve25519、Argon2id、BLAKE3 | 标准、经过审计、无意外 |
| IPC | JSON-RPC 2.0 over UDS | 简单、可调试、语言无关 |
| 前端 | 待定（计划 Tauri/Vue3） | 旧 Wails 前端在 `deprecated/frontend/` |

## 关键约定

- **接口驱动**：所有引擎通过 `internal/app/interfaces.go` 中的接口定义
- **流式构建器**：`file.NewEngine().WithDatabase(d).WithEventBus(b)`
- **HLC 排序**：`2006-01-02T15:04:05.000Z_0001_NodeID` — 字典序可排序
- **幂等插入**：`ON CONFLICT(hlc) DO NOTHING` — 安全重传
- **BLAKE3**：同时用于文件哈希和默克尔树哈希
- **2MB 分块**：固定块大小，使用 `sync.Pool` 复用

## 开发

```bash
go build ./...                       # 构建所有包
go test ./...                        # 所有单元测试
go test -v -count=1 ./internal/core/sync/...  # 同步测试
go test -bench=. ./internal/core/sync/...     # 基准测试
./tests/test_all.sh                  # 完整测试套件
```

无 Makefile。仅使用标准 Go 工具链。

## 已知问题

- mDNS 节点发现尚未正常工作；节点通过显式 IP 连接
- 旧 Wails 前端在 `deprecated/frontend/` — 替代方案待定
- gRPC protobuf 存根在 `proto/` 中，为未来迁移预留
