# Parade File 层架构设计文档

> 路径: `internal/file/`
> 范围: 本地虚拟文件树、2MB 分块 I/O、BLAKE3 哈希、断点续传协议
> 状态: v0.2.0-libp2p

## 1. 目的（Purpose）

`internal/file` 是 Parade 项目的**文件基础设施层**。它向上对 App 层暴露"虚拟文件树 + 分块读写 + 断点续传"三组能力，向下仅依赖 Core 层的 `db.Database` / `eventbus.EventBus` / `logger.Logger` 三个接口。它的核心使命可归纳为四点:

1. **虚拟文件树 (VFS)**: 把多根本地目录聚合成一个排序的 JSON 可序列化树，供前端直接渲染。内置 `fsnotify` 监听器，目录变动自动失效缓存。
2. **2MB 分块读取**: 固定 `DefaultChunkSize = 2 * 1024 * 1024` 的分块粒度。`sync.Pool` 复用 2MB 缓冲，4 路并发读限流避免磁盘抖动。
3. **BLAKE3 内容哈希 + 两级缓存**: 基于 `(size, mtime)` 粗筛，再以"头/中/尾 32KB 采样"做细筛，命中后直接返回十六进制摘要。摘要持久化到 `file_logs` 表，跨进程复用。
4. **断点续传协议 (Resumable Download)**: 支持乱序到达的 `SaveChunk`，通过 `ChunkTracker` 双层结构 (bitmap + per-slot maxEnd) 跟踪每个 slot 的精确覆盖字节数，完成时原子重命名 `.parade_tmp → target`。

**依赖方向**:

```
App 层  ←持有→  file.Engine (interface via app.FileEngine)
                      │
                      ├─→ Core: db.Database  (file_logs / shared_directories)
                      ├─→ Core: eventbus.EventBus  (file:progress / file:completed / fs:dir_changed)
                      ├─→ Core: logger.Logger
                      ├─→ fsnotify  (文件监听)
                      └─→ zeebo/blake3  (内容哈希)
```

App 层不直接 `import` `internal/file` 的具体类型，而是依赖 `app.FileEngine` 接口 (`internal/app/interfaces.go`)。所有测试通过 `MockFile` 实现该接口。

---

## 2. 文件清单（File Inventory）

| 文件 | 行数 | 职责 |
|------|------|------|
| `vfs.go` | 519 | `Engine` 顶层结构、`FileNode` 树节点、`ShareDirectory/UnshareDirectory`、`fsnotify` watcher 生命周期、树缓存与哈希缓存失效 |
| `transfer.go` | 491 | `runtimeState` 全部运行时字段、`WithDatabase/WithEventBus/WithLogger` 链式构造、`PrepareDownload`、`SaveChunk` 核心写盘流程、`ChunkTracker` 内存缓存、事件发布、`CleanupStaleTempFiles` 维护任务 |
| `hash.go` | 154 | `HashFile` (BLAKE3)、`fileFingerprint` (3×32KB 采样)、`hashCacheEntry` 两级缓存、`persistHashToFileLog` 持久化 |
| `chunk.go` | 87 | `GetChunk` (2MB 读)、`GetFileMeta` 元信息查询、常量 `DefaultChunkSize` |
| `chunk_tracker.go` | 243 | `ChunkTracker` 双层结构、`MarkReceived` (跨 slot)、`MissingOffsets/MissingRanges`、`Save/LoadChunkTracker` 二进制持久化 |

测试文件:

| 文件 | 行数 | 说明 |
|------|------|------|
| `file_test.go` | 700 | 19 个 `Test*` 函数，覆盖 VFS 缓存、`fsnotify` 失效、GetChunk、HashFile (3 用例)、PrepareDownload+SaveChunk、ChunkTracker (7 用例)、乱序 SaveChunk (3 用例) |

测试均使用 `t.TempDir()` 隔离，部分测试会创建 `.parade_tmp` / `.parade_tmp.bitmap` 文件验证磁盘副作用。

---

## 3. 核心结构（Core Structures）

### 3.1 顶层 `Engine` 与 `runtimeState`

`Engine` 本身只持有"共享根集合 + runtime 指针"两件元信息，所有可变状态都在 `runtimeState` 中:

```go
// vfs.go
type Engine struct {
    mu          sync.RWMutex
    sharedRoots map[string]struct{}
    runtime     *runtimeState
}

// transfer.go
type runtimeState struct {
    database      db.Database
    bus           eventbus.EventBus
    logr          logger.Logger
    chunkPool     sync.Pool               // 2MB byte slice 复用
    readLimiter   chan struct{}            // cap 4 = 4 路读并发限流
    cacheMu       sync.RWMutex
    treeCache     map[string]treeCacheEntry
    hashCache     map[string]hashCacheEntry
    taskLocks     sync.Map                 // map[string]*sync.Mutex
    chunkTrackers sync.Map                 // map[string]*ChunkTracker
    watchMu       sync.Mutex
    watchers      map[string]*rootWatcher
}
```

