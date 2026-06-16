package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// subtleConstantTimeEqual 以常量时间比较两个字符串，避免 token 比对的时序侧信道。
func subtleConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// hashToken 返回明文 token 的 SHA256 十六进制摘要，用于 token 去重的唯一索引。
// 哈希不可逆，落库后无法还原 token，安全；空 token 返回空串（不参与唯一约束）。
func hashToken(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// secretCodec 负责对落库的敏感字段（API token、上游 api_key）做透明
// 加解密。设计目标：
//   - 拿到 .sqlite3 文件而没有 master key 时，无法还原任何密钥（修复阻断项2：
//     旧实现把明文密钥直接写进 SQLite，使配置层的 AES-GCM 加密形同虚设）。
//   - 向后兼容：未启用 key 时按明文存取；已有明文行在读到时原样返回，
//     下次写入自动升级为密文。
//
// 密文格式： "enc:v1:" + base64(nonce || ciphertext)，AES-256-GCM。
// 明文（无前缀）按原样返回，因此可与历史明文数据共存并平滑迁移。
type secretCodec struct {
	gcm cipher.AEAD // nil 表示未启用加密（明文模式）
}

const secretPrefix = "enc:v1:"

// newSecretCodec 用 32 字节 key 构造 AES-256-GCM 编解码器。
// key 为空切片时返回明文模式编解码器（gcm=nil）。
func newSecretCodec(key []byte) (*secretCodec, error) {
	if len(key) == 0 {
		return &secretCodec{}, nil
	}
	// 统一派生为 32 字节，便于接受任意长度的口令。
	sum := sha256.Sum256(key)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretCodec{gcm: gcm}, nil
}

// enabled 报告是否处于加密模式。
func (c *secretCodec) enabled() bool { return c != nil && c.gcm != nil }

// encrypt 把明文加密为 "enc:v1:..." 字符串。明文模式或空串时原样返回。
func (c *secretCodec) encrypt(plain string) (string, error) {
	if !c.enabled() || plain == "" {
		return plain, nil
	}
	// 已是密文则不二次加密（幂等，避免重复 Upsert 时层层包裹）。
	if strings.HasPrefix(plain, secretPrefix) {
		return plain, nil
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.gcm.Seal(nil, nonce, []byte(plain), nil)
	combined := append(nonce, sealed...)
	return secretPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// decrypt 还原 "enc:v1:..." 密文。无前缀的值视为历史明文，原样返回。
func (c *secretCodec) decrypt(stored string) (string, error) {
	if !strings.HasPrefix(stored, secretPrefix) {
		return stored, nil // 历史明文，向后兼容
	}
	if !c.enabled() {
		return "", fmt.Errorf("encrypted secret found but no master key configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretPrefix))
	if err != nil {
		return "", fmt.Errorf("invalid encrypted secret encoding: %w", err)
	}
	nonceSize := c.gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", fmt.Errorf("encrypted secret too short")
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plain, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}
	return string(plain), nil
}
