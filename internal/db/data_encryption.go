package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	defaultDataEncryptionKey   = "go-bot-arch-4-4-5-default-data-encryption-key"
	sensitiveDataCipherPrefix  = "enc:v1:"
	searchOrdersScanMultiplier = 6
)

type sensitiveDataCipher struct {
	aead cipher.AEAD
}

func newSensitiveDataCipher(secret string) (*sensitiveDataCipher, error) {
	seed := strings.TrimSpace(secret)
	if seed == "" {
		seed = defaultDataEncryptionKey
	}
	key := sha256.Sum256([]byte(seed))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-256 cipher 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM 失败: %w", err)
	}
	return &sensitiveDataCipher{aead: aead}, nil
}

func (c *sensitiveDataCipher) encrypt(raw string) (string, error) {
	if c == nil || raw == "" {
		return raw, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成加密随机数失败: %w", err)
	}
	payload := c.aead.Seal(nil, nonce, []byte(raw), nil)
	token := append(nonce, payload...)
	return sensitiveDataCipherPrefix + base64.StdEncoding.EncodeToString(token), nil
}

func (c *sensitiveDataCipher) decrypt(raw string) (string, error) {
	if c == nil || raw == "" {
		return raw, nil
	}
	if !strings.HasPrefix(raw, sensitiveDataCipherPrefix) {
		return raw, nil
	}
	encoded := strings.TrimPrefix(raw, sensitiveDataCipherPrefix)
	token, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("解析加密字段失败: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(token) <= nonceSize {
		return "", fmt.Errorf("加密字段长度非法")
	}
	nonce := token[:nonceSize]
	ciphertext := token[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密敏感字段失败: %w", err)
	}
	return string(plain), nil
}

func (d *Database) encryptSensitiveFieldValue(raw string) (string, error) {
	if d == nil || d.dataCipher == nil {
		return raw, nil
	}
	return d.dataCipher.encrypt(raw)
}

func (d *Database) decryptSensitiveFieldValue(raw string) (string, error) {
	if d == nil || d.dataCipher == nil {
		return raw, nil
	}
	return d.dataCipher.decrypt(raw)
}

func (d *Database) encryptLedgerSensitiveFields(item LedgerEntryInput) (LedgerEntryInput, error) {
	encrypted := item
	note, err := d.encryptSensitiveFieldValue(item.Note)
	if err != nil {
		return LedgerEntryInput{}, err
	}
	cardNo, err := d.encryptSensitiveFieldValue(item.CardNo)
	if err != nil {
		return LedgerEntryInput{}, err
	}
	encrypted.Note = note
	encrypted.CardNo = cardNo
	return encrypted, nil
}

func (d *Database) decryptLedgerSensitiveFields(item *LedgerEntry) error {
	if d == nil || item == nil {
		return nil
	}
	note, err := d.decryptSensitiveFieldValue(item.Note)
	if err != nil {
		return err
	}
	cardNo, err := d.decryptSensitiveFieldValue(item.CardNo)
	if err != nil {
		return err
	}
	item.Note = note
	item.CardNo = cardNo
	return nil
}

func orderMatchKeyword(item LedgerEntry, keyword string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Note), needle) ||
		strings.Contains(strings.ToLower(item.Username), needle) ||
		strings.Contains(strings.ToLower(item.Nickname), needle)
}