`Engine.mu` (RWMutex) 保护 `sharedRoots` 与 `runtime` 字段本身。其余所有字段都被各自专门的锁保护 (`cacheMu` / `watchMu`)，或通过 `sync.Map` 的无锁语义访问 (`taskLocks` / `chunkTrackers`)。

### 3.2 `FileNode`: 虚拟树节点

```go
// vfs.go
type FileNode struct {
    Name     string      `json:"name"`
    Path     string      `json:"path"`
    IsFolder bool        `json:"is_folder"`
    Size     int64       `json:"size"`
    Hash     string      `json:"hash"`       // 由 HashFile 按需填充
    Children []*FileNode `json:"children,omitempty"`
}
```

**关键特性**:

- 全字段都有 `json` tag，可直接 `json.Marshal` 推给 Vue3 前端。
- `Hash` 字段留空，前端可在 idle 时按需调用 `HashFile` 补齐，避免阻塞树构建。
- `Children` 在叶子节点省略 (`omitempty`)，节省序列化字节。
- 树构建时 `buildTreeFromEntry` 递归扫描，**子节点排序规则: 文件夹优先 → 名称小写字母序**。

**缓存不可变性**: 缓存中存储的 `FileNode` 是**深拷贝** (`cloneNode`)，外部修改返回的节点不会污染缓存。`getOrBuildTree` 命中缓存后总是 `cloneNode(cache.node)`，外部修改后下一次命中仍是旧值。

### 3.3 `ChunkTracker`: 双层追踪结构

```go
// chunk_tracker.go
type ChunkTracker struct {
    totalSize   int64
    totalSlots  int            // ceil(totalSize / DefaultChunkSize)，最小 1
    bitmap      []uint64       // bitset：1 bit per slot (0=missing, 1=received)
    slotMaxEnd  []int64        // per-slot：此 slot 内已写入的最大 end 偏移
    totalUnique int64          // 精确的独特字节覆盖数
    received    int            // 被 touch 过的 slot 数
}
```

**为什么需要双层?**

- 仅靠 `bitmap` 只能知道"这个 slot 有没有收到过数据"，**无法表达 partial chunk**。例如 2MB 的 slot 1 收到了前 1MB，bitmap 已经置位但实际只覆盖一半。
- `slotMaxEnd[i]` 记录该 slot 内**已写入的最大 end 偏移**，结合 `slotStart` 即可算出精确覆盖字节数。
- `totalUnique = Σ max(0, slotMaxEnd[i] - slotStart)`，就是 `BytesReceived()` 的返回值，用于单调递增的进度上报。
- `received = popcount(bitmap)`，是 `MissingOffsets()` 的预计算分母。

**乱序正确性**: 即使 `SaveChunk` 调用顺序为 slot 5 → slot 3 → slot 0，每次 `MarkReceived` 都基于"该 slot 内当前已写入的最大 end 位置"做增量计算。`totalUnique` 始终单调递增，**绝不会因为晚到的 chunk 而回退**。

---

## 4. 链式构造器（Fluent Builder）

所有依赖通过 `WithXxx` 注入，返回 `*Engine` 自身以支持链式调用:

```go
eng := file.NewEngine().
    WithDatabase(database).
    WithEventBus(bus).
    WithLogger(logr)
```

**关键点**:

- 三个 `With*` 方法都在 `Engine.mu` 写锁下修改 `runtime`。如果调用顺序早于 `NewEngine` 内部隐式创建了 runtime，则 lazy init。
- `NewEngine()` 已经会 `newRuntimeState()`，所以 `WithXxx` 通常只是赋值。
- 三个注入方法**不返回 error**，依赖为空时仅是不生效，调用方需在真正用到时检查 `getDB()` / `getRuntime()` 返回值。
- `getRuntime()` 用 `Engine.mu` 读锁保护，保证并发读写 runtime 指针安全。

`newRuntimeState()` 初始化三件基础设施:

```go
chunkPool: sync.Pool{
    New: func() interface{} { return make([]byte, DefaultChunkSize) },
},
readLimiter: make(chan struct{}, 4),  // 4 路并发读
treeCache:   make(map[string]treeCacheEntry),
hashCache:   make(map[string]hashCacheEntry),
watchers:    make(map[string]*rootWatcher),
```

---

## 5. 完整对外 API（Public API）

### 5.1 VFS / 目录管理

