package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	masterKey[]byte // 主密钥 (Argon2推导，用于解密本地数据库)
	privKey[]byte // 我的私钥 (Curve25519, 32字节)
	pubKey[]byte // 我的公钥 (Curve25519, 32字节)
	teamKey[]byte // 队伍对称密钥
}

func NewEngine() Engine {
	return &paradeCrypto{}
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

	return nil
}

func (c *paradeCrypto) GetPublicKeyBase64() string {
	if c.pubKey == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(c.pubKey)
}
