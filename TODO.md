# 1. 网络层详细设计与开发要求

网络层是“游行”的生命线，负责在不稳定的局域网环境下建立稳定、加密的 P2P 通信信道。

### 1.1 模块职责
1.  **节点发现**：实现零配置的“雷达”功能，感知同队伍成员。
2.  **控制面 (信令)**：管理 4327 端口的长连接，负责聊天、心跳、对账元数据。
3.  **数据面 (流传输)**：管理 4328 端口，负责高吞吐的文件 Chunk 传输。
4.  **安全封装**：所有流出网络的数据必须经过 `crypto` 引擎的“信封”包装。

### 1.2 技术选型与规范
*   **协议栈**：gRPC over HTTP/2（利用多路复用避免 TCP 握手开销）。
*   **服务发现**：`hashicorp/mdns` (基础发现) + `hashicorp/memberlist` (存活状态管理)。
*   **序列化**：Protocol Buffers (proto3)。

### 1.3 核心开发任务

#### A. 发现引擎实现 (Discovery)
*   **mDNS 广播**：在局域网内广播服务名 `_parade._tcp`，Payload 中携带本机的 `PubKeyBase64`（由 App 层传入）。
*   **Gossip 维护**：集成 `memberlist`。当收到新节点的 mDNS 响应后，尝试建立 Gossip 连接。
*   **事件上报**：
    *   当节点加入并完成握手（验证队伍口令一致）后，向 `EventBus` 发布 `TopicPeerJoined`。
    *   当连续三次心跳失败，发布 `TopicPeerLeft`。

#### B. 控制面信令 (4327 端口 - gRPC Bidi Streaming)
*   **信封模式 (Enveloping)**：定义一个通用的 Proto 消息：
    ```proto
    message Envelope {
        string sender_id = 1;
        bytes payload = 2; // 这里的 payload 是被 crypto.EncryptTeam 加密后的数据
        string signature = 3;
    }
    ```
*   **双向流 API**：
    *   `StreamChat`：处理实时的群聊与私聊。
    *   `SyncMetadata`：用于重连后的 HLC 对账。接收对方的 HLC，从 DB 查询缺失记录并回传。

#### C. 数据面传输 (4328 端口 - gRPC Server Streaming)
*   **流式下发**：实现 `DownloadFile(FileRequest) returns (stream Chunk)`。
*   **限流与重试**：实现简单的滑动窗口或限流，防止文件传输挤占所有带宽导致心跳超时。

### 1.4 交付 API 接口 (Interface)
网络层需要为 `App` 层提供以下方法：
*   `Start(teamSecret string)`：初始化网络引擎，启动监听和发现。
*   `BroadcastMessage(msg []byte)`：将加密后的消息发送给所有在线 Peer。
*   `RequestFileChunk(peerID string, taskID string, offset int64)`：向特定节点请求文件块。

---

# 2. 文件系统层详细设计与开发要求

文件系统层负责将本地磁盘抽象为可供团队成员查阅的虚拟资产，并确保大文件传输时系统不卡死、不崩溃。

### 2.1 模块职责
1.  **虚拟文件树生成**：将本地共享目录映射为元数据结构。
2.  **高速分块读写**：实现 2MB 颗粒度的磁盘 I/O。
3.  **完整性校验**：利用 Blake3 算法保证文件“所见即所得”。
4.  **进度持久化协作**：与数据库配合，维护断点续传状态。

### 2.2 技术选型与规范
*   **哈希算法**：`zeebo/blake3`（追求极致的计算速度，减少 CPU 占用）。
*   **I/O 模型**：`io.CopyBuffer` + `sync.Pool` (复用内存缓冲区)。
*   **并发控制**：限制同时读取磁盘的协程数量，防止机械硬盘 I/O 阻塞。

### 2.3 核心开发任务

#### A. 虚拟目录管理 (Metadata)
*   **递归扫描**：实现一个 Scanner，扫描用户指定的共享文件夹。
*   **元数据缓存**：生成如下结构，用于回复网络层的查询请求：
    ```go
    type FileNode struct {
        Name     string
        IsFolder bool
        Size     int64
        Hash     string // 仅对文件有效
        Children []*FileNode
    }
    ```
*   **按需哈希**：不建议启动时全量计算哈希。仅在：1. 对方请求下载；2. 系统闲置时，逐步计算并存入 `db.FileLog`。

#### B. 读取流控制器 (Sender Side)
*   **Chunk 提取**：实现 `GetChunk(path string, offset int64) ([]byte, error)`。
*   **内存保护**：使用 `sync.Pool` 预分配 2MB 的 `[]byte` 缓冲区。禁止使用 `ioutil.ReadFile` 等一次性读取全量文件的操作。

#### C. 写入流与进度管理 (Receiver Side)
*   **临时文件处理**：下载中的文件以 `.parade_tmp` 结尾。
*   **原子写入**：
    1.  接收网络层传来的 Chunk。
    2.  根据 Offset 写入临时文件对应位置。
    3.  **关键步**：调用 `db.UpsertFileLog(taskID, newOffset)`。
    4.  检查是否完成，若完成则重命名并向 `EventBus` 发布 `TopicFileCompleted`。

### 2.4 交付 API 接口 (Interface)
文件层需要为 `App` 层提供以下方法：
*   `ShareDirectory(absPath string) error`：添加一个新的共享根目录。
*   `GetLocalTree() []FileNode`：获取本机的虚拟文件结构。
*   `PrepareDownload(taskID string, totalSize int64) (startOffset int64)`：检查 DB，判断是否需要断点续传，返回起始位置。
*   `SaveChunk(taskID string, data []byte, offset int64) error`：持久化接收到的文件块。

---

### 共同建议：

1.  **Mock 联调**：在真正的网络传输代码写好前，网络层可以先写一个“内存转发器”模拟网络收发；文件层可以用 `strings.NewReader` 模拟磁盘文件。
2.  **错误处理**：局域网环境经常断线，请务必处理好 `context.Canceled` 和超时逻辑，不要让 goroutine 泄露。
3.  **尊重 EventBus**：网络层和文件层是生产者，App 层是消费者。任何状态变更（发现人、下载了 1%）都请无脑抛给 `EventBus`，不要尝试直接修改 UI 或调用 App 层的复杂逻辑。