| 方法 | 签名 | 行为 |
|------|------|------|
| `ShareDirectory` | `(absPath string) error` | 规范化路径 → 加入 `sharedRoots` → 启动 watcher → 失效树缓存 → 写 `shared_directories` 表 |
| `UnshareDirectory` | `(absPath string) error` | 从 map 移除 → 失效树缓存 → 停止 watcher → 删 `shared_directories` 表 |
| `LoadSharedDirectories` | `() error` | 启动时从 DB 恢复所有共享根，对存在的目录重新启动 watcher；不存在的目录 (例如 U 盘拔出) 静默跳过 |
| `GetSharedRoots` | `() []string` | 快照 `sharedRoots`，无序 |
| `GetLocalTree` | `() ([]*FileNode, error)` | 排序后所有共享根的递归扫描，`getOrBuildTree` 命中即返回缓存 |
| `GetVirtualTree` | `(rootPath string) (interface{}, error)` | 适配 `app.FileEngine` 接口；空字符串退化为 `GetLocalTree` |
| `GetDirectoryChildren` | `(absPath string) (interface{}, error)` | **单层 lazy 扫描**，不递归；适配前端按需展开 |
| `Close` | `() error` | 停止所有 watcher，关闭 fsnotify 句柄 |

**`ShareDirectory` 的失败回滚**: 如果 `ensureRootWatcher` 失败，已加入 `sharedRoots` 的条目会被回滚删除，保证内存与 DB 状态一致。但 `watcher` 启动失败时 `InsertSharedDirectory` **不会**回滚，调用方需根据 `error` 决定是否手动 unshare。

### 5.2 分块 I/O

| 方法 | 签名 | 行为 |
|------|------|------|
| `GetChunk` | `(path string, offset int64) ([]byte, error)` | 打开文件 → stat 校验 → 占用读限流槽 → `sync.Pool` 取 2MB 缓冲 → `ReadAt` → 复制**精确长度**返回 |
| `GetFileMeta` | `(path string) (os.FileInfo, error)` | stat，拒绝目录 |
| 常量 `DefaultChunkSize` | `= 2 * 1024 * 1024` | 固定分块大小 |

**`GetChunk` 行为细节**:

- `offset >= info.Size()` 时返回 `io.EOF`，不分配缓冲。
- 返回的 `[]byte` **是紧密尺寸的 copy** (不是从 pool 借出的 buffer)，调用方无需担心 buffer 复用导致的数据竞争。
- `sync.Pool` 的 buffer 仅作为 `ReadAt` 的暂存读出区，函数退出前 `defer releaseChunkBuffer` 归还。
- `readLimiter` 的 4 路并发上限避免了多 peer 同时请求 chunk 时把磁盘 IO 打满。

### 5.3 哈希

| 方法 | 签名 | 行为 |
|------|------|------|
| `HashFile` | `(path string) (string, error)` | 两级缓存 → BLAKE3 全量哈希 → 写 `file_logs` (task_id=`hash:<hex>`) |

详见 §7。

### 5.4 下载协议

| 方法 | 签名 | 行为 |
|------|------|------|
| `PrepareDownload` | `(ctx, taskID, filePath, peerID, totalSize) (int64, error)` | 决策树见 §6 |
| `SaveChunk` | `(ctx, taskID, targetPath, peerID, data, offset, totalSize) error` | 核心写盘流程，见 §6 |
| `GetMissingChunks` | `(ctx, taskID, targetPath) ([]int64, error)` | 查询 `file_log` → 加载/创建 tracker → `MissingOffsets()` |

**特殊错误**:

```go
var ErrDownloadCompleted = errors.New("download already completed")
```

调用方通过 `errors.Is(err, ErrDownloadCompleted)` 判定可跳过下载流程。

### 5.5 维护

| 方法 | 签名 | 行为 |
|------|------|------|
| `CleanupStaleTempFiles` | `()` | 遍历所有共享根，删除 `*.parade_tmp` 和 `*.parade_tmp.bitmap` 孤立文件 |

启动时与网络层重连时调用，清理上一会话中断留下的脏文件。

---

## 6. 断点续传协议详解（Resumable Download Protocol）

这是 File 层的**最复杂部分**。本节给出完整的状态机与并发模型。

### 6.1 文件系统制品生命周期

每次下载会涉及 0~3 个磁盘文件:

| 文件 | 存在时机 | 何时消失 |
|------|---------|---------|
| `<target>.parade_tmp` | 下载中持续写入 | 完成时 `os.Rename` 到 target |
| `<target>.parade_tmp.bitmap` | 第一次 `SaveChunk` 后创建 | 完成时 `os.Remove` |
| `<target>` (最终文件) | **仅完成时**存在 | 永远不会自动消失 |

时序图:

```
t=0:  PrepareDownload → 创建 file_log (Transferred=0, Status=Transferring)
t=1:  SaveChunk #0 (offset=0)
      ├─ 写入 <target>.parade_tmp @ 0
      ├─ 创建 <target>.parade_tmp.bitmap
      └─ 写 file_log (Transferred=2MB, Status=Transferring)
t=2:  SaveChunk #1 (offset=2MB)
      ├─ 写入 <target>.parade_tmp @ 2MB
      ├─ 更新 <target>.parade_tmp.bitmap
      └─ 写 file_log (Transferred=4MB, Status=Transferring)
...
t=N:  SaveChunk #K (offset=最后一 chunk)
      ├─ 写入 <target>.parade_tmp @ end
      ├─ 更新 bitmap → IsComplete() == true
      ├─ 写 file_log (Transferred=TotalSize, Status=Completed)
      ├─ os.Remove(.parade_tmp.bitmap)
      ├─ cleanupTracker(taskID)   // 从内存中释放
      └─ os.Rename(.parade_tmp, target)   // 原子提交
```

