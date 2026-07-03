# 存储模块 (Storage Module) 接口说明

本模块基于 **SQLite (WAL 模式)** 提供高性能、线程安全的本地数据持久化服务。

## 1. 核心模型

### `Message` (消息/信令)
用于存储聊天记录、系统通知和同步元数据。
*   **HLC**: 混合逻辑时钟，用于分布式环境下消息的绝对排序。
*   **Content**: `[]byte` 类型，存入前需经由逻辑层完成**落盘加密**。

### `FileLog` (文件传输任务)
用于维护文件传输状态，支持断点续传。
*   **TaskID**: 文件哈希或路径唯一标识。
*   **Transferred**: 已完成的字节偏移量（Offset）。

## 2. 接口方法与调用场景

### A. 消息处理

| 方法 | 调用者 | 调用场景 |
| :--- | :--- | :--- |
| **`InsertMessage`** | 逻辑层 | 收到新聊天消息或系统通知时，直接落盘。 |
| **`GetRecentMessages`** | 前端/逻辑层 | 软件启动或打开聊天窗口时，拉取历史记录（分页）。 |
| **`GetMessagesSinceHLC`** | 网络层 | **增量同步核心**。当对端告诉我们他的最后一条消息时间戳时，我们查出并发送其缺失的数据。 |

### B. 文件传输

| 方法 | 调用者 | 调用场景 |
| :--- | :--- | :--- |
| **`UpsertFileLog`** | 文件层 | 传输进行时，每完成一个 Chunk (2MB) 更新一次 `Transferred` 进度。 |
| **`GetFileLog`** | 文件层 | 启动下载任务前，检查是否存在已下载的部分。若存在，则从该 Offset 开始请求。 |

### C. 高性能批量操作

| 方法 | 调用者 | 调用场景 |
| :--- | :--- | :--- |
| **`RunInTx`** | 网络层 | **批量对账**。从网络接收到大量缺失消息时，必须包裹在事务中一次性写入，否则会导致 I/O 阻塞。 |

## 3. 最佳实践 (Quick Start)

### 初始化
```go
// 建议在 main.go 或 App 结构体初始化时调用一次
dbInst, _ := db.NewSQLiteDB("parade.db")
```

### 批量插入示例 (网络层对账)
```go
err := dbInst.RunInTx(ctx, func(tx db.DBTx) error {
    for _, msg := range incomingMsgs {
        if err := tx.InsertMessageTx(ctx, msg); err != nil {
            return err // 任意一条失败，整批回滚
        }
    }
    return nil
})
```

### 断点续传查询 (文件层)
```go
log, _ := dbInst.GetFileLog(ctx, fileHash)
if log != nil && log.Transferred < log.TotalSize {
    // 触发断点续传逻辑，从 log.Transferred 开始请求数据
}
```

## 4. 注意事项
1.  **并发安全**：本模块已开启 `WAL` 模式及 `busy_timeout`，支持多个协程并发读写，无需在外部加锁。
2.  **落盘加密**：数据库**不负责**加密逻辑。请逻辑层务必在调用 `Insert` 前加密 `Content` 字段。
3.  **HLC 约束**：禁止修改 `Message.HLC` 的生成逻辑，它是确保分布式数据一致性的唯一基准。
