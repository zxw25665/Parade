package crypto

// Engine 定义了游行系统所有的密码学和身份操作契约
type Engine interface {
	// ---- 身份与密钥管理 ----
	// 注册新身份：生成 Curve25519 密钥对，通过密码+盐(Argon2)加密私钥并存入凭证文件
	RegisterIdentity(password, filepath string) error
	// 登录加载身份：读取凭证文件，用密码解密出私钥，并在内存中推导主密钥
	LoadIdentity(password, filepath string) error
	// 获取当前成员的公钥 (Base64格式)，用于网络层广播自己的身份
	GetPublicKeyBase64() string
	// 获取当前成员的 Curve25519 私钥 (32字节, 原始字节)，用于派生 libp2p 身份密钥
	// 仅在 LoadIdentity 后可用，否则返回 nil
	GetPrivateKey() []byte
	// 返回从公钥确定性派生的个人 UUID（相同身份在不同设备上一致）
	GetPersonalUUID() string
	// 返回 LoadIdentity 期间收集的非致命警告（如队伍密钥文件损坏）
	IdentityLoadWarnings() []error
	SaveTeamKeys() error
	// SetTeamKeysFile 配置队伍密钥持久化路径；空路径表示仅内存保存
	SetTeamKeysFile(path string)
	// 设置队伍口令，内部转换为队伍对称密钥
	SetTeamKey(teamPassword string) error
	// 返回队伍密钥的十六进制哈希，用于 mDNS TXT 同队过滤
	TeamKeyHash() string

	// ---- 多队伍密钥管理 ----
	SetTeamKeyForTeam(teamID, teamPassword string) error
	RemoveTeamKey(teamID string) error
	SetActiveTeam(teamID string) error
	GetActiveTeam() string
	GetTeamIDs() []string
	TeamKeyHashFor(teamID string) string
	DecryptTeamForTeam(teamID string, ciphertext []byte) ([]byte, error)

	// ---- 核心加解密机制 ----
	// 落盘加密：使用用户主密钥加密（落盘前调用）
	EncryptLocal(plaintext []byte) ([]byte, error)
	DecryptLocal(ciphertext []byte) ([]byte, error)

	// 队伍加密：使用队伍对称密钥加密（发往局域网前调用）
	EncryptTeam(plaintext []byte) ([]byte, error)
	DecryptTeam(ciphertext []byte) ([]byte, error)

	// 私聊加密：使用目标公钥(ECDH)协商出临时会话密钥，双重加密
	EncryptPrivate(plaintext []byte, remotePubKeyBase64 string) ([]byte, error)
	DecryptPrivate(ciphertext[]byte, remotePubKeyBase64 string) ([]byte, error)
}