### 6.2 `PrepareDownload` 决策树

```
┌────────────────────────────────────────────────────────────┐
│  GetFileLog(taskID)                                        │
└──────────┬─────────────────────────────────────────────────┘
           │
           ├── nil ──► 创建新 log (Transferred=0) ──► return 0
           │
           ├── Status==Completed ──► return TotalSize, ErrDownloadCompleted
           │
           ├── Status==Transferring
           │     │
           │     ▼
           │   LoadChunkTracker(.parade_tmp.bitmap, TotalSize)
           │     │
           │     ├── OK + MissingOffsets() 非空 ──► return missing[0]    ★
           │     │
           │     ├── OK + IsComplete() == false ──► return BytesReceived() ★
           │     │
           │     ├── OK + IsComplete() == true  ──► return TotalSize
           │     │
           │     └── LoadError (bitmap 不存在) ──► return log.Transferred
           │
           ▼
```

**★ 关键点**: 当 bitmap 存在时，**永远以 bitmap 为准**，不用 `log.Transferred`。原因:

`log.Transferred` 是 `BytesReceived()` 某次 SaveChunk 时的快照。如果 chunk 5 先到、chunk 3 后到，第一次 `SaveChunk` 写入 `Transferred=12MB` 看似已传 12MB，但实际覆盖只有 slot 0,1,2 (bitmap `received=3`，totalUnique=6MB)。第二次 `SaveChunk` 才会更新为 6MB+2MB=8MB。**只用 `Transferred` 选起点会跳到 missing slot 之后，造成那个 slot 永远收不到。**

### 6.3 `SaveChunk` 流程

```
per-task lock (taskLocks.LoadOrStore)        // 同 task 串行化
        │
        ▼
mkdir -p <target_dir>
        │
        ▼
OpenFile(<target>.parade_tmp, O_CREATE|O_RDWR)
WriteAt(data, offset)
Close
        │
        ▼
getOrCreateTracker(taskID, .parade_tmp.bitmap, TotalSize)
        │  ├─ chunkTrackers cache hit → 复用
        │  ├─ LoadChunkTracker(bitmapPath) OK → 缓存
        │  └─ 都不存在 → NewChunkTracker(TotalSize) → 缓存
        ▼
MarkReceived(offset, len(data))
        │  跨 slot 处理 (尾部 partial chunk 跨 slot 边界)
        │  幂等：bitmap 已置位的不重数
        ▼
Save(bitmapPath)                             // 持久化双层结构
        │
        ▼
UpsertFileLog(Transferred = BytesReceived(), Status)
        │
        ▼
publishProgress(taskID, filePath, Transferred, TotalSize)
        │
        ▼
if IsComplete():
   ├─ os.Remove(bitmapPath)
   ├─ cleanupTracker(taskID)                 // chunkTrackers.Delete + taskLocks.Delete
   ├─ os.Rename(.parade_tmp → target)        // 失败重试一次 (delete + rename)
   └─ publishCompleted(taskID)
```

### 6.4 乱序 SaveChunk 的正确性

假设 6MB 文件 (3 个 slot)，按 [slot 1, slot 0, slot 2] 顺序到达 2MB chunk:

| 步骤 | 事件 | bitmap | slotMaxEnd | BytesReceived |
|------|------|--------|-----------|---------------|
| init | NewChunkTracker(6MB) | [0,0,0] | [0,0,0] | 0 |
| 1 | SaveChunk(offset=2MB, 2MB) | [0,1,0] | [0,4MB,0] | 2MB |
| 2 | SaveChunk(offset=0, 2MB) | [1,1,0] | [2MB,4MB,0] | 4MB |
| 3 | SaveChunk(offset=4MB, 2MB) | [1,1,1] | [2MB,4MB,6MB] | 6MB ✓ |

**关键性质**:

- `totalUnique` 在每一步都**单调递增** (2MB → 4MB → 6MB)，进度条不倒退。
- `received` 在每一步也单调递增 (1 → 2 → 3)，用于 `MissingOffsets` 的快速判定。
- 第三次 `SaveChunk` 触发 `IsComplete()=true`，自动走 finalize 流程。

**跨 slot chunk 的边界处理** (例如 5MB 文件，最后一 chunk 仅 1MB):

```
5MB 文件: totalSlots = 3
slot 0: [0, 2MB)
slot 1: [2MB, 4MB)
slot 2: [4MB, 5MB)  ← 仅 1MB

SaveChunk(offset=0, 5MB)   // 单个 chunk 覆盖全文件
  → startIdx=0, endIdx=2
  → 三个 slot 都被 touch
  → slotMaxEnd 累加时考虑 slot 边界
  → 正确得到 totalUnique=5MB
```

