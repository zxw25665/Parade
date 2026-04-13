# App 桥接层接口说明

本模块（`internal/app`）是“游行”软件的业务核心。它持有 **Crypto**、**Database**、**EventBus**、**Network** 和 **File** 五大引擎的句柄，并负责协调它们完成复杂的业务流水线。

## 1. 核心职责

1. **接口暴露**：将后端功能映射为 Wails 友好的方法供前端（Vue3）调用。
2. **业务编排**：例如“发送消息”流程，需同时调度存储、加密、时钟和网络模块。
3. **UI 推送**：通过 `Frontend` 接口，将底层异步事件（如收到消息、节点上线）实时推送到前端。
4. **时序维护**：利用混合逻辑时钟（HLC）确保分布式网络中的消息顺序。

## 2. 前端 API 契约 (Wails Call)

前端工程师可以通过 `window.go.app.App.MethodName()` 调用以下方法：

### 认证与身份 (Auth)
* **`CheckHasIdentity() bool`**  
  检查本地是否存在 `.parade_identity` 凭证。
* **`Register(password string) error`**  
  创建新身份。**注意**：密码不可找回。
* **`Login(password string) error`**  
  解锁本地身份并解密私钥进内存。

### 队伍与连接 (Network)
* **`JoinTeam(secret string) error`**  
  设置队伍口令并启动 4327 端口的 gRPC 监听及 mDNS 广播。

### 消息协作 (Chat)
* **`SendTeamChat(text string) error`**  
  发送群聊消息。流程：生成 HLC -> 本地加密存储 -> 队伍加密 -> 网络广播。
* **`GetRecentHistory(limit, offset int) ([]map[string]interface{}, error)`**  
  拉取历史记录。返回的数据已解密为明文，前端直接渲染即可。

## 3. UI 异步推送 (Events On)

前端需监听以下事件以实现响应式刷新：

| 事件名称 | 载荷数据 | 触发时机 |
| :--- | :--- | :--- |
| **`ui_new_message`** | `MessageView` 对象 | 收到他人消息或本节点消息同步成功。 |
| **`ui_peer_joined`** | `PeerInfo` (PubKey, IP) | 发现新的局域网成员。 |
| **`ui_peer_left`** | `PeerInfo` | 成员心跳丢失。 |

## 4. 关键架构设计

### 混合逻辑时钟 (HLC)
系统不依赖节点物理时钟，而是生成 `ISO8601_Counter_NodeID` 格式的字符串。
* **排序规则**：直接对 HLC 字符串进行字典序（Alphabetical Order）排列即可得到绝对正确的因果顺序。

### 隔离设计 (Frontend Interface)
为了通过单元测试，我们定义了 `Frontend` 接口。
* **测试环境**：使用 `MockUI`，仅记录事件不产生依赖。
* **生产环境**：使用 `WailsUI`，调用 Wails 运行时的 `EventsEmit`。

## 5. 快速初始化 (用于 main.go)

```go
// 1. 实例化所有底层组件
eb := eventbus.New()
cr := crypto.NewEngine()
dbInst, _ := db.NewSQLiteDB("parade.db")
ui := app.NewWailsUI() // 生产环境 UI

// 2. 实例化 App
application := app.NewApp(eb, cr, dbInst, networkImpl, fileImpl, ui)

// 3. Wails 启动时注入上下文
wails.Run(&options.App{
    OnStartup: func(ctx context.Context) {
        ui.SetContext(ctx)      // 允许 UI 推送事件
        application.Startup(ctx) // 允许 App 开始工作
    },
    Bind: []interface{}{ application }, // 绑定 API
})
```

## 6. 注意事项
* **异步处理**：`registerEventSubscribers` 监听 `EventBus` 是异步的，这保证了网络层在高频收发消息时，UI 刷新不会卡死后端逻辑。
* **数据安全**：所有通过 `GetRecentHistory` 或 `ui_new_message` 发往前端的数据均为**解密后的明文**，前端无需关心加密逻辑。
