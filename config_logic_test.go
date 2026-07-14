package main

import (
	"os"
	"strings"
	"testing"
)

func TestEconomicConfigReadsDoNotSilentlyDefaultOnErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func getConfigIntFromDBChecked(",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"invalid integer config",
		"兑换商城价格配置读取失败",
		"兑换邀请码价格配置读取失败",
		"兑换续期卡价格配置读取失败",
		"本次交易未扣除积分",
		"邀请码价格配置暂时读取失败",
		"续期卡价格配置暂时读取失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("economic config read guard missing %q", want)
		}
	}
	if strings.Contains(text, `invPrice := getConfigInt("invite_price", 300)`) ||
		strings.Contains(text, `renPrice := getConfigInt("renew_price", 150)`) {
		t.Fatal("exchange flow still reads economic prices with silent default helper")
	}
	for _, unsafe := range []string{
		"func getConfigIntFromDB(db *gorm.DB, key string, defaultVal int) int",
		"func getConfigInt(key string, defaultVal int) int",
		"使用默认值: key=%s default=%d err=%s",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("economic config unchecked fallback helper still exists: %q", unsafe)
		}
	}
}

func TestSetConfigIntWithAuditUsesCheckedOldValue(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"txOldValue, err := getConfigIntFromDBChecked(tx, key, defaultVal)",
		"oldValue = txOldValue",
		"return 0, err",
		"return oldValue, nil",
		"return err",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("setConfigIntWithAudit old value guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"oldValue = getConfigIntFromDB(tx, key, defaultVal)",
		"oldValue, err = getConfigIntFromDBChecked(tx, key, defaultVal)",
		"return oldValue, err",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("setConfigIntWithAudit still exposes unsafe old value path %q", unsafe)
		}
	}
}

func TestAdminConfigMutationReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		wants     []string
		forbidden []string
	}{
		{
			name:      "integer config",
			startFunc: "func setConfigIntWithAudit(",
			endFunc:   "func setServerLinesWithAudit(",
			wants: []string{
				"txOldValue, err := getConfigIntFromDBChecked(tx, key, defaultVal)",
				"oldValue = txOldValue",
				"return 0, err",
				"return oldValue, nil",
			},
			forbidden: []string{
				"return oldValue, err",
			},
		},
		{
			name:      "server lines",
			startFunc: "func setServerLinesWithAudit(",
			endFunc:   "func generateInviteCodesWithAudit(",
			wants: []string{
				"txOldLen := 0",
				"oldLen = txOldLen",
				"return 0, 0, err",
				"return oldLen, newLen, nil",
			},
			forbidden: []string{
				"return oldLen, newLen, err",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing post-transaction return guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still exposes transactional intermediate value: %s", tt.name, unsafe)
			}
		}
	}
}

func TestAbsAPIURLRequiresValidBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
		http bool
	}{
		{name: "https host", raw: "https://abs.example.com", want: true},
		{name: "https with base path", raw: "https://abs.example.com/audiobookshelf", want: true},
		{name: "http host", raw: "http://127.0.0.1:13378", want: true, http: true},
		{name: "missing host", raw: "https://", want: false},
		{name: "unsupported scheme", raw: "ftp://abs.example.com", want: false},
		{name: "userinfo rejected", raw: "https://user:pass@abs.example.com", want: false},
		{name: "query rejected", raw: "https://abs.example.com?token=bad", want: false},
		{name: "fragment rejected", raw: "https://abs.example.com/#frag", want: false},
		{name: "space rejected", raw: "https://abs.example.com /api", want: false},
		{name: "leading space rejected", raw: " https://abs.example.com", want: false},
		{name: "line separator rejected", raw: "https://abs.example.com/\u2028api", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidAbsAPIURL(tt.raw); got != tt.want {
				t.Fatalf("isValidAbsAPIURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			if got := isAbsAPIHTTPURL(tt.raw); got != tt.http {
				t.Fatalf("isAbsAPIHTTPURL(%q) = %v, want %v", tt.raw, got, tt.http)
			}
		})
	}
}

func TestValidateConfigUsesParsedAbsAPIURL(t *testing.T) {
	source, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("read config.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"AbsURL:                strings.TrimRight(strings.TrimSpace(getEnv(\"ABS_API_URL\", \"\")), \"/\")",
		"if !isValidAbsAPIURL(AppConfig.AbsURL)",
		"if isAbsAPIHTTPURL(AppConfig.AbsURL)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ABS_API_URL parsed validation guard missing %q", want)
		}
	}
	if strings.Contains(text, "strings.HasPrefix(AppConfig.AbsURL") {
		t.Fatal("ABS_API_URL validation still relies on string prefix checks")
	}
}

func TestLocalGardenMiniAppURLRequiresExactLocalHost(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "localhost with port", raw: "http://localhost:8081/garden", want: true},
		{name: "ipv4 loopback", raw: "http://127.0.0.1:8081/garden", want: true},
		{name: "ipv6 loopback", raw: "http://[::1]:8081/garden", want: true},
		{name: "https is not local exception", raw: "https://localhost/garden", want: false},
		{name: "localhost suffix rejected", raw: "http://localhost.evil.com/garden", want: false},
		{name: "ipv4 prefix rejected", raw: "http://127.0.0.1.evil.com/garden", want: false},
		{name: "userinfo rejected by hostname", raw: "http://localhost@evil.com/garden", want: false},
		{name: "invalid", raw: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalGardenMiniAppURL(tt.raw); got != tt.want {
				t.Fatalf("isLocalGardenMiniAppURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
