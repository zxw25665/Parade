# 文件层模块说明

本目录实现“游行”项目的文件系统层第一阶段能力：  
**本地文件树生成 + 2MB Chunk 读取 + 断点进度存储（`file_logs`）**。

---

## 1. 当前职责范围

按照项目 `TODO.md` 中“2. 文件系统层详细设计与开发要求”，当前已落地：

1. **虚拟文件树生成**
   - 支持注册共享目录根：`ShareDirectory(absPath string) error`
   - 支持递归扫描并返回本地虚拟树：`GetLocalTree() ([]*FileNode, error)`
   - 兼容 `app.FileEngine`：`GetVirtualTree(rootPath string) (interface{}, error)`
   - 支持目录按需加载（单层）：`GetDirectoryChildren(absPath string) ([]*FileNode, error)`
   - 内置树缓存 + **文件系统事件驱动失效**（目录变化即失效）

2. **高速分块读取（Sender Side）**
   - 固定 Chunk 大小：`2MB`
   - 按偏移读取：`GetChunk(path string, offset int64) ([]byte, error)`
   - 使用 `sync.Pool` 复用 2MB 缓冲区，避免频繁大对象分配
   - 使用并发读限流（默认 4），降低磁盘 I/O 抖动风险

3. **完整性哈希**
   - `HashFile(path string) (string, error)`
   - 采用 `BLAKE3`（`github.com/zeebo/blake3`）
   - 按需缓存：基于 `文件绝对路径 + size + modTime` 命中缓存
   - 计算结果会写入 `db.file_logs`（`task_id = "hash:"+<hash>`）

4. **断点续传状态（Receiver Side）**
   - `PrepareDownload(...)`：读取或初始化 `file_logs`，返回起始 offset
   - `SaveChunk(...)`：写入 `.parade_tmp` 临时文件并更新 `file_logs`
   - 当文件完成时执行原子重命名：`.parade_tmp -> 目标文件`

5. **事件上报**
   - 下载中：发布 `eventbus.TopicFileProgress`
   - 下载完成：发布 `eventbus.TopicFileCompleted`

---

## 2. 核心数据结构

```go
type FileNode struct {
    Name     string
    Path     string
    IsFolder bool
    Size     int64
    Hash     string
    Children []*FileNode
}
```

---

## 3. 对外接口

- `NewEngine() *Engine`
- `WithDatabase(database db.Database) *Engine`
- `WithEventBus(bus eventbus.EventBus) *Engine`
- `ShareDirectory(absPath string) error`
- `UnshareDirectory(absPath string) error`
- `Close() error`
- `GetLocalTree() ([]*FileNode, error)`
- `GetVirtualTree(rootPath string) (interface{}, error)`
- `GetDirectoryChildren(absPath string) ([]*FileNode, error)`
- `GetChunk(path string, offset int64) ([]byte, error)`
- `HashFile(path string) (string, error)`
- `PrepareDownload(ctx, taskID, filePath, peerID, totalSize) (int64, error)`
- `SaveChunk(ctx, taskID, targetPath, peerID, data, offset, totalSize) error`

> `StartDownload(...)` 目前是接口占位，后续在“与 network 协议对接”阶段实现。
>
> 资源生命周期建议：`NewEngine -> ShareDirectory -> ... -> Close`。

---

## 4. 与其他层的关系

- **DB 层**：依赖 `db.Database` 的 `GetFileLog` / `UpsertFileLog` 维护断点。
- **EventBus 层**：仅发布进度与完成事件，不直接触达 UI/App 复杂逻辑。
- **Network 层**：当前尚未耦合协议细节；由后续阶段接入 chunk 收发协议。

---

## 5. 使用示例

```go
eng := file.NewEngine().
    WithDatabase(database).
    WithEventBus(bus)

_ = eng.ShareDirectory("D:/Share")
tree, _ := eng.GetLocalTree()

offset, _ := eng.PrepareDownload(ctx, "task-1", "D:/Downloads/a.zip", "peer-A", totalSize)
chunk, _ := eng.GetChunk("D:/Share/a.zip", offset)
_ = eng.SaveChunk(ctx, "task-1", "D:/Downloads/a.zip", "peer-A", chunk, offset, totalSize)
```

---

## 6. 已完成与待完成边界

### 已完成（本阶段）
- 本地文件树生成
- 固定 2MB chunk 读取
- 断点进度持久化 + 临时文件原子落盘
- 进度/完成事件发布

### 待后续阶段
- 与 `network` 文件传输协议对接（真正远端下载会话）
- 更细粒度的并发写控制、任务清理与失败重试策略
- 哈希缓存策略（按需计算并持久化）
