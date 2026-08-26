package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const (
	TestPwd  = "correct-password-123"
	WrongPwd = "wrong-password-456"
)

func identityPaths(t testing.TB) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "alice.parade_identity"), filepath.Join(dir, "bob.parade_identity")
}

// TestIdentityLifecycle 验证：注册 -> 文件持久化 -> 正确密码加载 -> 错误密码拒绝
func TestIdentityLifecycle(t *testing.T) {
	identityPath, _ := identityPaths(t)

	engine := NewEngine()

	// 1. 注册身份
	err := engine.RegisterIdentity(TestPwd, identityPath)
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
	err = anotherEngine.LoadIdentity(WrongPwd, identityPath)
	if err == nil {
		t.Error("LoadIdentity should fail with wrong password")
	}

	// 3. 尝试用正确密码加载
	err = anotherEngine.LoadIdentity(TestPwd, identityPath)
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
	identityPath, _ := identityPaths(t)

	engine := NewEngine()
	if err := engine.RegisterIdentity(TestPwd, identityPath); err != nil {
		t.Fatalf("register identity: %v", err)
	}

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

	if err := engineA.SetTeamKey(teamPwd); err != nil {
		t.Fatalf("SetTeamKey failed: %v", err)
	}
	if err := engineB.SetTeamKey(teamPwd); err != nil {
		t.Fatalf("SetTeamKey failed: %v", err)
	}

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
	identityPath, bobPath := identityPaths(t)

	alice := NewEngine()
	bob := NewEngine()

	// 1. 两人各自初始化身份
	if err := alice.RegisterIdentity("pwd1", identityPath); err != nil {
		t.Fatalf("register Alice: %v", err)
	}
	if err := bob.RegisterIdentity("pwd2", bobPath); err != nil {
		t.Fatalf("register Bob: %v", err)
	}

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
	if err := malicious.SetTeamKey("any-team-pwd"); err != nil {
		t.Fatalf("SetTeamKey failed: %v", err)
	}
	_, err = malicious.DecryptPrivate(encryptedForBob, alicePub)
	if err == nil {
		t.Error("Malicious user should not be able to decrypt private chat without Alice or Bob's private key")
	}
}

// TestTeamKeysPersistToConfiguredPath verifies that team keys are persisted to
// the engine's configured path and restored on a fresh engine with the same path.
func TestTeamKeysPersistToConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	keysFile := filepath.Join(dir, TeamKeysFileName)
	idFile := filepath.Join(dir, ".parade_identity")

	engine := NewEngine(WithTeamKeysFile(keysFile))
	if err := engine.RegisterIdentity(TestPwd, idFile); err != nil {
		t.Fatalf("RegisterIdentity failed: %v", err)
	}
	if err := engine.SetTeamKeyForTeam("team-a", "secret-a"); err != nil {
		t.Fatalf("SetTeamKeyForTeam failed: %v", err)
	}

	if _, err := os.Stat(keysFile); err != nil {
		t.Fatalf("team keys file not created at configured path %s: %v", keysFile, err)
	}

	reloaded := NewEngine(WithTeamKeysFile(keysFile))
	if err := reloaded.LoadIdentity(TestPwd, idFile); err != nil {
		t.Fatalf("LoadIdentity failed: %v", err)
	}
	if ids := reloaded.GetTeamIDs(); len(ids) != 1 || ids[0] != "team-a" {
		t.Errorf("team keys not restored from configured path, got %v", ids)
	}
	if reloaded.GetActiveTeam() != "team-a" {
		t.Errorf("active team not restored, got %q", reloaded.GetActiveTeam())
	}
}

// TestTeamKeysIsolatedAcrossPaths verifies that two identities with distinct
// team-key paths never share team keys, even after reload.
func TestTeamKeysIsolatedAcrossPaths(t *testing.T) {
	dirA := t.TempDir()
	keysA := filepath.Join(dirA, TeamKeysFileName)
	idA := filepath.Join(dirA, ".parade_identity")
	dirB := t.TempDir()
	keysB := filepath.Join(dirB, TeamKeysFileName)
	idB := filepath.Join(dirB, ".parade_identity")

	engineA := NewEngine(WithTeamKeysFile(keysA))
	if err := engineA.RegisterIdentity("pwd-a", idA); err != nil {
		t.Fatalf("RegisterIdentity A failed: %v", err)
	}
	if err := engineA.SetTeamKeyForTeam("team-a", "secret-a"); err != nil {
		t.Fatalf("SetTeamKeyForTeam A failed: %v", err)
	}

	engineB := NewEngine(WithTeamKeysFile(keysB))
	if err := engineB.RegisterIdentity("pwd-b", idB); err != nil {
		t.Fatalf("RegisterIdentity B failed: %v", err)
	}
	if err := engineB.SetTeamKeyForTeam("team-b", "secret-b"); err != nil {
		t.Fatalf("SetTeamKeyForTeam B failed: %v", err)
	}

	reloadA := NewEngine(WithTeamKeysFile(keysA))
	if err := reloadA.LoadIdentity("pwd-a", idA); err != nil {
		t.Fatalf("LoadIdentity A failed: %v", err)
	}
	if ids := reloadA.GetTeamIDs(); len(ids) != 1 || ids[0] != "team-a" {
		t.Errorf("identity A team keys contaminated, got %v", ids)
	}

	reloadB := NewEngine(WithTeamKeysFile(keysB))
	if err := reloadB.LoadIdentity("pwd-b", idB); err != nil {
		t.Fatalf("LoadIdentity B failed: %v", err)
	}
	if ids := reloadB.GetTeamIDs(); len(ids) != 1 || ids[0] != "team-b" {
		t.Errorf("identity B team keys contaminated, got %v", ids)
	}
}

// TestTeamKeysNoCWDFileWhenConfigured verifies that a configured engine never
// creates the old CWD-relative .parade_teams artifact.
func TestTeamKeysNoCWDFileWhenConfigured(t *testing.T) {
	cwdKeys := filepath.Join(".", TeamKeysFileName)
	_ = os.Remove(cwdKeys)
	t.Cleanup(func() { _ = os.Remove(cwdKeys) })

	dir := t.TempDir()
	keysFile := filepath.Join(dir, TeamKeysFileName)
	idFile := filepath.Join(dir, ".parade_identity")

	engine := NewEngine(WithTeamKeysFile(keysFile))
	if err := engine.RegisterIdentity(TestPwd, idFile); err != nil {
		t.Fatalf("RegisterIdentity failed: %v", err)
	}
	if err := engine.SetTeamKeyForTeam("team-x", "secret-x"); err != nil {
		t.Fatalf("SetTeamKeyForTeam failed: %v", err)
	}

	if _, err := os.Stat(cwdKeys); !os.IsNotExist(err) {
		t.Errorf("unconfigured CWD %s was created (stat err=%v)", cwdKeys, err)
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
