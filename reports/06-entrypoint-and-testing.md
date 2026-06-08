# Parade (游行) — 入口与测试约定设计文档

> Version: `v0.2.0-libp2p` · Module: `parade` · Scope: 入口初始化、Wails 配置、身份文件、混合逻辑时钟、UUID 派生、测试约定
> Status: 入口与质量保障专题文档 · Last updated: 2026-06-08

---

## 1. 入口主程序 main.go

### 1.1 文件位置与体量

源文件: `main.go` (仓库根目录, 100 行, **无测试覆盖**)

`main.go` 是 Parade 桌面端的唯一入口,负责把所有引擎装配起来交给 Wails 运行时,本身不写业务逻辑,业务编排全部委托给 `internal/app.App`。

### 1.2 关键常量与全局变量

```go
//go:embed all:frontend/dist
var assets embed.FS

const AppVersion = "v0.2.0-libp2p"

var appInstance *app.App
```

要点:

- `//go:embed all:frontend/dist` 把 Vite 的 `frontend/dist` 静态资源直接打包进 Go 二进制。`all:` 前缀让隐藏文件 (例如 `.nojekyll`) 一并嵌入,生产环境不再依赖外部静态目录。
- `appInstance` 是包级全局,目的是在 `onSecondInstanceLaunch` 回调里能拿到运行时上下文 (Wails 启动后才会有 `context.Context`)。

### 1.3 第二实例拦截回调

```go
func onSecondInstanceLaunch(_ options.SecondInstanceData) {
    ctx := appInstance.GetContext()
    if ctx != nil {
        runtime.WindowUnminimise(ctx)
        runtime.Show(ctx)
    }
    log.Println("[Parade] Second instance blocked; existing window activated")
}
```

行为: 用户双开 Parade 时,新进程不进入 Wails 主循环,而是唤起已有窗口 (还原 + 置顶)。`GetContext()` 返回 `nil` 说明主进程还没走到 `OnStartup`,此时静默退出,避免 NPE。

### 1.4 main() 初始化顺序 (严格,不可乱)

整个 `main()` 是一个**单向的依赖图构造过程**,顺序由后置依赖决定:

```
1. eventBus := eventbus.New()                          // 无依赖
2. cry     := crypto.NewEngine()                       // 无依赖
3. database, err := db.NewSQLiteDB("./.parade_data.db") // 失败 -> log.Fatalf
4. logBroker, err := logger.NewLogBroker("./.parade.log", 5000) // 失败 -> log.Fatalf
5. fileEngine := file.NewEngine()
       .WithDatabase(database)
       .WithEventBus(eventBus)
       .WithLogger(logBroker)
   fileEngine.LoadSharedDirectories()                  // 阻塞: 恢复已持久化的共享根
6. netEngine := network.NewLibp2pEngine(eventBus, cry, logBroker)
   netEngine.AttachFileEngine(fileEngine)              // 构造后注入, 避免循环依赖
7. wailsUI := app.NewWailsUI()                         // 上下文稍后在 OnStartup 中注入
8. appInstance = app.NewApp(eventBus, cry, database, netEngine, fileEngine, wailsUI, logBroker)
9. err := wails.Run(&options.App{
       Title:  "Parade " + AppVersion,
       Width:  1024,
       Height: 768,
       AssetServer: &assetserver.Options{ Assets: assets },
       OnStartup:   func(ctx) { wailsUI.SetContext(ctx); appInstance.Startup(ctx) },
       OnShutdown:  func(ctx) { appInstance.Shutdown() },
       SingleInstanceLock: &options.SingleInstanceLock{
           UniqueId:               "com.parade.app-7f3a9c2e",
           OnSecondInstanceLaunch: onSecondInstanceLaunch,
       },
       Bind: []interface{}{ appInstance },             // 只绑定 App, 不暴露子引擎
   })
```

### 1.5 关闭顺序 (defer, LIFO)

```go
defer database.Close()
defer logBroker.Close()
defer fileEngine.Close()
defer netEngine.Stop()
```

LIFO 实际执行顺序为: `netEngine.Stop()` -> `fileEngine.Close()` -> `logBroker.Close()` -> `database.Close()`,对应“先停对外、再停文件、再停日志、最后关库”的兜底策略。

