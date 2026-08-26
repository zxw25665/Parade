# 后端功能覆盖契约

本文档以当前 `README.md`、实际 JSON-RPC 注册表、现有 Go 测试和前端调用面为准，记录后端持续开发的可验证目标。

## 目标

“99% 功能完成”定义为：

- 37 个已注册 JSON-RPC 方法都有成功路径和主要错误路径测试；
- README 声明的群聊、私聊、文件共享/续传、离线同步和无服务器运行都有可观察测试；
- stdio JSON-RPC、网络协议和 daemon 生命周期都有至少一个真实边界测试；
- `go test ./...`、`go test -race ./...`、`go vet ./...` 和集成测试全部通过；
- 文档与实际注册数量、测试阶段和命令保持一致。

这不是代码覆盖率百分比，也不代表不存在未知缺陷。

## JSON-RPC 覆盖矩阵

注册位置：`internal/app/jsonrpc.go`。当前实际注册数量为 37 个。

| 领域 | 方法 | 当前证据 | 主要缺口 |
| --- | --- | --- | --- |
| 身份 | `CheckHasIdentity` | 仅旧 shell 测试 | 缺少 Go 应用层和错误路径测试 |
| 身份 | `Register`, `Login` | 应用/系统流程成功路径 | 已存在身份、错误密码、持久化失败 |
| 身份 | `Logout` | 无 | 状态清理和重复调用 |
| 团队 | `JoinTeam`, `JoinTeamWithName` | 部分系统流程 | 未登录、数据库失败、网络启动失败 |
| 团队 | `LeaveTeam`, `SwitchTeam`, `ListTeams`, `GetActiveTeam`, `GetPubKey` | `ListTeams` 仅旧 shell；`GetActiveTeam` 部分覆盖 | 状态切换、错误路径、未登录 |
| 聊天 | `SendTeamChat` | 成功路径和接收方测试 | 未登录、加密失败、发送失败 |
| 聊天 | `SendPrivateChat`, `StartPrivateConversation` | 无直接应用层测试 | UUID 解析、私聊加解密、数据库失败 |
| 聊天 | `ListConversations`, `GetConversationMessages` | 部分系统/旧 shell | 未登录、私聊解密、无效会话、分页边界 |
| Peer | `GetPeers`, `GetPeersWithStatus` | 无 | 空集合、状态映射、并发变化 |
| Peer | `ConnectToPeer` | 无 | 连接失败、阶段结果、端口配置 |
| Peer | `ListSavedPeers`, `SavePeer`, `RemovePeer` | 无 | 去重、持久化失败、删除不存在对象 |
| 生命周期 | `OnForeground` | 无 | 恢复连接、事件触发 |
| 文件 | `ShareDirectory`, `UnshareDirectory` | 只有文件引擎层测试 | 应用层鉴权、持久化、错误传播 |
| 文件 | `GetDirectoryChildren`, `GetRemoteDirectoryChildren` | 只有文件引擎层测试 | 路径穿越、空路径、网络失败 |
| 文件 | `StartDownload`, `GetDefaultDownloadDir` | 无 | 断点续传、目标目录、网络失败 |
| 共享组 | `CreateShareGroup`, `ListShareGroups`, `AddDirectoryToShareGroup` | 无 | CRUD 成功/失败、重复目录 |
| 共享组 | `RemoveDirectoryFromShareGroup`, `DeleteShareGroup`, `GetShareGroupDirs` | 无 | 不存在对象、级联关系、权限 |
| 日志 | `ExportLogs`, `WriteLogFile` | 无 | logger 缺失、不可写路径、内容完整性 |

## 传输层覆盖

`stdio_server_test.go` 已覆盖真实服务器生命周期、panic 隔离和在途请求排空；反射注册层由 `jsonrpc_contract_test.go` 覆盖。以下应用层错误路径仍需补齐：

- JSON 解析错误：`-32700`；
- 空方法：`-32600`；
- 未知方法：`-32601`；
- 参数数量错误、参数 JSON 类型错误；
- 应用方法返回错误的统一 JSON-RPC 映射。

`tests/test_cluster.sh` 已改为真实退出状态的 Go 进程内同步、libp2p wire 和 app 集成入口；它明确不是 daemon 进程级 E2E。win-64 使用 `tests/test_cluster.ps1`，避免依赖 bash 子进程的 PATH 行为。

## 多节点真实进程覆盖（分阶段）

上述集群门槛不启动 daemon 二进制。daemon 进程级多节点覆盖按以下阶段推进，详细步骤、端口分配与断言清单见 `docs/多节点实际测试方案.md`：

