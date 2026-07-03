# 加密模块接口说明

本模块是“游行”软件的安全基石，基于 **Argon2id** (密钥派生)、**AES-256-GCM** (对称加密) 和 **Curve25519** (非对称密钥交换) 构建。

## 1. 密钥体系 

| 密钥类型 | 派生来源 | 保护范围 |
| :--- | :--- | :--- |
| **用户主密钥 (Master)** | 用户密码 + 随机盐 | 负责加密本地私钥文件及 SQLite 中的所有 `Content` 数据。 |
| **队伍密钥 (Team)** | 队伍口令 (SHA-256) | 负责局域网内的所有群聊、系统信令和文件元数据传输。 |
| **私聊密钥 (Session)** | 双方 Curve25519 协商 | 负责成员间的 1 对 1 私聊，实现双重加密（队伍加密之上再加私聊加密）。 |

## 2. 接口方法与调用场景

### A. 身份管理

| 方法 | 调用者 | 调用场景 |
| :--- | :--- | :--- |
| **`RegisterIdentity`** | 逻辑层 | **“注册”或“首次启动”**。生成本地凭证文件 `.parade_identity`。 |
| **`LoadIdentity`** | 逻辑层 | **“登录”**。验证用户密码并解密私钥至内存。 |
| **`GetPublicKeyBase64`**| 网络层 | 获取本节点的“身份证号”，用于向局域网广播自己。 |

### B. 加解密操作

| 方法 | 对应场景 | 安全逻辑 |
| :--- | :--- | :--- |
| **`EncryptLocal`** | **落盘存储** | 将明文消息加密后交给数据库模块存入 `messages` 表。 |
| **`EncryptTeam`** | **局域网群聊** | 将数据包裹成信封发往 4327 端口。同队伍成员均可解密。 |
| **`EncryptPrivate`** | **一对一私聊** | 基于目标公钥协商密钥。**只有目标成员能解开**，其他人（即使在同队）无法旁听。 |

## 3. 快速上手示例

### 注册与登录
```go
cryptoEng := crypto.NewEngine()

// 1. 注册（仅第一次）
cryptoEng.RegisterIdentity("my-password", "./user.parade_identity")

// 2. 启动加载
err := cryptoEng.LoadIdentity("my-password", "./user.parade_identity")
if err != nil {
    // 提示密码错误或文件损坏
}
```

### 发送私聊消息 (网络层逻辑)
```go
// 1. 原始文本
rawMsg := []byte("你好，这是私聊")

// 2. 第一层：私聊加密（指定对方公钥）
privateSecret, _ := cryptoEng.EncryptPrivate(rawMsg, remotePubBase64)

// 3. 第二层：队伍加密（确保局域网安全）
finalEnvelope, _ := cryptoEng.EncryptTeam(privateSecret)

// 4. 发送 finalEnvelope...
```

## 4. 安全规范 (必读)

1.  **内存安全**：`privKey` 和 `masterKey` 仅存在于内存中，严禁将其记录到日志或存入数据库。
2.  **凭证保护**：`.parade_identity` 文件包含加密后的私钥，逻辑层应提醒用户妥善备份该文件，**丢失或忘记密码将导致所有历史数据无法解密**。
3.  **队伍口令**：队伍口令不设“盐”，因为局域网节点间没有中心服务器共享盐。请提醒用户尽量使用复杂的队伍口令。
4.  **Base64 约定**：所有在网络传输、数据库索引中出现的公钥，统一采用 **Standard Base64** 编码字符串。
