package crypto

import (
	"bytes"
	"os"
	"testing"
)

const (
	TestPwd      = "correct-password-123"
	WrongPwd     = "wrong-password-456"
	IdentityPath = "./test_alice.parade_identity"
	BobPath      = "./test_bob.parade_identity"
)

// TestIdentityLifecycle 验证：注册 -> 文件持久化 -> 正确密码加载 -> 错误密码拒绝
func TestIdentityLifecycle(t *testing.T) {
	_ = os.Remove(IdentityPath)
	defer os.Remove(IdentityPath)

	engine := NewEngine()

	// 1. 注册身份
	err := engine.RegisterIdentity(TestPwd, IdentityPath)
	if err != nil {
		t.Fatalf("Failed to register identity: %v", err)
	}

	// 记录公钥用于对比
	pubKey1 := engine.GetPublicKeyBase64()
	if pubKey1 == "" {
		t.Fatal("Public key should not be empty")
	}

	// 2. 尝试用错误密码加载
	anotherEngine := NewEngine()
	err = anotherEngine.LoadIdentity(WrongPwd, IdentityPath)
	if err == nil {
		t.Error("LoadIdentity should fail with wrong password")
	}

	// 3. 尝试用正确密码加载
	err = anotherEngine.LoadIdentity(TestPwd, IdentityPath)
	if err != nil {
		t.Fatalf("Failed to load identity with correct password: %v", err)
	}

	// 验证公钥是否一致
	if anotherEngine.GetPublicKeyBase64() != pubKey1 {
		t.Error("Public key mismatch after reloading")
	}
}

// TestLocalEncryption 验证：落盘加密的数据是否能正确还原
func TestLocalEncryption(t *testing.T) {
	_ = os.Remove(IdentityPath)
	defer os.Remove(IdentityPath)

	engine := NewEngine()
	_ = engine.RegisterIdentity(TestPwd, IdentityPath)

	originalText := []byte("这是一段需要存入数据库的私密聊天记录")

	// 加密
	ciphertext, err := engine.EncryptLocal(originalText)
	if err != nil {
		t.Fatalf("Local encryption failed: %v", err)
	}

	if bytes.Equal(originalText, ciphertext) {
		t.Error("Ciphertext should not be same as plaintext")
	}

	// 解密
	plaintext, err := engine.DecryptLocal(ciphertext)
	if err != nil {
		t.Fatalf("Local decryption failed: %v", err)
	}

	if !bytes.Equal(originalText, plaintext) {
		t.Error("Decrypted text does not match original")
	}
}

// TestTeamEncryption 验证：同一队伍口令下，数据是否互通
func TestTeamEncryption(t *testing.T) {
	teamPwd := "group-secret-key"
	engineA := NewEngine()
	engineB := NewEngine()

	engineA.SetTeamKey(teamPwd)
	engineB.SetTeamKey(teamPwd)

	raw := []byte("局域网群聊数据")

	// A 加密
	encrypted, _ := engineA.EncryptTeam(raw)
	// B 解密
	decrypted, err := engineB.DecryptTeam(encrypted)

	if err != nil {
		t.Fatalf("Team decryption failed: %v", err)
	}
	if !bytes.Equal(raw, decrypted) {
		t.Error("Team data mismatch")
	}
}

// TestPrivateChatEncryption 验证：Alice 与 Bob 的 ECDH 协商加密 (跨用户私聊)
func TestPrivateChatEncryption(t *testing.T) {
	_ = os.Remove(IdentityPath)
	_ = os.Remove(BobPath)
	defer os.Remove(IdentityPath)
	defer os.Remove(BobPath)

	alice := NewEngine()
	bob := NewEngine()

	// 1. 两人各自初始化身份
	_ = alice.RegisterIdentity("pwd1", IdentityPath)
	_ = bob.RegisterIdentity("pwd2", BobPath)

	alicePub := alice.GetPublicKeyBase64()
	bobPub := bob.GetPublicKeyBase64()

	// 2. 模拟 Alice 给 Bob 发送私密消息
	messageToBob := []byte("嘿 Bob，这是只有我们俩能看到的私聊")
	encryptedForBob, err := alice.EncryptPrivate(messageToBob, bobPub)
	if err != nil {
		t.Fatalf("Alice failed to encrypt for Bob: %v", err)
	}

	// 3. 模拟 Bob 收到并解密
	decryptedByBob, err := bob.DecryptPrivate(encryptedForBob, alicePub)
	if err != nil {
		t.Fatalf("Bob failed to decrypt Alice's message: %v", err)
	}

	if !bytes.Equal(messageToBob, decryptedByBob) {
		t.Error("Private chat content mismatch")
	}

	// 4. 验证：如果恶意第三方尝试解密（即便他知道队伍口令）
	malicious := NewEngine()
	malicious.SetTeamKey("any-team-pwd")
	_, err = malicious.DecryptPrivate(encryptedForBob, alicePub)
	if err == nil {
		t.Error("Malicious user should not be able to decrypt private chat without Alice or Bob's private key")
	}
}

// BenchmarkArgon2 性能测试：查看 Argon2 派生密钥耗时
// 预期在 100ms - 500ms 左右，这是正常的，太快说明安全性不足
func BenchmarkArgon2(b *testing.B) {
	salt := make([]byte, 16)
	for i := 0; i < b.N; i++ {
		_ = deriveMasterKey("password-to-bench", salt)
	}
}
