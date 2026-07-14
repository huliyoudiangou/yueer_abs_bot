package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAbsClientSendRequestDefaultsNilHTTPClient(t *testing.T) {
	var sawAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			t.Fatalf("request path = %q, want /api/ping", r.URL.Path)
		}
		sawAuthorization = r.Header.Get("Authorization") == "Bearer test-token"
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &AbsClient{
		BaseURL: server.URL,
		Token:   "test-token",
	}
	body, code, err := client.sendRequest("GET", "/api/ping", nil)
	if err != nil {
		t.Fatalf("sendRequest err = %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("sendRequest code = %d, want %d", code, http.StatusOK)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("sendRequest body = %q", string(body))
	}
	if !sawAuthorization {
		t.Fatal("sendRequest did not attach bearer token")
	}
	if client.HttpClient != nil {
		t.Fatal("sendRequest should not mutate nil HttpClient fallback into shared client")
	}
}

func TestAbsClientSendRequestUsesProjectUserAgentAndBodyContentType(t *testing.T) {
	var getUserAgent, getContentType string
	var postUserAgent, postContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get":
			getUserAgent = r.Header.Get("User-Agent")
			getContentType = r.Header.Get("Content-Type")
		case "/api/post":
			postUserAgent = r.Header.Get("User-Agent")
			postContentType = r.Header.Get("Content-Type")
		default:
			t.Errorf("unexpected request path: %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &AbsClient{
		BaseURL:    server.URL,
		Token:      "test-token",
		HttpClient: server.Client(),
	}

	if _, code, err := client.sendRequest(http.MethodGet, "/api/get", nil); err != nil || code != http.StatusOK {
		t.Fatalf("GET sendRequest = code %d, err %v", code, err)
	}
	if _, code, err := client.sendRequest(http.MethodPost, "/api/post", []byte(`{}`)); err != nil || code != http.StatusOK {
		t.Fatalf("POST sendRequest = code %d, err %v", code, err)
	}

	if getUserAgent != absClientUserAgent {
		t.Fatalf("GET User-Agent = %q, want %q", getUserAgent, absClientUserAgent)
	}
	if getContentType != "" {
		t.Fatalf("GET Content-Type = %q, want empty", getContentType)
	}
	if postUserAgent != absClientUserAgent {
		t.Fatalf("POST User-Agent = %q, want %q", postUserAgent, absClientUserAgent)
	}
	if postContentType != "application/json" {
		t.Fatalf("POST Content-Type = %q, want application/json", postContentType)
	}
}

func TestAbsUserPathEscapesPathSegment(t *testing.T) {
	got := absUserPath("user/alpha beta?x=1#frag")
	want := "/api/users/user%2Falpha%20beta%3Fx=1%23frag"
	if got != want {
		t.Fatalf("absUserPath() = %q, want %q", got, want)
	}

	statsGot := absUserListeningStatsPath("user/alpha beta?x=1#frag")
	statsWant := want + "/listening-stats"
	if statsGot != statsWant {
		t.Fatalf("absUserListeningStatsPath() = %q, want %q", statsGot, statsWant)
	}

	sessionsGot := absUserListeningSessionsPath("user/alpha beta?x=1#frag")
	sessionsWant := want + "/listening-sessions?itemsPerPage=100&page=0"
	if sessionsGot != sessionsWant {
		t.Fatalf("absUserListeningSessionsPath() = %q, want %q", sessionsGot, sessionsWant)
	}
}

