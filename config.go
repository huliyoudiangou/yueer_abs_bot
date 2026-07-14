package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/joho/godotenv"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	TgToken               string
	AdminIDs              map[int64]bool
	AbsURL                string
	AbsKey                string
	AbsTemplateID         string
	NoticeGroupID         int64
	BackupGroupID         int64
	BackupEncryptKey      string
	SecurityPepper        string
	NotifyBeforeDays      int
	InviteRequired        bool
	AccountValidDays      int
	AccountGraceDays      int
	DbURL                 string
	WorkerCount           int
	QueueCapacity         int
	DatabaseMaxOpenConns  int
	DatabaseMaxIdleConns  int
	DatabaseBusyTimeoutMS int
	StartupNotifyAdmins   bool
	GithubAPIToken        string
	GardenMiniAppEnabled  bool
	GardenMiniAppURL      string
	GardenMiniAppListen   string
	// 🗑️ 已彻底删除 ServerLines 属性
}

var AppConfig *Config

func LoadConfig() {
	_ = godotenv.Load()

	AppConfig = &Config{
		TgToken:               getEnv("TELEGRAM_BOT_TOKEN", ""),
		AbsURL:                strings.TrimRight(strings.TrimSpace(getEnv("ABS_API_URL", "")), "/"),
		AbsKey:                getEnv("ABS_API_KEY", ""),
		AbsTemplateID:         getEnv("ABS_TEMPLATE_USER_ID", ""),
		NoticeGroupID:         getEnvAsInt64("NOTICE_GROUP_ID", 0),
		BackupGroupID:         getEnvAsInt64("BACKUP_GROUP_ID", 0),
		BackupEncryptKey:      getEnv("BACKUP_ENCRYPT_KEY", ""),
		SecurityPepper:        getEnv("SECURITY_PEPPER", ""),
		NotifyBeforeDays:      getEnvAsInt("NOTIFY_BEFORE_DAYS", 3),
		InviteRequired:        getEnvAsBool("INVITE_REQUIRED", true),
		AccountValidDays:      getEnvAsInt("ACCOUNT_VALID_DAYS", 30),
		AccountGraceDays:      getEnvAsInt("ACCOUNT_GRACE_DAYS", 7),
		DbURL:                 getEnv("DATABASE_URL", "data/bot_data.db"), // 修正默认路径避免找不到文件
		WorkerCount:           getEnvAsInt("WORKER_COUNT", 30),
		QueueCapacity:         getEnvAsInt("QUEUE_CAPACITY", 10000),
		DatabaseMaxOpenConns:  getEnvAsInt("DATABASE_MAX_OPEN_CONNS", 10),
		DatabaseMaxIdleConns:  getEnvAsInt("DATABASE_MAX_IDLE_CONNS", 10),
		DatabaseBusyTimeoutMS: getEnvAsInt("DATABASE_BUSY_TIMEOUT_MS", 10000),
		StartupNotifyAdmins:   getEnvAsBool("BOT_STARTUP_NOTIFY_ADMINS", false),
		GithubAPIToken:        strings.TrimSpace(getEnv("GITHUB_API_TOKEN", "")),
		GardenMiniAppEnabled:  getEnvAsBool("GARDEN_MINI_APP_ENABLED", false),
		GardenMiniAppURL:      strings.TrimRight(strings.TrimSpace(getEnv("GARDEN_MINI_APP_URL", "")), "/"),
		GardenMiniAppListen:   strings.TrimSpace(getEnv("GARDEN_MINI_APP_LISTEN", ":8081")),
	}

	AppConfig.AdminIDs = make(map[int64]bool)
	adminStr := getEnv("ADMIN_TELEGRAM_IDS", "")
	if adminStr != "" {
		for _, idStr := range strings.Split(adminStr, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64); err == nil {
				AppConfig.AdminIDs[id] = true
			}
		}
	}

	validateConfig()
}

