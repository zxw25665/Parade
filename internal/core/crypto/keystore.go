package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/curve25519"
)

// IdentityFile 是存入磁盘的凭证格式
type IdentityFile struct {
	Salt          []byte `json:"salt"`           // Argon2 需要的盐
	EncryptedPriv[]byte `json:"encrypted_priv"` // AES-GCM 加密后的 Curve25519 私钥
	PubKey[]byte `json:"pub_key"`        // 对应的公钥（明文保存，方便查阅）
}

// paradeCrypto 实现了 Engine 接口
type paradeCrypto struct {
	masterKey     []byte            // 主密钥 (Argon2推导，用于解密本地数据库)
	privKey       []byte            // 我的私钥 (Curve25519, 32字节)
	pubKey        []byte            // 我的公钥 (Curve25519, 32字节)
	personalUUID  string            // deterministic UUID derived from pubkey
	teamKeys      map[string][]byte // 多队伍对称密钥环
	activeTeam    string            // 当前活跃的队伍 ID
	loadWarnings  []error           // non-fatal warnings from LoadIdentity
	teamKeysFile  string            // 队伍密钥持久化路径 (空 = 仅内存，不落盘)
}

// Option 配置新建的 crypto 引擎。
type Option func(*paradeCrypto)

// WithTeamKeysFile 设置加密队伍密钥的持久化路径（通常为 <data-dir>/.parade_teams）。
// 未设置时队伍密钥仅保存在内存中，不读写任何磁盘文件。
func WithTeamKeysFile(path string) Option {
	return func(c *paradeCrypto) {
		c.teamKeysFile = path
	}
}

func NewEngine(opts ...Option) Engine {
	c := &paradeCrypto{
		teamKeys: make(map[string][]byte),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// 派生主密钥 (使用 Argon2id)
func deriveMasterKey(password string, salt[]byte)[]byte {
	// 参数设置：1次迭代，64MB内存，4个线程，生成32字节长度的AES密钥
	return argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
}

func (c *paradeCrypto) RegisterIdentity(password, filepath string) error {
	// 1. 生成随机的盐 (16 bytes)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}

	// 2. 生成 Curve25519 密钥对
	var privKey, pubKey [32]byte
	if _, err := rand.Read(privKey[:]); err != nil {
		return err
	}
	curve25519.ScalarBaseMult(&pubKey, &privKey)

	// 3. 推导主密钥
	masterKey := deriveMasterKey(password, salt)

	// 4. 用主密钥加密私钥
	encryptedPriv, err := aesGCMEncrypt(masterKey, privKey[:])
	if err != nil {
		return err
	}

	// 5. 保存到磁盘
	idFile := IdentityFile{
		Salt:          salt,
		EncryptedPriv: encryptedPriv,
		PubKey:        pubKey[:],
	}
	data, _ := json.MarshalIndent(idFile, "", "  ")
	
	// 设置 0600 权限，仅当前系统用户可读写
	if err := os.WriteFile(filepath, data, 0600); err != nil {
		return fmt.Errorf("failed to save identity file: %w", err)
	}

	return c.LoadIdentity(password, filepath) // 注册完自动加载到内存
}

func (c *paradeCrypto) LoadIdentity(password, filepath string) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("identity file not found: %w", err)
	}

	var idFile IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return errors.New("corrupted identity file")
	}

	// 1. 推导主密钥
	masterKey := deriveMasterKey(password, idFile.Salt)

	// 2. 尝试解密私钥
	privKey, err := aesGCMDecrypt(masterKey, idFile.EncryptedPriv)
	if err != nil {
		return errors.New("invalid password") // 解密失败说明密码错误
	}

	// 3. 加载到内存
	c.masterKey = masterKey
	c.privKey = privKey
	c.pubKey = idFile.PubKey
	c.personalUUID = uuid.NewHash(sha256.New(), uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8"), idFile.PubKey, 5).String()
	if c.teamKeys == nil {
		c.teamKeys = make(map[string][]byte)
	}

	// loadTeamKeys errors are non-fatal; collect as warnings
	if err := c.loadTeamKeys(); err != nil {
		c.loadWarnings = append(c.loadWarnings, err)
	}

	return nil
}

func (c *paradeCrypto) GetPublicKeyBase64() string {
	if c.pubKey == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(c.pubKey)
}

func (c *paradeCrypto) GetPrivateKey() []byte {
	if c.privKey == nil {
		return nil
	}
	clone := make([]byte, len(c.privKey))
	copy(clone, c.privKey)
	return clone
}

func (c *paradeCrypto) IdentityLoadWarnings() []error {
	return c.loadWarnings
}

func (c *paradeCrypto) GetPersonalUUID() string {
	return c.personalUUID
}
