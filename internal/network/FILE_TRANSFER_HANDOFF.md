# 文件传输对接说明（network -> file）

本文档用于和文件层对齐文件下载协议与调用流程，目标是让 `StartDownload(...)` 尽快接入网络层。

## 当前状态

网络层已完成以下内容：

- 定义并落地 `proto/file.proto`。
- 生成 `internal/network/pb/file.pb.go` 和 `internal/network/pb/file_grpc.pb.go`。
- 在 `internal/network/grpc_file.go` 实现：
  - 服务端 `DownloadFile(...)`：按 `offset` 读取本地 chunk 并流式下发。
  - 客户端 `StartDownloadWithRetry(...)`：支持重试和断点续传编排。

## 协议约定（file.proto）

### FileRequest

- `task_id`：下载任务 ID，断点续传必须复用同一个 ID。
- `peer_id`：请求方节点标识。
- `file_path`：发送端本地文件路径（后续可升级为 file_id/hash）。
- `offset`：从哪个偏移开始拉取。
- `chunk_size`：期望块大小（默认 2MB）。

### FileChunk

- `task_id`：与请求保持一致。
- `peer_id`：发送方节点标识。
- `file_path`：对应文件路径。
- `offset`：该块在文件中的起始偏移。
- `data`：块数据。
- `total_size`：文件总大小（用于完成判断和落盘逻辑）。
- `eof`：流结束标记。
- `file_hash`：可选字段，后续用于完整性校验。

### RPC

- `rpc DownloadFile(FileRequest) returns (stream FileChunk)`
- `rpc GetFileMeta(FileMetaRequest) returns (FileMetaResponse)`

## 下载流程（网络层编排）

网络层客户端按以下步骤执行下载：

1. 调用 `PrepareDownload(ctx, taskID, localSavePath, peerID, totalSize)` 获取起始 `offset`。
2. 发起 `DownloadFile` 流请求（携带 `task_id + offset`）。
3. 每收到一个 `FileChunk`，调用 `SaveChunk(...)` 落盘。
4. 收到 `eof=true` 或达到 `total_size` 时结束。
5. 如果中途失败，按重试策略重新走第 1 步，自动续传。

## 文件层需要接入的内容

文件层的 `StartDownload(targetPubKey, virtualPath, localSavePath)` 当前是占位，实现时建议：

1. 先获取远端元信息（至少 `totalSize`，建议带 `file_hash`）。
2. 生成或接收 `taskID`（保证任务幂等）。
3. 调用网络层下载编排（当前为 `StartDownloadWithRetry(...)`）。
4. 将 `SaveChunk`、`PrepareDownload` 的错误完整返回到调用方。
5. 对 `ErrDownloadCompleted` 直接视为成功返回。

## 错误处理建议

- `context.Canceled` / `context.DeadlineExceeded`：立即结束，不再重试。
- 网络错误：有限次重试 + 固定或退避延迟。
- `task_id` 不一致或非法偏移：立即报错，防止写入错乱。
- `total_size == 0` 场景：优先使用服务端块中携带的 `total_size`。

## 待优化项（后续）

- 将 `file_path` 升级为稳定 `file_id/hash`，避免路径耦合。
- 引入块级校验（hash/校验和）以提升数据完整性保障。
- 增加下载任务状态查询与取消能力（便于 UI 控制）。
- 增加端到端集成测试（断线续传、重复任务、并发下载）。