func validateConfig() {
	var missing []string

	if strings.TrimSpace(AppConfig.TgToken) == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if strings.TrimSpace(AppConfig.AbsURL) == "" {
		missing = append(missing, "ABS_API_URL")
	}
	if strings.TrimSpace(AppConfig.AbsKey) == "" {
		missing = append(missing, "ABS_API_KEY")
	}
	if strings.TrimSpace(AppConfig.AbsTemplateID) == "" {
		missing = append(missing, "ABS_TEMPLATE_USER_ID")
	}
	if len(AppConfig.AdminIDs) == 0 {
		missing = append(missing, "ADMIN_TELEGRAM_IDS")
	}
	if strings.TrimSpace(AppConfig.BackupEncryptKey) == "" {
		missing = append(missing, "BACKUP_ENCRYPT_KEY")
	}
	if strings.TrimSpace(AppConfig.SecurityPepper) == "" {
		missing = append(missing, "SECURITY_PEPPER")
	}

	if len(missing) > 0 {
		log.Fatalf("❌ 关键配置缺失，禁止启动。请检查: %s", strings.Join(missing, ", "))
	}

	if !isValidAbsAPIURL(AppConfig.AbsURL) {
		log.Fatalf("❌ ABS_API_URL 格式错误，必须是 http:// 或 https:// 开头的有效基础 URL，且不能包含空白、控制字符、URL 账号密码信息、查询参数或 fragment，当前值: url=%s", formatPlainValue(AppConfig.AbsURL))
	}

	if isAbsAPIHTTPURL(AppConfig.AbsURL) {
		allowInsecureABS := getEnvAsBool("ALLOW_INSECURE_ABS_HTTP", false)
		if !allowInsecureABS {
			log.Fatalf("❌ 生产环境禁止使用明文 HTTP ABS_API_URL，请改为 https://。如仅本地测试，请显式设置 ALLOW_INSECURE_ABS_HTTP=true")
		}
		log.Printf("⚠️ 当前允许使用明文 HTTP ABS_API_URL，仅建议本地测试环境使用")
	}

	if len([]byte(AppConfig.BackupEncryptKey)) < 32 {
		log.Fatalf("❌ BACKUP_ENCRYPT_KEY 太短，建议至少 32 字节，最好 64 字符以上")
	}

	if len([]byte(AppConfig.SecurityPepper)) < 32 {
		log.Fatalf("❌ SECURITY_PEPPER 太短，建议至少 32 字节，最好 64 字符以上")
	}

	if AppConfig.BackupEncryptKey == AppConfig.SecurityPepper {
		log.Printf("🔐 SECURITY_PEPPER 指纹: %s", secretFingerprint(AppConfig.SecurityPepper))
		log.Printf("🔐 BACKUP_ENCRYPT_KEY 指纹: %s", secretFingerprint(AppConfig.BackupEncryptKey))
		log.Fatalf("❌ BACKUP_ENCRYPT_KEY 和 SECURITY_PEPPER 不能相同，请分别设置两个不同密钥")
	}

	if AppConfig.AccountValidDays < 0 {
		log.Fatalf("❌ ACCOUNT_VALID_DAYS 不能为负数")
	}

	if AppConfig.AccountGraceDays < 0 {
		log.Fatalf("❌ ACCOUNT_GRACE_DAYS 不能为负数")
	}

	if AppConfig.NotifyBeforeDays < 0 {
		log.Fatalf("❌ NOTIFY_BEFORE_DAYS 不能为负数")
	}

	if AppConfig.WorkerCount < 1 {
		log.Printf("⚠️ WORKER_COUNT 小于 1，已自动修正为 1")
		AppConfig.WorkerCount = 1
	}
	if AppConfig.WorkerCount > 100 {
		log.Printf("⚠️ WORKER_COUNT 过大，已自动限制为 100")
		AppConfig.WorkerCount = 100
	}

	if AppConfig.QueueCapacity < 100 {
		log.Printf("⚠️ QUEUE_CAPACITY 过小，已自动修正为 100")
		AppConfig.QueueCapacity = 100
	}
	if AppConfig.QueueCapacity > 100000 {
		log.Printf("⚠️ QUEUE_CAPACITY 过大，已自动限制为 100000")
		AppConfig.QueueCapacity = 100000
	}
	if AppConfig.DatabaseMaxOpenConns < 1 {
		log.Printf("⚠️ DATABASE_MAX_OPEN_CONNS 小于 1，已自动修正为 1")
		AppConfig.DatabaseMaxOpenConns = 1
	}
	if AppConfig.DatabaseMaxOpenConns > 20 {
		log.Printf("⚠️ DATABASE_MAX_OPEN_CONNS 过大，SQLite 场景已自动限制为 20")
		AppConfig.DatabaseMaxOpenConns = 20
	}

	if AppConfig.DatabaseMaxIdleConns < 1 {
		log.Printf("⚠️ DATABASE_MAX_IDLE_CONNS 小于 1，已自动修正为 1")
		AppConfig.DatabaseMaxIdleConns = 1
	}
	if AppConfig.DatabaseMaxIdleConns > AppConfig.DatabaseMaxOpenConns {
		log.Printf("⚠️ DATABASE_MAX_IDLE_CONNS 大于 DATABASE_MAX_OPEN_CONNS，已自动修正")
		AppConfig.DatabaseMaxIdleConns = AppConfig.DatabaseMaxOpenConns
	}

	if AppConfig.DatabaseBusyTimeoutMS < 1000 {
		log.Printf("⚠️ DATABASE_BUSY_TIMEOUT_MS 过小，已自动修正为 1000")
		AppConfig.DatabaseBusyTimeoutMS = 1000
	}
	if AppConfig.DatabaseBusyTimeoutMS > 60000 {
		log.Printf("⚠️ DATABASE_BUSY_TIMEOUT_MS 过大，已自动限制为 60000")
		AppConfig.DatabaseBusyTimeoutMS = 60000
	}

	if AppConfig.GardenMiniAppListen == "" {
		AppConfig.GardenMiniAppListen = ":8081"
	}
	if AppConfig.GardenMiniAppEnabled {
		if AppConfig.GardenMiniAppURL == "" {
			log.Fatalf("GARDEN_MINI_APP_ENABLED=true requires GARDEN_MINI_APP_URL")
		}
		if !strings.HasPrefix(AppConfig.GardenMiniAppURL, "https://") && !isLocalGardenMiniAppURL(AppConfig.GardenMiniAppURL) {
			log.Fatalf("GARDEN_MINI_APP_URL must use https:// for Telegram Mini App public access: url=%s", formatPlainValue(AppConfig.GardenMiniAppURL))
		}
	}
}