实现见 `MarkReceived` 的 `startIdx/endIdx` 推导与 `slotStart/slotEnd` 钳制。

### 6.5 并发约束

| 锁/通道 | 何时占用 | 粒度 |
|---------|---------|------|
| `taskLocks` (sync.Map → `*sync.Mutex`) | 整个 `SaveChunk` 函数体 | per-taskID |
| `chunkTrackers` (sync.Map) | `getOrCreateTracker` 读写 + `cleanupTracker` | per-taskID |
| `cacheMu` (RWMutex) | `hashCache` 读写 | 进程级 |
| `watchMu` (Mutex) | watcher map 增删 | 进程级 |
| `Engine.mu` (RWMutex) | `sharedRoots` 增删 | 进程级 |
| `readLimiter` (chan cap 4) | `GetChunk` 整个读盘 | 进程级 4 路 |

`per-task 锁` 是关键: 同一 taskID 的 `SaveChunk` 串行执行，避免 bitmap 与 `file_log` 写入交错。但**不同 taskID 互不阻塞**，允许并发下载多个文件。

---

## 7. BLAKE3 哈希与两级缓存

### 7.1 为什么需要两级缓存?

直接全文件 BLAKE3 哈希是 O(文件大小) 的 IO + CPU 开销。常见场景"前端的文件树面板想展示每个文件的 hash"会触发成百上千次 `HashFile`。**仅靠 `(size, mtime)` 命中缓存是不安全的**:

- 编辑器保存时可能只更新内容而不更新 mtime (某些场景)。
- mtime 精度不足 (ext4 是纳秒级，FAT32 是 2 秒级)，跨平台时容易误判。
- 手工 `touch` 文件可以伪造 mtime。

**两级校验**:
1. **粗筛** (零 IO): `(size, modTime)` 完全匹配则进入下一级。
2. **细筛** (3×32KB IO): BLAKE3(头 32KB + 中 32KB + 尾 32KB)，不匹配则全量重算。

### 7.2 `fileFingerprint` 实现

```go
// hash.go:88
func (e *Engine) fileFingerprint(file *os.File, size int64) (string, error) {
    const sampleSize = 32 * 1024  // 32KB
    offsets := []int64{0}
    if size > sampleSize {
        offsets = append(offsets, size - sampleSize)   // 尾部
    }
    if size > 2 * sampleSize {
        mid := size/2 - sampleSize/2                  // 中部居中
        offsets = append(offsets, mid)
    }
    // 排序去重 (小文件可能 head==tail)
    // 每个 sample: 16 字节 meta (offset + n, LE) + bytes → BLAKE3 hasher
    return hex.EncodeToString(hash.Sum(nil))
}
```

**采样策略**:

- 小于 32KB: 只取头部一次。
- 32KB~64KB: 取头部 + 尾部。
- 大于 64KB: 取头部 + 中部 + 尾部。

每个 sample 之前先写 16 字节元数据 (offset + 实际读出长度，little-endian)，防止"两个不同位置但内容相同的 sample"被混淆。

### 7.3 `HashFile` 流程

```
Open file + Stat
    │
    ▼
absPath = filepath.Abs + Clean
    │
    ▼
runtime.cacheMu.RLock → 查 hashCache[absPath]
    │
    ├─ 命中 + (size, modTime) 匹配
    │     │
    │     ▼
    │   fileFingerprint(file, size)
    │     │
    │     ├─ 匹配 cached.fingerprint
    │     │     │
    │     │     ▼
    │     │   persistHashToFileLog(absPath, cache.hash, size)
    │     │     (跨进程复用；即使本进程清缓存，下次启动也能从 DB 拿到)
    │     │     │
    │     │     ▼
    │     │   return cache.hash                              ★ 命中快路径
    │     │
    │     └─ 不匹配 → 落入"全量重算"路径
    │
    └─ 未命中 → 落入"全量重算"路径

全量重算路径:
    │
    ▼
fileFingerprint (为了缓存住，避免下次又重算)
file.Seek(0)
io.Copy(blake3.New(), file)
    │
    ▼
hex hash
    │
    ▼
cacheMu.Lock → 写入 hashCache
    │
    ▼
persistHashToFileLog(absPath, hash, size)
    │
    ▼
return hash
```

### 7.4 哈希的进程间共享

`file_logs` 表同时承担两个职责 (用 `TaskID` 前缀区分):

| TaskID 形式 | 用途 |
|------------|------|
| `hash:<hex>` | 哈希备忘录；`FilePath` 是被哈希的文件绝对路径，`Transferred==TotalSize`，`Status==Completed` |
| `<任意 UUID>` | 下载任务进度；`FilePath` 是目标路径，`Transferred` 动态增长 |