### 1.6 顺序背后的硬约束

| 步骤 | 强约束 | 违反后果 |
|:---|:---|:---|
| 1. EventBus 必须先建 | file 与 netEngine 都会向其发布 TopicPeerJoined / TopicMsgReceived 等 | nil 解引用,订阅侧 panic |
| 2. Crypto 无依赖但需早建 | netEngine 构造时把 cry 注入,后续加密/解密均依赖其 in-memory 私钥 | 网络层无法解密团队广播 |
| 3. Database 在 file 之前 | `LoadSharedDirectories` 从 DB 读 `shared_roots` 表 | 共享根丢失,文件树从零开始 |
| 4. LogBroker 在 file/net 之前 | 两者都注入 LogBroker,否则子模块的 `logr.Info()` 走默认 logger | 日志分散,无法统一 JSONL |
| 5. AttachFileEngine 后置 | netEngine 需要 fileEngine 处理 `StartDownload` / `BrowseRemoteDirectory` 反向调用 | 构造期循环依赖 |
| 6. WailsUI 在 OnStartup 中才 SetContext | 启动时 ctx 还不存在,过早调用 `runtime.EventsEmit(ctx, ...)` 会 NPE | 前端黑屏 / 日志告警 |
| 7. Bind 只放 App | 子引擎 (db/crypto/...) 不暴露给 JS,降低前端可攻击面,统一由 App 编排 | 接口泄漏,版本演进困难 |

### 1.7 Wails Options 关键字段

| 字段 | 值 | 备注 |
|:---|:---|:---|
| `Title` | `"Parade " + AppVersion` | 窗口标题,版本号实时拼接 |
| `Width` / `Height` | `1024` / `768` | 默认窗口尺寸 (前端可拖拽) |
| `AssetServer.Assets` | `assets` (embed.FS) | 走 `//go:embed` 嵌入的 dist |
| `OnStartup` | `wailsUI.SetContext + appInstance.Startup` | **唯一注入点** |
| `OnShutdown` | `appInstance.Shutdown` | 关闭事件订阅 + 落盘 |
| `SingleInstanceLock.UniqueId` | `"com.parade.app-7f3a9c2e"` | 操作系统级互斥,7f3a9c2e 为项目后缀防重名 |
| `Bind` | `[]interface{}{ appInstance }` | JS 端只看到 `window.go.app.App.*` |

---

## 2. Wails 配置文件 (wails.json)

### 2.1 文件位置

源文件: `wails.json` (仓库根目录, 13 行, **带 `$schema`**)

### 2.2 完整内容

```json
{
  "$schema": "https://wails.io/schemas/config.v2.json",
  "name": "parade",
  "outputfilename": "parade",
  "assetdir": "./frontend/dist",
  "wailsjsdir": "./frontend/src/lib",
  "frontend:install": "npm install",
  "frontend:build": "npm run build",
  "author": { "name": "", "email": "" }
}
```

### 2.3 字段语义

| 字段 | 值 | 作用 |
|:---|:---|:---|
| `$schema` | Wails v2 schema URL | 编辑器智能提示,运行时忽略 |
| `name` | `parade` | 项目名,`wails build` 也用作二进制名前缀 (Linux 区分大小写) |
| `outputfilename` | `parade` | 最终二进制名 (Linux 下输出 `parade`,Windows 下输出 `parade.exe`) |
| `assetdir` | `./frontend/dist` | Vite 构建产物的源目录,需与 `main.go` 中 `//go:embed all:frontend/dist` 完全一致 |
| `wailsjsdir` | `./frontend/src/lib` | `wails build/dev` 自动生成 JS 绑定 (TypeScript) 的目标目录,前端 `import { ... } from '$lib'` 即可 |
| `frontend:install` | `npm install` | `wails build` 前自动安装前端依赖 |
| `frontend:build` | `npm run build` | 构建前端,产物必须落到 `assetdir` |
| `author` | 空 | 占位,未填写,不影响构建 |

### 2.4 注意事项

- **没有 `frontend:dev` 字段**: 开发模式直接走 `wails dev` 默认行为 (Vite dev server on `:34115` + Wails 二进制),无需在此声明。
- **wailsjsdir 与 gitignore**: `.gitignore` 显式忽略 `frontend/src/lib/wailsjs/`,因为这是每次 `wails build/dev` 都重新生成的代码。

