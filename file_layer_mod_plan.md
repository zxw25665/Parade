# 文件系统层修改计划

## 背景

### Bug 1: SaveChunk 空洞文件风险

`SaveChunk` 当前用 `offset + len(data) >= totalSize` 判定下载完成。  
若 chunk 乱序到达（网络层 pipe-line 并发请求），靠后的 chunk 先写入，文件前半段存在空洞
（稀疏文件，全零字节），此时触发原子重命名将产出损坏文件。

### 设计文档指明的最终形态

> **断点续传与校验**："接收端在本地记录已下载的字节偏移量。如果局域网发生波动导致连接中断，
> 恢复连接后接收端只需请求未下载的剩余块。同时，每块数据附带校验码，确保组装后的文件 100% 完整无损。"

> **网络层 API**：`RequestFileChunk(peerID string, taskID string, offset int64)` — 粒度为单个 chunk。

> **数据面**：gRPC Server Streaming + 滑动窗口限流。

核心结论：接收端需要**逐块追踪**每个 chunk 的到达状态（而非只记一个 `transferred` 总数），
resume 时精确获知缺失的偏移量列表，只请求真正缺失的块。

---

## 方案：Bitset Chunk Tracker

### 新增文件

`internal/file/chunk_tracker.go`

```go
type ChunkTracker struct {
    chunkSize  int64
    totalSize  int64
    totalSlots int
    bitmap     []uint64    // bitset
    received   int         // 已收 slot 计数，O(1) 完成判定
    dirty      bool
}

func NewChunkTracker(totalSize int64) *ChunkTracker
func LoadChunkTracker(bitmapPath string, totalSize int64) (*ChunkTracker, error)
func (ct *ChunkTracker) MarkReceived(offset int64) (allDone bool, err error)
func (ct *ChunkTracker) IsComplete() bool
func (ct *ChunkTracker) MissingOffsets() []int64    // resume 用
func (ct *ChunkTracker) BytesReceived() int64       // 进度汇报用
func (ct *ChunkTracker) Save(path string) error     // 持久化 bitmap
```

**存储**：`.parade_tmp.bitmap` 侧边文件，与临时文件生命周期一致。  
**大小开销**：1GB 文件 → 64 bytes；100GB → 6.4KB。可忽略。

### 改动文件：`internal/file/transfer.go`

| 改动点 | 说明 |
|--------|------|
| `SaveChunk` 完成判定 | `offset+len >= totalSize` → `tracker.MarkReceived(offset) + IsComplete()` |
| `PrepareDownload` | 增加 `GetMissingChunks()` 方法供网络层 resume 时调用 |
| `runtimeState` | 增加 `chunkTrackers sync.Map` ([taskID]*ChunkTracker) |

### 对现有层的影响

| 层级 | 影响 |
|------|------|
| `chunk.go` | 不变 |
| `hash.go` | 不变 |
| `vfs.go` | 不变 |
| `file_test.go` | 补充 ChunkTracker 单元测试、SaveChunk 乱序到达测试 |
| DB `file_logs` | 不变（`transferred` 仍用于 UI 进度条） |
| `app.FileEngine` 接口 | 不变 |
| 新增 `chunk_tracker.go` | 纯逻辑，无外部依赖 |

### 实现步骤

1. 编写 `chunk_tracker.go`：bitset 核心逻辑 + 持久化
2. 修改 `transfer.go`：`runtimeState` 加 `chunkTrackers` map；`SaveChunk` 接入 tracker
3. 修改 `PrepareDownload` / 增加 `GetMissingChunks`：resume 时返回缺失偏移量列表
4. 补充测试：乱序写入测试、resume 测试、位运算边界（对齐/非对齐/单块的边界情况）

---

## 附录：进度追踪

- [x] chunk_tracker.go 实现
- [x] SaveChunk 接入 tracker
- [x] PrepareDownload + GetMissingChunks
- [x] 测试（乱序 + resume + 边界 + 重复幂等 + 跨 slot chunk）
- [x] `go test -race` 通过