启动时 `HashFile` 第一次调用总是会重新计算 (内存 cache 是空的)，但结果会**写回 DB**。下次进程启动后 `HashFile` 仍会从 stat + 内存 cache 触发，但因为没有内存 cache 会走"全量重算"路径，这意味着**跨进程的缓存命中率仅靠磁盘 IO 的 `fingerprint` 校验**。

> 当前实现的 trade-off: 跨进程时 `hashCache` 总是 miss，必须做 3×32KB IO 才能命中。后续可优化为启动时 `SELECT * FROM file_logs WHERE task_id LIKE 'hash:%'` 预热到 `hashCache`，但当前未实现。

---

## 8. EventBus 集成

File 层是**纯发布者**，不订阅任何 topic。所有方法对 `bus == nil` 状态都做 nil-check 后静默 no-op，便于单测。

| Topic | Payload | 触发时机 |
|-------|---------|---------|
| `file:progress` (`TopicFileProgress`) | `FileProgressPayload{TaskID, FilePath, Transferred, TotalSize, IsUpload:false}` | 每次 `SaveChunk` 写盘成功后 |
| `file:completed` (`TopicFileCompleted`) | `string` (TaskID) | 原子重命名成功后 |
| `fs:dir_changed` (`TopicDirChanged`) | `string` (root path) | `fsnotify` 命中 Create/Write/Remove/Rename/Chmod 后 |

**`IsUpload: false` 硬编码**: 当前 file 层只实现了下载 (receiver 侧)，上传 (sender 侧) 由网络层直接使用 `GetChunk` 而不走 `SaveChunk`，所以 `FileProgressPayload.IsUpload` 永远是 `false`。如果未来 file 层封装上传流程，需在此处根据调用上下文传入 `true`。

---

## 9. 数据库集成

仅使用 `db.Database` 接口中的 6 个方法，**全部无事务** (`*sql.Tx` 不在调用路径上):

| 方法 | 调用方 | 用途 |
|------|--------|------|
| `InsertSharedDirectory` | `ShareDirectory` | 新增共享根 |
| `DeleteSharedDirectory` | `UnshareDirectory` | 删除共享根 |
| `ListSharedDirectories` | `LoadSharedDirectories` | 启动时恢复 |
| `GetFileLog` | `PrepareDownload` / `GetMissingChunks` | 查询下载进度或哈希备忘录 |
| `UpsertFileLog` | `PrepareDownload` 初始化 / `SaveChunk` 进度 / `HashFile` 哈希持久化 | 写入或更新 |

**`file_logs` 表的双重职责**: 见 §7.4。

**SQLite 模式的兼容性**: 所有方法都是单语句 `INSERT OR REPLACE` / `SELECT`，无 BEGIN/COMMIT，依赖 SQLite WAL 模式 + `busy_timeout=5000ms` 即可。并发 `SaveChunk` 因有 `taskLocks` 串行化，同一 taskID 不会冲突；不同 taskID 操作的是不同 `task_id` 主键行，SQLite 行级锁足够。

---

## 10. fsnotify 驱动的缓存失效

`runRootWatcher` 是**每个共享根一个 goroutine**，事件循环:

```
for {
    select {
    case event := <-watcher.Events:
        if event.Op & (Create|Write|Remove|Rename|Chmod) == 0 { continue }
        invalidateTreeCache(root)                // 整棵树重扫
        invalidateHashCachePath(event.Name)      // 单文件 hash 缓存 + 后代 (前缀匹配)
        publishDirChanged(root)

        if event.Op & Create != 0 {
            info, _ := os.Stat(event.Name)
            if info != nil && info.IsDir() {
                walkAndWatch(event.Name, watcher)  // 递归加 watch
            }
        }

    case <-watcher.Errors:
        // 静默吞掉，依赖 watcher 关闭信号

    case <-rw.done:
        watcher.Close()
        return
    }
}
```

**`walkAndWatch` 的递归 watch**:

- 启动 `ShareDirectory` 时调用一次，把整棵子目录加入 watch 集合。
- 运行时**新建子目录**触发 Create 事件 → 单独 `walkAndWatch` 扩展 watch。
- 删除子目录时 fsnotify 自动从 watch 集合移除 (无需手动 un-watch)。

**`invalidateHashCachePath` 的后代失效**:

- 直接 `delete(hashCache, absPath)` 失效事件对应的文件。
- 同时遍历整个 `hashCache`，对所有以 `absPath + os.PathSeparator` 开头的 key 失效，处理"目录移动 / 重命名导致整棵子树需要重算"的场景。
- `IsPathWithinRoot` (`vfs.go:419`) 提供了**带分隔符检查**的 prefix 判断，避免 `"/tmp/shared"` 与 `"/tmp/shared_etc"` 的碰撞。

**`stopRootWatcher` 的优雅关闭**:

```go
rw.once.Do(func() { close(rw.done) })
```

`once` 防止多次 close panic。`done` 通道用于中断 `runRootWatcher` 主循环。`watcher.Close()` 在 `defer`-like 模式下做，但 `fsnotify` 关闭后 `<-Events` 与 `<-Errors` 都会关闭 → 主循环自然退出。