---

## 3. 身份文件格式 (`.parade_identity`)

### 3.1 路径与权限

- 路径常量: `const IdentityFile = "./.parade_identity"` (定义于 `internal/app/app.go:24`)
- 权限: `0600` (仅当前用户可读写,Linux 下 `os.WriteFile(filepath, data, 0600)` 显式设置)
- 重要性: 包含加密后的私钥,一旦泄露需立刻 `Register` 新建;一旦遗忘密码,所有本地数据永久不可解密 (Argon2id 单向不可逆)

### 3.2 JSON 格式

```json
{
  "salt": "Yjk5OGYyZWMxNWI4...",
  "encrypted_priv": "ZWNi...",
  "pub_key": "AbCdEf..."
}
```

字段含义:

| 字段 | 类型 | 长度 | 说明 |
|:---|:---|:---|:---|
| `salt` | base64 | 16 字节原文 | Argon2id 盐,每个用户独立,防彩虹表 |
| `encrypted_priv` | base64 | 32 字节密文 + GCM tag | AES-256-GCM 加密后的 Curve25519 私钥 |
| `pub_key` | base64 | 32 字节明文 | Curve25519 公钥,前端 `window.go.app.App.GetPublicKeyBase64()` 也返回此值 |

### 3.3 RegisterIdentity (keystore.go:47-83)

注册流程,六步:

```go
func (c *paradeCrypto) RegisterIdentity(password, filepath string) error {
    // 1. 16 字节随机盐
    salt := make([]byte, 16)
    rand.Read(salt)

    // 2. Curve25519 密钥对
    var privKey, pubKey [32]byte
    rand.Read(privKey[:])
    curve25519.ScalarBaseMult(&pubKey, &privKey)

    // 3. Argon2id 派生主密钥 (1 轮, 64MB 内存, 4 线程, 32 字节)
    masterKey := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)

    // 4. AES-256-GCM 加密私钥
    encryptedPriv, _ := aesGCMEncrypt(masterKey, privKey[:])

    // 5. 落盘 (0600 权限)
    os.WriteFile(filepath, data, 0600)

    // 6. 注册完自动加载到内存
    return c.LoadIdentity(password, filepath)
}
```

### 3.4 LoadIdentity (keystore.go:86-121)

登录流程,五步:

```go
func (c *paradeCrypto) LoadIdentity(password, filepath string) error {
    // 1. 读文件 + JSON 反序列化
    data, _ := os.ReadFile(filepath)
    var idFile IdentityFile
    json.Unmarshal(data, &idFile)

    // 2. 重新派生主密钥
    masterKey := deriveMasterKey(password, idFile.Salt)

    // 3. AES-GCM 解密私钥 (失败 -> "invalid password")
    privKey, err := aesGCMDecrypt(masterKey, idFile.EncryptedPriv)
    if err != nil { return errors.New("invalid password") }

    // 4. 加载到内存
    c.masterKey = masterKey
    c.privKey   = privKey
    c.pubKey    = idFile.PubKey

    // 5. 派生 personalUUID (UUID v5 + sha256, 跨设备确定性)
    c.personalUUID = uuid.NewHash(sha256.New(),
        uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8"),
        idFile.PubKey, 5).String()

    // 6. 加载多队伍密钥环 (.parade_teams), 错误以 warning 形式收集
    if err := c.loadTeamKeys(); err != nil { c.loadWarnings = append(...) }
    return nil
}
```

### 3.5 设计要点

- **Argon2id 参数**: 1 轮迭代 + 64MB 内存 + 4 线程,2026 年主流硬件上单次派生约 200ms,既抗 GPU 又不影响用户体验。
- **personalUUID 跨设备确定性**: 同一密钥对在不同设备上算出同一个 UUID,这样局域网里只靠公钥就能相互识别,无需中心注册。
- **loadWarnings 非致命**: 多团队密钥 (`.parade_teams`) 加载失败只告警,不阻断登录;用户仍可重新 `JoinTeam`。

---

## 4. 混合逻辑时钟 (HLC)

### 4.1 文件与体量

源文件: `internal/app/hlc.go` (24 行)

### 4.2 设计动机

