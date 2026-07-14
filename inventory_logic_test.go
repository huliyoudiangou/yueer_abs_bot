package main

import (
	"os"
	"strings"
	"testing"
)

func TestInventoryListHandlesReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `strings.Contains(text, "乾坤袋")`)
	if start < 0 {
		t.Fatal("inventory list command block missing")
	}
	end := strings.Index(text[start:], `strings.Contains(text, "盲盒")`)
	if end < 0 {
		t.Fatal("inventory list command boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"Find(&items).Error",
		"乾坤袋读取失败",
		"乾坤袋暂时读取失败",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("inventory read guard missing %q", want)
		}
	}
	if strings.Contains(block, "DB.Where(\"user_id = ? AND quantity > 0\", userID).Find(&items)\n") ||
		strings.Contains(block, "DB.Where(\"user_id = ? AND quantity > 0\", userID).Find(&items)\r\n") {
		t.Fatal("inventory list still treats read errors as an empty bag")
	}
}
