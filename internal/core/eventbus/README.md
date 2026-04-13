# 事件总线模块

本模块是“游行”软件各层级之间解耦的核心中枢。它允许底层模块（如网络、文件系统）在无需感知上层逻辑的情况下发布状态变更。

## 1. 工作原理
1. **订阅 (Subscribe)**: 模块启动时，向总线注册感兴趣的 `Topic`。
2. **发布 (Publish)**: 当事件发生时，生产者将数据丢入总线。
3. **分发 (Dispatch)**: 总线为每个订阅者开启独立的 Goroutine 进行异步调用。

## 2. 系统主题字典

| Topic (常量) | 数据载荷 (Payload) | 触发场景 |
| :--- | :--- | :--- |
| `TopicPeerJoined` | `PeerEventPayload` | 发现新邻居节点。 |
| `TopicMsgReceived` | `MsgReceivedPayload` | 收到任何网络消息或指令。 |
| `TopicFileProgress`| `FileProgressPayload`| 文件块写入磁盘成功。 |
| `TopicFileCompleted`| `string` (TaskID) | 下载或上传任务彻底结束。 |

## 3. 调用规范

### 订阅事件 (通常在 App 层的启动逻辑中)
```go
subID := bus.Subscribe(eventbus.TopicPeerJoined, func(ctx context.Context, ev eventbus.Event) {
    payload := ev.Payload.(eventbus.PeerEventPayload)
    // 更新在线列表 UI...
})
```

### 发布事件 (在网络层接收协程中)
```go
// 收到数据后
bus.Publish(eventbus.TopicMsgReceived, eventbus.MsgReceivedPayload{
    SenderID: "xxx",
    Content:  decryptedBytes,
})
```

## 4. 扩展性说明
* **类型安全**: 在 `topics.go` 中定义载荷结构体。虽然 `Payload` 是 `interface{}`，但在订阅端通过 `payload.(YourStruct)` 进行断言即可恢复类型。
* **隔离性**: 总线内置了 `recover()`。即使前端 Wails 的推送逻辑写错了导致 Panic，网络层的接收和文件层的下载也不会中断。
* **生命周期**: 记得在组件销毁时（例如切换队伍）调用 `Unsubscribe` 以防止内存中残留无效的回调引用。