Parade 没有中心服务器,所有节点时钟可能偏差数秒甚至跨时区,纯物理时间无法保证因果顺序。HLC 字符串同时编码**物理时间 + 进程内单调计数器 + 节点标识**,满足:

1. 单调递增: 同进程内永远不重复
2. 跨进程可比: 节点 ID 后缀打破平局
3. 字典序即因果序: `strings.Compare(hlc1, hlc2)` 即可

### 4.3 实现

```go
var hlcCounter uint32  // 进程全局 atomic 计数器

func GenerateHLC(nodeUUID string) string {
    ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
    cnt := atomic.AddUint32(&hlcCounter, 1) % 10000  // 9999 后回绕

    nodeID := "unknown"
    if len(nodeUUID) >= 8 { nodeID = nodeUUID[:8] }

    return fmt.Sprintf("%s_%04d_%s", ts, cnt, nodeID)
}
```

### 4.4 字段分解

| 段 | 格式 | 例子 | 说明 |
|:---|:---|:---|:---|
| 物理时间 | `2006-01-02T15:04:05.000Z` | `2026-04-13T12:00:00.000Z` | 毫秒精度 UTC,ISO8601 子集 |
| 计数器 | `%04d` | `0001` | 0..9999 循环,同毫秒内保证单调 |
| 节点 ID | 前 8 字节 UUID | `REMOTE` | 打破跨节点平局,长度不足 fallback `"unknown"` |

完整例子: `2026-04-13T12:00:00.000Z_0001_REMOTE`

### 4.5 关键属性

- **字典序可排序**: `hlc1 < hlc2` 等价于 `hlc1` 因果在 `hlc2` 之前 (本进程内); 跨进程上,先比时间戳,再比计数器,最后比节点 ID。
- **回绕容忍**: 计数器 10000 进制,即使单毫秒爆量也不会破坏排序,极端情况只是后到的消息 ID 更小,但物理时间保证新消息时间戳不会更早。
- **占位安全**: `len(nodeUUID) < 8` 时退化为 `"unknown"`,不 panic。

### 4.6 使用约定

- **生成位置**: 每条 `SendTeamChat` / `SendPrivateChat` 首先生成 `hlc := GenerateHLC(myUUID)`,然后把它绑到 `MsgReceivedPayload.HLC` 一起走 `EncryptTeam` 落盘 + 广播。
- **存储**: DB 里 `messages.hlc` 是 TEXT,索引走 `ORDER BY hlc`。
- **前端排序**: `hlc.localeCompare(bhlc)` 即得到全局因果序,无需后端按时间排。

---

## 5. UUID 派生 (derived_id.go)

### 5.1 文件与体量

源文件: `internal/app/derived_id.go` (39 行)

### 5.2 命名空间 UUIDs

```go
var (
    identityNS     = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
    teamNS         = uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
    shareGroupNS   = uuid.MustParse("6ba7b814-9dad-11d1-80b4-00c04fd430c8")
    conversationNS = uuid.MustParse("6ba7b815-9dad-11d1-80b4-00c04fd430c8")
)
```

四个不同命名空间确保**身份 UUID / 团队 UUID / 共享组 UUID / 会话 UUID 永远不会撞车**。`6ba7b81x-9dad-11d1-80b4-00c04fd430c8` 是 RFC 4122 推荐的命名空间家族前缀 (最后一个 hex 区分类型)。

### 5.3 通用 v5 派生

```go
func deriveUUID(ns uuid.UUID, data []byte) string {
    return uuid.NewHash(sha256.New(), ns, data, 5).String()
}
```

`uuid.NewHash` 是 `github.com/google/uuid` 提供的标准 v5 (SHA-256) 实现,接受 namespace + data + version,返回带连字符的 36 字符串。

### 5.4 团队会话 ID

```go
func DeriveTeamConvID(teamID string) string {
    hash := sha256.Sum256([]byte("team:" + teamID))
    return uuid.NewHash(sha256.New(), conversationNS, hash[:], 5).String()
}
```

逻辑:

1. 先把 `teamID` 用 `team:` 前缀包一层,**防止** `"abc"` 这种短串和私聊双方 ID 撞 (虽然下面 `private:` 前缀已经分开,这里多加一层防御)
2. SHA-256 一次得到 32 字节摘要
3. 用 `conversationNS` 做 v5 派生

