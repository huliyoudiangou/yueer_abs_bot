package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const backupMagicHeader = "ABSBACKUPv2"

func cleanupStalePlainBackups() {
	backupDir := "data/backups"

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// 只清理明文 .db 文件，不清理 .enc
		if strings.HasSuffix(name, ".db") {
			plainBackupPath := filepath.Join(backupDir, name)
			fullPath := formatPlainValue(plainBackupPath)
			if err := os.Remove(plainBackupPath); err == nil {
				log.Printf("🧹 已清理历史明文备份文件: %s", fullPath)
			} else {
				log.Printf("⚠️ 清理历史明文备份文件失败: path=%s err=%s", fullPath, formatPlainError(err))
			}
		}
	}
}

func createEncryptedDBBackup() (string, func(), error) {
	if AppConfig == nil {
		return "", nil, fmt.Errorf("系统配置尚未初始化，无法创建数据库备份")
	}

	backupKey := strings.TrimSpace(AppConfig.BackupEncryptKey)
	if backupKey == "" {
		return "", nil, fmt.Errorf("未配置 BACKUP_ENCRYPT_KEY，禁止发送明文数据库备份")
	}

	if DB == nil {
		return "", nil, fmt.Errorf("数据库尚未初始化，无法创建备份")
	}

	backupDir := "data/backups"
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", nil, fmt.Errorf("创建备份目录失败: %w", err)
	}

	randomSuffix, err := generateBackupRandomSuffix(8)
	if err != nil {
		return "", nil, fmt.Errorf("生成备份随机文件名失败: %w", err)
	}

	ts := time.Now().Format("20060102_150405")
	plainPath := filepath.Join(backupDir, fmt.Sprintf("bot_data_%s_%s.db", ts, randomSuffix))
	encPath := plainPath + ".enc"

	cleanupPlain := func() {
		_ = os.Remove(plainPath)
	}

	cleanupAll := func() {
		_ = os.Remove(plainPath)
		_ = os.Remove(encPath)
	}

	// SQLite 正确备份方式：用 VACUUM INTO 导出一个一致性的数据库快照。
	// 注意：这里会短暂产生一个明文临时数据库文件，所以后面必须确保删除。
	if err := DB.Exec("VACUUM INTO ?", plainPath).Error; err != nil {
		cleanupPlain()
		return "", nil, fmt.Errorf("导出 SQLite 备份失败: %w", err)
	}
	defer cleanupPlain()

	plainData, err := os.ReadFile(plainPath)
	if err != nil {
		return "", nil, fmt.Errorf("读取临时备份失败: %w", err)
	}
	defer zeroBytes(plainData)

	encryptedData, err := encryptBackupData(plainData, backupKey)
	if err != nil {
		return "", nil, err
	}
	defer zeroBytes(encryptedData)

	// 写出前先用当前密钥做一次解密自校验，避免发送不可恢复的坏备份。
	verifyPlainData, err := decryptBackupData(encryptedData, backupKey)
	if err != nil {
		return "", nil, fmt.Errorf("加密备份自校验失败: %w", err)
	}
	if len(verifyPlainData) != len(plainData) || string(verifyPlainData[:16]) != string(plainData[:16]) {
		zeroBytes(verifyPlainData)
		return "", nil, fmt.Errorf("加密备份自校验失败: 解密内容与原始备份不一致")
	}
	zeroBytes(verifyPlainData)

	if err := os.WriteFile(encPath, encryptedData, 0600); err != nil {
		_ = os.Remove(encPath)
		return "", nil, fmt.Errorf("写入加密备份失败: %w", err)
	}

	// 返回给调用方。
	// 调用方发送 Telegram 文件完成后，应执行 cleanup 删除加密临时文件。
	return encPath, cleanupAll, nil
}

func encryptBackupData(plainData []byte, backupKey string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("生成随机 salt 失败: %w", err)
	}

	key := argon2.IDKey([]byte(backupKey), salt, 3, 64*1024, 4, 32)
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM 失败: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("生成随机 nonce 失败: %w", err)
	}

	cipherText := gcm.Seal(nil, nonce, plainData, nil)

	output := make([]byte, 0, len(backupMagicHeader)+len(salt)+len(nonce)+len(cipherText))
	output = append(output, []byte(backupMagicHeader)...)
	output = append(output, salt...)
	output = append(output, nonce...)
	output = append(output, cipherText...)

	return output, nil
}

func decryptBackupData(encryptedData []byte, backupKey string) ([]byte, error) {
	if len(encryptedData) < len(backupMagicHeader)+16 {
		return nil, fmt.Errorf("备份文件过短或格式错误")
	}

	if string(encryptedData[:len(backupMagicHeader)]) != backupMagicHeader {
		return nil, fmt.Errorf("备份文件头不匹配")
	}

	offset := len(backupMagicHeader)

	salt := encryptedData[offset : offset+16]
	offset += 16

	key := argon2.IDKey([]byte(backupKey), salt, 3, 64*1024, 4, 32)
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM 失败: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(encryptedData) < offset+nonceSize {
		return nil, fmt.Errorf("备份文件 nonce 缺失")
	}

	nonce := encryptedData[offset : offset+nonceSize]
	offset += nonceSize

	cipherText := encryptedData[offset:]

	plainData, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return nil, fmt.Errorf("备份解密失败: %w", err)
	}

	return plainData, nil
}

func generateBackupRandomSuffix(byteLen int) (string, error) {
	if byteLen <= 0 {
		byteLen = 8
	}

	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