1. 单 daemon stdio 进程测试（现在可落地）：以普通 `--debug` 模式启动真实二进制，经标准输入/输出驱动 JSON-RPC 生命周期（身份、队伍、群聊、私聊、启停、信号退出），断言真实退出码与输出。不能使用 `--headless`，因为该模式不启动 IPC。不涉及多节点网络，确定性可复现，可纳入 `pixi run test-all` 的新阶段。
2. 同机多 daemon 测试（需先补前置改造）：当前对端地址不感知端口。`ConnectToPeer` 只接收 IP，identify 探测固定为本地端口 +1，P2P 拨号固定为本地端口（`/ip4/{ip}/tcp/{本地端口}`）；应用层保存的 peer 仅为 IP 字符串（`config.toml` 的 `[peers]`），断线重连同样按本地端口拨号。同机两个 daemon 必须使用不同 `--port`，因此先落地“地址感知的对端端点”：peer 持久化携带 ip:port，`ConnectToPeer` 与重连按保存的端口拨号。改造完成后，再做确定性的同机双 daemon 同步收敛、消息互达和文件传输断言。
3. mDNS：仅作可选诊断，不进入验证门槛。README 已知问题已注明部分网络环境需手动 `ConnectToPeer`；集群门槛固定 `SKIP_MDNS_TEST=1`，mDNS 启动失败只记 warning 不阻断（`internal/network/mdns.go`）。

验证门槛：阶段 1 以真实 stdio 生命周期脚本的退出码断言为准，失败必须可检出，沿用 `test_cluster.sh` 的失败可见约定；阶段 2 的前置改造先过 `go test -race`、`go vet` 与现有 cluster 门槛，再以双 daemon E2E 脚本断言握手、同步收敛与消息互达。剩余缺口：37 个 RPC 方法逐项成功/错误路径、同机之外的跨主机/跨网段多节点、mDNS 真实发现。

## 高风险后端清单

按优先级执行，每项都必须先有能复现问题的测试：

1. 网络输入边界：短 `ConvID`/UUID、空 `BucketPaths`、无界 JSON 读取和流读取超时；
2. Merkle/线性同步：真实双节点 wire 测试、并发 `last_hlc` 单调性、冻结桶对迟到消息的收敛性；
3. 文件传输：共享根校验、文件元数据越权、下载大小/哈希校验、跨进程断点续传；
4. 加密持久化：`.parade_teams` 应遵循 data-dir，保存错误必须传播；
5. logger/eventbus：`-race` 下的配置读写、关闭生命周期、取消期间事件丢失和超时行为；
6. CLI/daemon：真实 stdio 启停、信号退出、配置/端口/生产模式、Windows 可执行流程；
7. 应用层 RPC：先覆盖 21 个无任何测试证据的方法，再补齐已有方法的错误路径。

## 本轮已完成

- logger：拒绝非正容量，配置读写加锁，关闭后写入不再触发空文件访问；新增普通、race 和 vet 回归门槛。
- JSON-RPC：新增反射注册层参数校验、单/双返回值错误传播测试；新增真实注册表测试，确认当前注册数量为 37，并验证 `CheckHasIdentity` 的 handler 路径。
- 网络同步：新增真实双节点 wire 测试，修复线性同步短/空 `ConvID` 日志切片崩溃、Merkle sync 空 `BucketPaths` 索引崩溃，以及 Merkle 流半关闭、超时和消息上限问题。
- stdio/daemon：stdin EOF 不再绕过清理；在途请求会排空，handler panic 映射为 `-32603`，daemon 负责进程级退出。
- 持久化：队伍密钥和 peer 列表跟随 data-dir，保存错误向上传播；测试 fixture 使用独立临时目录。
- 测试入口：shell 集群脚本不再自我掩盖失败，win-64 提供 PowerShell 原生入口。

## 验证门槛

每个修复必须包含回归测试，并通过相关最小集合后才能合并：

```text
go test ./path/to/package -count=1
go test -race ./path/to/package -count=1
go vet ./...
go test ./...
```

阶段完成时再运行：

```text
pixi run test-race
pixi run test-all
pixi run test-backend
```

当前可验证门槛由 Go 包测试、真实 libp2p wire 测试、stdio 生命周期测试和 win-64 PowerShell 入口组成。daemon 进程级多节点覆盖按“多节点真实进程覆盖（分阶段）”推进：阶段 1 现在可落地，阶段 2 待对端端点端口感知改造完成；37 个 RPC 方法的逐项成功/错误路径仍是持续覆盖工作。