**性质**: 同一 `teamID` 跨设备、跨进程算出同一个 UUID,前端靠它去重 / 路由消息。

### 5.5 私聊会话 ID

```go
func DerivePrivateConvID(myUUID, peerUUID string) string {
    a, b := myUUID, peerUUID
    if a > b { a, b = b, a }  // 字典序排序
    hash := sha256.Sum256([]byte("private:" + a + ":" + b))
    return uuid.NewHash(sha256.New(), conversationNS, hash[:], 5).String()
}
```

**核心性质: 交换律**。`DerivePrivateConvID("alice", "bob") == DerivePrivateConvID("bob", "alice")`。

这意味着 Alice 和 Bob 无需协商,各自在自己机器上打开私聊窗口,后端会算出同一个 `convID`,消息路由、DB 索引都不需要中心协调。

### 5.6 共享组 UUID

共享组 (文���共享场景) 通过通用 `deriveUUID(shareGroupNS, data)` 派生,具体 data 格式因调用方而异 (通常包含共享根路径 + 团队 ID)。

### 5.7 测试保证

`derived_id_test.go` (97 行) 覆盖:

- 同一 `teamID` 多次调用结果相同 (deterministic)
- 不同 `teamID` 结果不同
- 私聊顺序无关 (commutative)
- 私聊 ID 不会撞团队 ID (`private:abc:bob` vs `team:abc`)
- 输出永远是 UUID v5,可用 `uuid.Parse` 反解
- 自调用 `DerivePrivateConvID("same", "same")` 仍产生合法 UUID
- 实现确实走 SHA-256 (而不是直接 base64)

---

## 6. 测试约定

### 6.1 总体原则

- **Mock 优先**: 所有跨模块依赖 (NetworkEngine, FileEngine, Frontend) 都用 mock,不真实起服务 / 写盘。
- **In-package 单元测试**: `app_test.go` / `derived_id_test.go` 都用 `package app`,可访问非导出字段 (`a.crypto`, `a.database`, `a.evBus`)。
- **External 集成测试**: `system_integration_test.go` 用 `package app_test`,只通过 `app.NewApp` 公开 API 验证,模拟外部用户的真实使用路径。
- **无 Makefile,无 lint 脚本**: 全部依赖标准 `go` 工具链。

### 6.2 Mock 命名

文件: `internal/app/app_test.go:18-69`

| Mock | 实现的接口 | 关键字段 |
|:---|:---|:---|
| `MockNetwork` | `NetworkEngine` | `LastPayload []byte` |
| `MockFile` | `FileEngine` | (无字段,所有方法返回零值) |
| `MockUI` | `Frontend` | `EventName string`, `Payload interface{}` |

所有 mock 都是**零值可用**的 struct,直接 `&MockNetwork{}` 即可传给 `NewApp`。`MockNetwork` 会把最后一次 `BroadcastTeam` 的 payload 存到 `LastPayload`,供后续断言解密结果用。`MockUI` 则把最后一次 `Notify` 调用的事件名和载荷暴露出来。

### 6.3 setup() 工厂

文件: `internal/app/app_test.go:71-91`

```go
func setup(t *testing.T) (*App, *MockNetwork, *MockUI, func()) {
    dbP, idP := "./test.db", "./test.id"
    _ = os.Remove(dbP)
    _ = os.Remove(idP)

    eb := eventbus.New()
    cr := crypto.NewEngine()
    d, _ := db.NewSQLiteDB(dbP)
    net := &MockNetwork{}
    file := &MockFile{}
    ui  := &MockUI{}

    app := NewApp(eb, cr, d, net, file, ui, nil)
    app.Startup(context.Background())

    return app, net, ui, func() {
        d.Close()
        os.Remove(dbP)
        os.Remove(idP)
    }
}
```

返回四元组的设计:

- 第一个是 `*App`,调用业务方法
- 第二个是 `*MockNetwork`,断言网络层
- 第三个是 `*MockUI`,断言前端推送
- 第四个是 `cleanup` 闭包,统一在 `defer cleanup()` 里清理 DB 与身份文件

### 6.4 标准测试流程 (TestApp_FullFlow)

文件: `internal/app/app_test.go:93-132`