func isValidAbsAPIURL(rawURL string) bool {
	_, err := parseAbsAPIURL(rawURL)
	return err == nil
}

func isAbsAPIHTTPURL(rawURL string) bool {
	parsed, err := parseAbsAPIURL(rawURL)
	return err == nil && parsed.Scheme == "http"
}

func parseAbsAPIURL(rawURL string) (*url.URL, error) {
	if rawURL == "" || rawURL != strings.TrimSpace(rawURL) || strings.ContainsAny(rawURL, " \r\n\t") {
		return nil, url.InvalidHostError(rawURL)
	}
	for _, r := range rawURL {
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return nil, url.InvalidHostError(rawURL)
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, url.InvalidHostError(rawURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, url.InvalidHostError(rawURL)
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, url.InvalidHostError(rawURL)
	}
	return parsed, nil
}

func isLocalGardenMiniAppURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.Scheme != "http" {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func secretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > 12 {
		return encoded[:12]
	}
	return encoded
}

func getEnv(key, defaultVal string) string {
	if val, exists := os.LookupEnv(key); exists {
		return val
	}
	return defaultVal
}
func getEnvAsInt(key string, defaultVal int) int {
	if val, err := strconv.Atoi(getEnv(key, "")); err == nil {
		return val
	}
	return defaultVal
}
func getEnvAsInt64(key string, defaultVal int64) int64 {
	if val, err := strconv.ParseInt(getEnv(key, ""), 10, 64); err == nil {
		return val
	}
	return defaultVal
}
func getEnvAsBool(key string, defaultVal bool) bool {
	if val, exists := os.LookupEnv(key); exists {
		return strings.ToLower(val) == "true"
	}
	return defaultVal
}