---

## 11. 并发与锁模型总览

| 资源 | 锁类型 | 持有者 | 持有时长 |
|------|--------|--------|---------|
| `Engine.sharedRoots` | `Engine.mu` (RWMutex) | `With*`, `ShareDirectory`, `UnshareDirectory`, `LoadSharedDirectories`, `GetSharedRoots`, `GetLocalTree` | 短 (单 map 操作) |
| `Engine.runtime` 指针 | `Engine.mu` (RWMutex) | `getRuntime` (RLock), `With*` (Lock) | 极短 |
| `treeCache` | `runtime.cacheMu` (RWMutex) | `getOrBuildTree`, `invalidateTreeCache` | RLock 命中快，Lock 仅写时 |
| `hashCache` | `runtime.cacheMu` (RWMutex) | `HashFile`, `invalidateHashCachePath` | 同上 |
| `watchers` map | `runtime.watchMu` (Mutex) | `ensureRootWatcher`, `stopRootWatcher`, `Close` | 短 |
| 磁盘读并发 | `runtime.readLimiter` (chan cap 4) | `GetChunk` | 整个 `ReadAt` 期间 |
| 2MB 缓冲 | `runtime.chunkPool` (sync.Pool) | `GetChunk` | 同上 |
| per-task 序列化 | `runtime.taskLocks` (sync.Map) | `SaveChunk` | 整个 `SaveChunk` |
| per-task tracker 缓存 | `runtime.chunkTrackers` (sync.Map) | `getOrCreateTracker`, `cleanupTracker`, `SaveChunk` | 短 |

**锁顺序**: 全局只有一处潜在锁嵌套: `ShareDirectory` 内 `Engine.mu` → `ensureRootWatcher` 内 `runtime.watchMu`。两者不会反向调用，**无死锁风险**。

**`sync.Map` 的使用**: `taskLocks` / `chunkTrackers` 用 `LoadOrStore` 懒加载，key 集合动态增长。`cleanupTracker` 在任务完成时 `Delete` 两个 key，防止内存泄漏。**潜在风险**: 如果进程异常退出前未 `cleanupTracker`，下次启动会看到陈旧的 taskLocks/trackers 残留，但 `taskLocks` 是 `*sync.Mutex` 实例本身，无外部状态；`chunkTrackers` 的 `*ChunkTracker` 实例也是无外部状态的对象，泄漏仅限于这两个 sync.Map 的 key 集合，**不会泄漏文件句柄或 DB 连接**。

---

## 12. `app.FileEngine` 接口合规

`internal/app/interfaces.go` 中定义的 5 个方法在 `file.Engine` 上全部存在:

| 接口方法 | Engine 方法 | 位置 |
|---------|------------|------|
| `GetVirtualTree(rootPath string) (interface{}, error)` | `GetVirtualTree` | vfs.go:186 |
| `ShareDirectory(absPath string) error` | `ShareDirectory` | vfs.go:47 |
| `UnshareDirectory(absPath string) error` | `UnshareDirectory` | vfs.go:81 |
| `GetDirectoryChildren(absPath string) (interface{}, error)` | `GetDirectoryChildren` | vfs.go:199 |
| `GetSharedRoots() []string` | `GetSharedRoots` | vfs.go:154 |

**接口未暴露但 App 层直接使用的方法** (作为具体类型 `*file.Engine` 调用):

- `HashFile`
- `PrepareDownload`
- `SaveChunk`
- `GetMissingChunks`
- `LoadSharedDirectories`
- `Close`
- `CleanupStaleTempFiles`
- `GetLocalTree`

这意味着 App 层**不是**完全通过接口与 File 层交互的。`main.go` 把 `*file.Engine` 实例直接传给 `NewApp`，App 内部对这些额外方法用具体类型调用。这一设计取舍是: 接口边界只覆盖"前端可调用的方法集" + "测试可 mock 的方法集"，而下载协议相关方法**只在生产代码路径上有真实实现**，单测通过 `app_test.go` 的 `MockFile` 即可覆盖接口部分，对非接口方法的测试在 `file_test.go` 内部用真实 `*file.Engine` 配合 `t.TempDir()` 完成。

---

## 13. 测试覆盖

`file_test.go` 共 19 个 `Test*` 函数，全部使用 `t.TempDir()` 隔离:

| 测试 | 覆盖点 |
|------|--------|
| `TestGetLocalTree` | 多根共享目录合并 + 排序 |
| `TestGetDirectoryChildren` | 单层 lazy 扫描 |
| `TestTreeCacheInvalidatedByFilesystemEvent` | fsnotify 触发 `invalidateTreeCache` |
| `TestUnshareDirectoryStopsWatcher` | Unshare 清理 watcher |
| `TestCloseStopsAllWatchers` | Close 全量清理 |
| `TestGetChunk` | 2MB 分块读取 + EOF |
| `TestHashFile_CacheAndRefresh` | 两级缓存命中 + 失效重算 |
| `TestHashFile_PersistToFileLog` | `file_logs` 哈希备忘录写入 |
| `TestHashFile_RefreshWhenModTimeUnchanged` | mtime 未变但内容变时强制重算 |
| `TestPrepareDownloadAndSaveChunk` | 端到端下载流程 |
| `TestChunkTrackerSingleSlot` | 单 slot tracker |
| `TestChunkTrackerMultiSlot` | 多 slot tracker |
| `TestChunkTrackerOutOfOrder` | 乱序 MarkReceived 单调性 |
| `TestChunkTrackerDuplicateChunk` | 重复 chunk 幂等性 |
| `TestChunkTrackerMissingOffsets` | 缺失偏移计算 |
| `TestChunkTrackerSaveLoad` | 二进制 bitmap 持久化往返 |
| `TestChunkTrackerBoundary` | 跨 slot 边界 |
| `TestChunkTrackerSmallFile` | 极小文件 (1 字节) |
| `TestSaveChunkOutOfOrder` | 真实 SaveChunk 乱序端到端 |
| `TestSaveChunkIncompleteShouldNotFinalize` | 未完成不触发重命名 |
| `TestSaveChunkDupChunkIdempotent` | 重复 SaveChunk 幂等 |

测试**不依赖**真实 DB，大部分测试不需要 `WithDatabase()`；需要 DB 的 `PrepareDownload` / `SaveChunk` 测试使用 `t.TempDir()` + 一个最小化的内存 SQLite 实现 (位于 `core/db` 的 test helper) 或 mock。**这意味着 `file` 包的测试是**纯单元测试**，不需要启动 EventBus / 启动网络**。

---

## 14. 设计权衡与未来扩展

### 14.1 当前的取舍

- **固定 2MB chunk**: 简单、可预测、对网络 MTU 友好;但对小文件 (例如几 KB) 会浪费内存。
- **bitmap 64-bit word**: 单文件最大支持 64 × 64 = 4096 个 slot = 8GB，足以覆盖常见场景;超大文件需要扩展为多 word bitmap (当前已经支持 `bitmapSize = (totalSlots+63)/64`，无上限)。
- **`os.IsExist` retry on rename**: 仅重试一次，覆盖"目标文件已存在"和"权限拒绝"两类常见竞争;不覆盖更复杂的并发 rename。
- **fsnotify 后代缓存失效用前缀扫描**: O(N) 在 hashCache size 上,但 hashCache 容量小 (一个进程内通常 < 1000 项);未来如出现热点场景可改为 `trie`。

### 14.2 已知缺口

- **没有上传协议**: Sender 侧的 `StartDownload` 注释为 `// StartDownload方法不再使用`，当前由网络层直接调用 `GetChunk`。如果未来 App 层想统一"上传/下载进度"，需要补一个 `UploadChunk` 入口。
- **没有速率限制**: `GetChunk` 限流只到磁盘 IO 层，没有面向对端的带宽控制;网络层有责任做流量整形。
- **没有任务取消**: `PrepareDownload` 返回的 offset 没有 cancel 语义;上层 (App) 通过不再调用 `SaveChunk` + `CleanupStaleTempFiles` 模拟取消。
- **`hashCache` 不预热**: 跨进程哈希备忘录虽然写到 DB，但启动时不会预读到内存，第一次访问总是要 stat + IO 校验。

### 14.3 扩展方向

- **多文件批量下载**: 当前 `SaveChunk` 单文件设计;网络层可包装 `BatchSaveChunk` 一次提交多个 offset。
- **压缩传输**: 当前 `GetChunk` 返回原始字节;未来可在数据面加 zstd 压缩。
- **加密传输**: 队伍/私聊加密在 network 层完成,file 层只关心明文落盘。
- **共享目录的权限模型**: 当前 `ShareDirectory` 无粒度控制,未来可加只读/读写/白名单。

---

## 15. 总结

File 层是 Parade 中**逻辑最复杂但依赖最薄**的一层:

- **3 个核心数据流**: VFS 扫描 (读) / Chunk 读取 (读) / SaveChunk (写)。
- **2 个核心数据结构**: `FileNode` (树) / `ChunkTracker` (双层追踪)。
- **2 个核心协议契约**: `ShareDirectory` ↔ `UnshareDirectory` (DB 持久化) / `PrepareDownload` ↔ `SaveChunk` (断点续传)。
- **5 个 EventBus topic** (实际只发 3 个): 进度 / 完成 / 目录变化。
- **3 类缓存**: treeCache (per-root) / hashCache (per-file) / chunkTrackers (per-task)。

它通过 `app.FileEngine` 接口与 App 层解耦,通过 EventBus 与 App 层异步通信,通过 `db.Database` 接口与 Core 层解耦,**可被独立测试、独立替换**。这种设计在 v0.2.0 阶段已足够支撑 libp2p 网络层带来的多 peer 并发下载场景。