```go
func TestApp_FullFlow(t *testing.T) {
    a, net, ui, cleanup := setup(t)
    defer cleanup()

    // 1. 三步引导 (强制顺序: 没有身份 -> 不能登录 -> 不能加入团队)
    _ = a.Register("123")
    _ = a.Login("123")
    _ = a.JoinTeam("team")

    // 2. 业务动作
    txt := "Hello World"
    _ = a.SendTeamChat(txt)

    // 3. 验证: DB 内容
    hist, _ := a.GetConversationMessages(DeriveTeamConvID(a.crypto.GetActiveTeam()), 1, 0)
    if hist[0]["content"] != txt { t.Errorf("DB content mismatch") }

    // 4. 验证: 网络层 payload (解密后比对原文)
    dec, _ := a.crypto.DecryptTeam(net.LastPayload)
    var netPayload eventbus.MsgReceivedPayload
    _ = json.Unmarshal(dec, &netPayload)
    if string(netPayload.Content) != txt { t.Errorf("Network payload mismatch") }

    // 5. 模拟收到他人消息 -> 验证 UI 推送
    incoming := eventbus.MsgReceivedPayload{
        HLC:      "2026-04-13T12:00:00.000Z_0001_REMOTE",
        SenderID: "remote_node",
        Content:  []byte("Incoming Message"),
    }
    a.evBus.Publish(eventbus.TopicMsgReceived, incoming)
    time.Sleep(100 * time.Millisecond)  // 等异步订阅者处理

    if ui.EventName != "ui_new_message" { t.Errorf("UI not notified") }
    uiData := ui.Payload.(map[string]interface{})
    if uiData["content"] != "Incoming Message" { t.Errorf("UI content mismatch") }
}
```

**端到端覆盖**: DB 落盘 + 网络广播 + 异步事件总线 + 前端推送,全链路在一个测试里走完。

### 6.5 边界测试

#### TestGetRecentHistory_CorruptedMessage (app_test.go:134-163)

往 DB 插一条原始垃圾数据 `Content: []byte("this is not valid encrypted data")`,验证 `GetConversationMessages` 返回 `"[message corrupted]"` 占位符而不是 panic。这是**密码忘记后历史数据怎么显示**的关键路径。

#### TestSendTeamChat_ReceiverID (app_test.go:165-184)

确认群聊消息的 `ReceiverID` 字段恒为 `db.ReceiverIDGroupChat` (空串),私聊则用对方 UUID。区分群/私消息的过滤逻辑都靠这个字段。

### 6.6 集成测试 (system_integration_test.go)

**外部测试包** `package app_test`,只能调用 `app` 公开 API,模拟真实用户:

| 测试 | 验证点 |
|:---|:---|
| `TestSystem_CompleteUserFlow` | Auth -> Send -> Receive -> Restart,重启后消息持久化解密 |
| `TestSystem_JoinTeamReusesUUID` | 同一队伍口令二次加入复用 UUID (Issue 1 fix) |
| `TestSystem_LoginAutoStartsNetwork` | Login 检测到 `.parade_teams` 存在时自动 Start 网络层 (Issue 2 fix) |
| `TestSystem_NoDuplicateOnSelfSender` | 自己在事件总线上的回声消息不会二次入库 (Issue 4 fix) |

集成测试的 mock 单独命名为 `IntegrationMockNetwork` / `IntegrationMockFile` / `IntegrationMockUI`,字段稍有不同 (`BroadcastCount`, `StartCalled`, `LastEvent`),避免与单元测试 mock 误用。

### 6.7 测试产物与 gitignore

`.gitignore` 显式忽略:

```
test_*.db
test_*-wal
test_*-shm
```

说明:

- 单元测试用 `test.db` / `test.id`,严格走 `setup` 的 cleanup,通常不留痕
- 集成测试用 `integration_test.db` / `.parade_teams` 等
- **历史教训**: 早期测试会漏 `defer os.Remove`,`.gitignore` 加 `test_*.db` 是兜底
- `.parade.log` 也被忽略,日志输出走 5000 条环形 buffer,运行后会被覆盖

### 6.8 运行命令速查