func TestAbsClientUserEndpointsUseEscapedPathHelpers(t *testing.T) {
	source, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		`"/api/users/"+AppConfig.AbsTemplateID`,
		`"/api/users/"+absUserID`,
		`fmt.Sprintf("/api/users/%s", absUserID)`,
		`fmt.Sprintf("/api/users/%s/listening-stats", absUserID)`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("abs client should not build raw ABS user path: %s", unsafe)
		}
	}
	for _, want := range []string{
		`absUserPath(AppConfig.AbsTemplateID)`,
		`absUserPath(absUserID)`,
		`absUserListeningStatsPath(absUserID)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("abs client missing escaped path helper usage %q", want)
		}
	}
}

func TestAbsPasswordUpdateChecksPayloadMarshalError(t *testing.T) {
	source, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func (c *AbsClient) UpdateAbsPassword(")
	if start < 0 {
		t.Fatal("UpdateAbsPassword missing")
	}
	end := strings.Index(text[start:], "func (c *AbsClient) DeleteUser(")
	if end < 0 {
		t.Fatal("UpdateAbsPassword boundary missing")
	}
	block := text[start : start+end]
	for _, unsafe := range []string{
		"reqBody, _ := json.Marshal(cleanPayload)",
		"reqBody, _ = json.Marshal(cleanPayload)",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("UpdateAbsPassword must not ignore payload marshal errors: %q", unsafe)
		}
	}
	for _, want := range []string{
		"reqBody, err := json.Marshal(cleanPayload)",
		`return fmt.Errorf("生成 ABS 密码更新请求失败: %w", err)`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("UpdateAbsPassword missing marshal error guard %q", want)
		}
	}
}

func TestAbsResponseSnippetSanitizesExternalBody(t *testing.T) {
	payload := "{\"message\":\"bad\nrequest\",\"pass\u202eword\":\"abs-pass-123\",\"to\u202eken\":\"abs-token-123\",\"url\":\"https://example.test/cb?to\u202eken=query-token-123\",\"auth\":\"Bearer bearer-token-123\"}"
	got := absResponseSnippet([]byte(payload))
	for _, unsafe := range []string{
		"abs-pass-123",
		"abs-token-123",
		"query-token-123",
		"bearer-token-123",
		"\n",
		"\r",
		"\u202e",
	} {
		if strings.Contains(got, unsafe) {
			t.Fatalf("absResponseSnippet leaked unsafe text %q in %q", unsafe, got)
		}
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("absResponseSnippet should mark redacted fields, got %q", got)
	}

	long := absResponseSnippet([]byte(strings.Repeat("界", 200)))
	if gotRunes := len([]rune(long)); gotRunes > 163 {
		t.Fatalf("absResponseSnippet length = %d runes, want <= 163", gotRunes)
	}
	if !strings.HasSuffix(long, "...") {
		t.Fatalf("absResponseSnippet should append truncation marker, got %q", long)
	}
}

func TestServerStatsStatusReadsAreVisible(t *testing.T) {
	source, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func (c *AbsClient) GetServerStats() string")
	if start < 0 {
		t.Fatal("GetServerStats missing")
	}
	end := strings.Index(text[start:], "func (c *AbsClient) SetUserActiveStatus(")
	if end < 0 {
		t.Fatal("GetServerStats boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`uBody, uCode, uErr := c.sendRequest`,
		`sBody, sCode, sErr := c.sendRequest`,
		"服务器监控 ABS 用户列表读取失败",
		"服务器监控 ABS 会话列表读取失败",
		`userCountText := "读取失败"`,
		`activeSessionCountText := "读取失败"`,
		`pointsUserCountText = "读取失败"`,
		"服务器监控 ABS 用户列表结构异常",
		"服务器监控 ABS 用户列表解析失败",
		"服务器监控 ABS 会话列表结构异常",
		"服务器监控 ABS 会话列表解析失败",
		"strconv.Itoa(len(uList))",
		"strconv.Itoa(len(sessionsList))",
		"strconv.FormatInt(pointsUserCount, 10)",
		"formatSystemConfigTimeForStatus(dailyListeningRefreshLastAtKey)",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastSuccessKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastTotalKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastSkippedKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastErrorKey",
		"积分用户注册总数: `%s`",
		"ABS 用户注册总数: `%s`",
		"活跃听书会话: `%s`",
		"escapeMarkdown(userCountText)",
		"escapeMarkdown(pointsUserCountText)",
		"escapeMarkdown(activeSessionCountText)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("server stats visible read failure guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"uBody, uCode, _ := c.sendRequest",
		"sBody, sCode, _ := c.sendRequest",
		"getSystemConfigString(dailyListeningRefreshLastAtKey)",
		"getSystemConfigString(dailyListeningRefreshLastSuccessKey)",
		"getSystemConfigString(dailyListeningRefreshLastTotalKey)",
		"getSystemConfigString(dailyListeningRefreshLastSkippedKey)",
		"getSystemConfigString(dailyListeningRefreshLastErrorKey)",
		"积分用户注册总数: `%d`",
		"ABS 用户注册总数: `%d`",
		"活跃听书会话: `%d`",
		"userCount, pointsUserCount, activeSessionCount",
		"_ = json.Unmarshal(sBody, &sessionsList)",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("server stats still collapses read failure into default value: %q", unsafe)
		}
	}
}
