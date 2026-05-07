package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
)

// ---- 基础 AES-GCM 工具函数 ----

// aesGCMEncrypt 使用 AES-GCM 加密数据，并将 Nonce 附加在密文头部
func aesGCMEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// 返回格式: [Nonce] + [Ciphertext]
	return aesgcm.Seal(nonce, nonce, plaintext, nil), nil
}

// aesGCMDecrypt 解析 Nonce 并解密
func aesGCMDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesgcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, actualCipher := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return aesgcm.Open(nil, nonce, actualCipher, nil)
}

// ---- 落盘加密实现 ----

func (c *paradeCrypto) EncryptLocal(plaintext []byte) ([]byte, error) {
	if c.masterKey == nil {
		return nil, errors.New("identity not loaded")
	}
	return aesGCMEncrypt(c.masterKey, plaintext)
}

func (c *paradeCrypto) DecryptLocal(ciphertext[]byte) ([]byte, error) {
	if c.masterKey == nil {
		return nil, errors.New("identity not loaded")
	}
	return aesGCMDecrypt(c.masterKey, ciphertext)
}

// ---- 队伍网络加密实现 ----

func (c *paradeCrypto) SetTeamKey(teamPassword string) {
	c.SetTeamKeyForTeam("", teamPassword)
}

func (c *paradeCrypto) SetTeamKeyForTeam(teamID, teamPassword string) {
	if c.teamKeys == nil {
		c.teamKeys = make(map[string][]byte)
	}
	hash := sha256.Sum256([]byte(teamPassword))
	c.teamKeys[teamID] = hash[:]
	if c.activeTeam == "" {
		c.activeTeam = teamID
	}
}

func (c *paradeCrypto) RemoveTeamKey(teamID string) {
	delete(c.teamKeys, teamID)
	if c.activeTeam == teamID {
		c.activeTeam = ""
		for id := range c.teamKeys {
			c.activeTeam = id
			break
		}
	}
}

func (c *paradeCrypto) SetActiveTeam(teamID string) error {
	if _, ok := c.teamKeys[teamID]; !ok {
		return fmt.Errorf("team key not found: %s", teamID)
	}
	c.activeTeam = teamID
	return nil
}

func (c *paradeCrypto) GetActiveTeam() string {
	return c.activeTeam
}

func (c *paradeCrypto) GetTeamIDs() []string {
	ids := make([]string, 0, len(c.teamKeys))
	for id := range c.teamKeys {
		ids = append(ids, id)
	}
	return ids
}

func (c *paradeCrypto) TeamKeyHash() string {
	return c.TeamKeyHashFor(c.activeTeam)
}

func (c *paradeCrypto) TeamKeyHashFor(teamID string) string {
	key, ok := c.teamKeys[teamID]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(key))
}

func (c *paradeCrypto) EncryptTeam(plaintext []byte) ([]byte, error) {
	key, ok := c.teamKeys[c.activeTeam]
	if !ok {
		return nil, errors.New("team key not set")
	}
	return aesGCMEncrypt(key, plaintext)
}

func (c *paradeCrypto) DecryptTeam(ciphertext []byte) ([]byte, error) {
	key, ok := c.teamKeys[c.activeTeam]
	if !ok {
		return nil, errors.New("team key not set")
	}
	return aesGCMDecrypt(key, ciphertext)
}

func (c *paradeCrypto) DecryptTeamForTeam(teamID string, ciphertext []byte) ([]byte, error) {
	key, ok := c.teamKeys[teamID]
	if !ok {
		return nil, fmt.Errorf("team key not found for team: %s", teamID)
	}
	return aesGCMDecrypt(key, ciphertext)
}

// ---- 私聊 E2E 端到端加密实现 ----

// ECDH: 使用我的私钥和对方的公钥协商出一个只有我们俩知道的会话密钥
func (c *paradeCrypto) getSessionKey(remotePubKeyBase64 string) ([]byte, error) {
	if c.privKey == nil {
		return nil, errors.New("identity not loaded")
	}

	remotePubKey, err := base64.StdEncoding.DecodeString(remotePubKeyBase64)
	if err != nil || len(remotePubKey) != 32 {
		return nil, errors.New("invalid remote public key")
	}

	var sharedSecret [32]byte
	var myPriv [32]byte
	var theirPub [32]byte
	copy(myPriv[:], c.privKey)
	copy(theirPub[:], remotePubKey)

	// X25519 标量乘法 (核心魔法：A_priv * B_pub == B_priv * A_pub)
	curve25519.ScalarMult(&sharedSecret, &myPriv, &theirPub)

	// 使用 SHA-256 作为简单的 KDF (密钥派生函数)，强化安全性
	sessionKey := sha256.Sum256(sharedSecret[:])
	return sessionKey[:], nil
}

func (c *paradeCrypto) EncryptPrivate(plaintext []byte, remotePubKeyBase64 string) ([]byte, error) {
	sessionKey, err := c.getSessionKey(remotePubKeyBase64)
	if err != nil {
		return nil, err
	}
	return aesGCMEncrypt(sessionKey, plaintext)
}

func (c *paradeCrypto) DecryptPrivate(ciphertext[]byte, remotePubKeyBase64 string) ([]byte, error) {
	sessionKey, err := c.getSessionKey(remotePubKeyBase64)
	if err != nil {
		return nil, err
	}
	return aesGCMDecrypt(sessionKey, ciphertext)
}