```bash
go build ./...                                    # 编译全部
go test ./...                                     # 跑所有测试
go test ./internal/file/...                       # 单包
go test -run TestSaveChunkOutOfOrder ./internal/file/  # 单测试
go test -v -count=1 ./internal/file/              # 详细 + 跳过缓存
```

无 Makefile,无 lint,无 typecheck 脚本,标准 `go` 工具链足矣。

---

## 7. 运行时产物清单

所有产物默认放在**当前工作目录**,与二进制同级。

| 文件 | 用途 | 权限 | 何时产生 |
|:---|:---|:---|:---|
| `.parade_identity` | Curve25519 加密密钥对 | `0600` | 首次 `Register` |
| `.parade_teams` | 多队伍对称密钥环 | `0600` | 首次 `JoinTeam` |
| `.parade_data.db` | SQLite 数据库 (WAL 模式) | 默认 (0644) | 程序启动,主流程 |
| `.parade_peers` | 已知节点 JSON 列表 | 默认 | 网络层首次发现 peer |
| `.parade.log` | JSONL 日志 (5000 条环形) | 默认 | `LogBroker` 启动后持续写入 |

**安全要求**: `.parade_identity` 与 `.parade_teams` 绝对不能上传到 Git,`.gitignore` 用通配 `*.parade_identity` 兜底,但 `.parade_teams` 是字面量,需开发者自行检查。

---

## 8. 构建产物

Wails v2 跨平台构建,产物全部落在 `build/`:

| 产物 | 平台 | 体积 | 备注 |
|:---|:---|:---|:---|
| `build/bin/parade` | Linux ELF | ~18 MB | `wails build` 默认 |
| `build/bin/parade.exe` | Windows PE | ~37 MB | `wails build -platform windows/amd64` |
| `build/appicon.png` | 通用 | 132 KB | 应用图标源,各平台 ICO 派生自此 |
| `build/windows/icon.ico` | Windows | 22 KB | 打包进 .exe |
| `build/windows/info.json` | Windows | <1 KB | 版本/版权元数据 |
| `build/windows/wails.exe.manifest` | Windows | 1 KB | DPI 感知 / UAC 声明 |

`build/bin/` 与 `build/windows/` 都被 `.gitignore` 忽略 (`build/bin/`, `build/windows/`, `build/darwin/`, `build/linux/`),仓库只保留源码与 `appicon.png` 源图。

---

## 9. 关键设计原则回顾

1. **依赖单向**: `main.go` 是唯一允许把所有引擎硬连在一起的地方,业务模块 `app.App` 只通过接口收依赖,方便测试。
2. **Wails Context 延迟注入**: 不在 `NewWailsUI` 时绑定 ctx,而是 `OnStartup` 回调里 SetContext,避免空 ctx 调用 NPE。
3. **Single Instance Lock 必备**: 桌面应用多开会损坏 SQLite WAL 状态,Wails 自带 OS 级互斥锁,UI 上唤起已有窗口即可。
4. **HLC 字符串即排序键**: 前后端共用同一格式,前端 `localeCompare` 就拿到全局因果序,后端 DB `ORDER BY hlc` 也无歧义。
5. **UUID 派生零协调**: 团队会话 + 私聊会话都用纯函数从已有 ID 派生,无状态、无锁、跨设备结果一致。
6. **Mock 三件套**: Network / File / UI 是测试的三大抓手,只要它们接口稳定,业务逻辑可以无 I/O 跑通。

---

## 10. 参考源文件索引

| 路径 | 行数 | 主题 |
|:---|:---|:---|
| `main.go` | 100 | Wails 入口、初始化顺序、SingleInstanceLock |
| `wails.json` | 13 | Wails 构建配置 |
| `internal/core/crypto/keystore.go` | 145 | 身份文件 Register/Load |
| `internal/app/hlc.go` | 24 | 混合逻辑时钟 |
| `internal/app/derived_id.go` | 39 | UUID v5 派生 |
| `internal/app/interfaces.go` | 39 | Mock 实现的三个接口 |
| `internal/app/app_test.go` | 185 | 单元测试、Mock、setup |
| `internal/app/derived_id_test.go` | 97 | 派生确定性 / 交换律测试 |
| `internal/app/system_integration_test.go` | 367 | 外部包集成测试,4 个 issue fix 验证 |
| `.gitignore` | 83 | 运行时/构建/测试产物排除规则 |
