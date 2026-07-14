package main

import (
	"os"
	"strings"
	"testing"
)

func TestBackupCleanupLogUsesPlainValue(t *testing.T) {
	source, err := os.ReadFile("backup.go")
	if err != nil {
		t.Fatalf("read backup.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func cleanupStalePlainBackups()")
	if start < 0 {
		t.Fatal("cleanupStalePlainBackups missing")
	}
	end := strings.Index(text[start:], "func createEncryptedDBBackup()")
	if end < 0 {
		t.Fatal("cleanupStalePlainBackups boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"plainBackupPath := filepath.Join(backupDir, name)",
		"fullPath := formatPlainValue(plainBackupPath)",
		"os.Remove(plainBackupPath)",
		"log.Printf(",
		"log.Printf(\"🧹 已清理历史明文备份文件: %s\", fullPath)",
		"log.Printf(\"⚠️ 清理历史明文备份文件失败: path=%s err=%s\", fullPath, formatPlainError(err))",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("backup cleanup path diagnostic missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"fullPath := filepath.Join(backupDir, name)",
		"os.Remove(fullPath)",
		"formatPlainError(os.Remove",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("backup cleanup still uses raw path for logging: %q", unsafe)
		}
	}
}
