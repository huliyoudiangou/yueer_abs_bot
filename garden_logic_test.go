package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm/clause"
)

func TestGardenMarketPriceIsStrictlyAboveBaseForPurchasableHerbs(t *testing.T) {
	for _, cfg := range gardenSeeds {
		if !cfg.Purchasable {
			continue
		}
		economy, ok := gardenHerbEconomy[cfg.Key]
		if !ok {
			t.Fatalf("%s missing garden herb economy config", cfg.HerbName)
		}
		base := gardenHerbBaseSellPrice(cfg)
		market := gardenHerbMarketPrice(cfg, "2026-06-16")
		if market <= base {
			t.Fatalf("%s market price = %d, want above base %d", cfg.HerbName, market, base)
		}
		max := economy.MarketPriceMax
		if market > max {
			t.Fatalf("%s market price = %d, want at most max %d", cfg.HerbName, market, max)
		}
	}
}

func TestGardenHerbEconomyPriceTable(t *testing.T) {
	tests := []struct {
		key            string
		basePrice      int
		marketMin      int
		marketMax      int
		marketLimitMin int
		marketLimitMax int
	}{
		{key: "ninglu", basePrice: 4, marketMin: 5, marketMax: 6, marketLimitMin: 15, marketLimitMax: 15},
		{key: "qingling", basePrice: 9, marketMin: 10, marketMax: 11, marketLimitMin: 12, marketLimitMax: 12},
		{key: "chiyang", basePrice: 18, marketMin: 20, marketMax: 22, marketLimitMin: 8, marketLimitMax: 8},
		{key: "yuehua", basePrice: 36, marketMin: 40, marketMax: 43, marketLimitMin: 5, marketLimitMax: 5},
		{key: "xuanshen", basePrice: 54, marketMin: 60, marketMax: 64, marketLimitMin: 3, marketLimitMax: 3},
		{key: "ziyuzhi", basePrice: 126, marketMin: 140, marketMax: 148, marketLimitMin: 1, marketLimitMax: 1},
	}

	for _, tt := range tests {
		cfg, ok := gardenSeedByKey(tt.key)
		if !ok {
			t.Fatalf("missing garden seed %s", tt.key)
		}
		if got := gardenHerbBaseSellPrice(cfg); got != tt.basePrice {
			t.Fatalf("%s base price = %d, want %d", cfg.HerbName, got, tt.basePrice)
		}
		economy, ok := gardenHerbEconomy[tt.key]
		if !ok {
			t.Fatalf("%s missing economy config", cfg.HerbName)
		}
		if economy.MarketPriceMin != tt.marketMin || economy.MarketPriceMax != tt.marketMax {
			t.Fatalf("%s market price range = %d-%d, want %d-%d", cfg.HerbName, economy.MarketPriceMin, economy.MarketPriceMax, tt.marketMin, tt.marketMax)
		}
		if economy.MarketLimitMin != tt.marketLimitMin || economy.MarketLimitMax != tt.marketLimitMax {
			t.Fatalf("%s market limit range = %d-%d, want %d-%d", cfg.HerbName, economy.MarketLimitMin, economy.MarketLimitMax, tt.marketLimitMin, tt.marketLimitMax)
		}
		for _, dayKey := range []string{"2026-06-16", "2026-06-17", "2026-06-18"} {
			market := gardenHerbMarketPrice(cfg, dayKey)
			if market < tt.marketMin || market > tt.marketMax {
				t.Fatalf("%s market price on %s = %d, want within %d-%d", cfg.HerbName, dayKey, market, tt.marketMin, tt.marketMax)
			}
			limit := gardenHerbMarketLimit(cfg, dayKey)
			if limit < tt.marketLimitMin || limit > tt.marketLimitMax {
				t.Fatalf("%s market limit on %s = %d, want within %d-%d", cfg.HerbName, dayKey, limit, tt.marketLimitMin, tt.marketLimitMax)
			}
		}
	}
}

func TestGardenMiniAppJSONResponseHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	writeGardenMiniAppJSON(rr, http.StatusCreated, gardenMiniAppResponse{OK: true})

	resp := rr.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	headers := map[string]string{
		"Content-Type":           "application/json; charset=utf-8",
		"Cache-Control":          "no-store",
		"Pragma":                 "no-cache",
		"Expires":                "0",
		"X-Content-Type-Options": "nosniff",
	}
	for name, want := range headers {
		if got := resp.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestGardenMiniAppRootRedirectUsesNoStoreHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	gardenMiniAppRootHandler(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if got := resp.Header.Get("Location"); got != "/garden" {
		t.Fatalf("Location = %q, want /garden", got)
	}
	headers := map[string]string{
		"Cache-Control":          "no-store",
		"Pragma":                 "no-cache",
		"Expires":                "0",
		"X-Content-Type-Options": "nosniff",
	}
	for name, want := range headers {
		if got := resp.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestGardenMiniAppMethodNotAllowedIsJSON(t *testing.T) {
	tests := []struct {
		name    string
		allowed string
		call    func(*httptest.ResponseRecorder)
	}{
		{
			name:    "root",
			allowed: http.MethodGet,
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/", nil)
				gardenMiniAppRootHandler(rr, req)
			},
		},
		{
			name:    "index",
			allowed: http.MethodGet,
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/garden", nil)
				gardenMiniAppIndexHandler(rr, req)
			},
		},
		{
			name:    "static",
			allowed: http.MethodGet,
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/garden/app.js", nil)
				gardenMiniAppStaticHandler(rr, req)
			},
		},
		{
			name:    "state",
			allowed: http.MethodGet,
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/api/garden/state", nil)
				gardenMiniAppStateHandler(rr, req, &tgbotapi.User{ID: 1})
			},
		},
		{
			name:    "action",
			allowed: http.MethodPost,
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodGet, "/api/garden/open-plot", nil)
				handleGardenMiniAppAction(rr, req, &tgbotapi.User{ID: 1}, func(gardenMiniAppActionRequest) (interface{}, error) {
					t.Fatal("action should not run for unsupported method")
					return nil, nil
				}, "ok")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.call(rr)
			resp := rr.Result()
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
			}
			if got := resp.Header.Get("Allow"); got != tt.allowed {
				t.Fatalf("Allow = %q, want %q", got, tt.allowed)
			}
			if got := resp.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			var payload gardenMiniAppResponse
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response err = %v", err)
			}
			if payload.OK || payload.Code != "METHOD_NOT_ALLOWED" || strings.TrimSpace(payload.Message) == "" {
				t.Fatalf("payload = %+v, want method-not-allowed JSON error", payload)
			}
		})
	}
}

func TestGardenMiniAppNotFoundIsJSON(t *testing.T) {
	tests := []struct {
		name string
		call func(*httptest.ResponseRecorder)
	}{
		{
			name: "root unknown path",
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodGet, "/missing", nil)
				gardenMiniAppRootHandler(rr, req)
			},
		},
		{
			name: "unknown static file",
			call: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodGet, "/garden/missing.js", nil)
				gardenMiniAppStaticHandler(rr, req)
			},
		},
		{
			name: "embedded file missing",
			call: func(rr *httptest.ResponseRecorder) {
				serveGardenMiniAppFile(rr, "web/garden/missing.js", "application/javascript; charset=utf-8")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.call(rr)
			resp := rr.Result()
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
			}
			headers := map[string]string{
				"Content-Type":           "application/json; charset=utf-8",
				"Cache-Control":          "no-store",
				"Pragma":                 "no-cache",
				"Expires":                "0",
				"X-Content-Type-Options": "nosniff",
			}
			for name, want := range headers {
				if got := resp.Header.Get(name); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
			var payload gardenMiniAppResponse
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response err = %v", err)
			}
			if payload.OK || payload.Code != "NOT_FOUND" || strings.TrimSpace(payload.Message) == "" {
				t.Fatalf("payload = %+v, want not-found JSON error", payload)
			}
		})
	}
}

func TestGardenMiniAppServerHasBoundedTimeouts(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func StartGardenMiniAppServer()")
	if start < 0 {
		t.Fatal("StartGardenMiniAppServer missing")
	}
	end := strings.Index(text[start:], "func registerGardenMiniAppRoutes(")
	if end < 0 {
		t.Fatal("StartGardenMiniAppServer boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"ReadHeaderTimeout: gardenMiniAppReadHeaderTimeout",
		"ReadTimeout:       gardenMiniAppReadTimeout",
		"WriteTimeout:      gardenMiniAppWriteTimeout",
		"IdleTimeout:       gardenMiniAppIdleTimeout",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Mini App HTTP server timeout guard missing %q", want)
		}
	}
}

func TestGardenMiniAppInitDataRejectsOversizedHeader(t *testing.T) {
	oldConfig := AppConfig
	AppConfig = &Config{TgToken: strings.Repeat("t", 32)}
	defer func() {
		AppConfig = oldConfig
	}()

	_, err := validateGardenMiniAppInitData(strings.Repeat("a", gardenMiniAppInitDataMaxLen+1))
	if err == nil {
		t.Fatal("oversized init data err = nil, want error")
	}
	if got := gardenMiniAppAuthErrorCode(err); got != "INIT_DATA_TOO_LARGE" {
		t.Fatalf("auth error code = %s, want INIT_DATA_TOO_LARGE", got)
	}
}

func TestGardenMiniAppInitDataLengthGuardRunsBeforeParse(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func validateGardenMiniAppInitData(")
	if start < 0 {
		t.Fatal("validateGardenMiniAppInitData missing")
	}
	end := strings.Index(text[start:], "func gardenMiniAppStateHandler(")
	if end < 0 {
		t.Fatal("validateGardenMiniAppInitData boundary missing")
	}
	block := text[start : start+end]
	lenIdx := strings.Index(block, "len(initData) > gardenMiniAppInitDataMaxLen")
	parseIdx := strings.Index(block, "url.ParseQuery(initData)")
	if lenIdx < 0 {
		t.Fatal("init data length guard missing")
	}
	if parseIdx < 0 {
		t.Fatal("init data parse marker missing")
	}
	if lenIdx > parseIdx {
		t.Fatal("init data length guard must run before URL parsing")
	}
	if !strings.Contains(text, `"INIT_DATA_TOO_LARGE"`) {
		t.Fatal("oversized init data error code should remain visible in auth diagnostics")
	}
}

func TestGardenMiniAppActionRequestDecodeRejectsTrailingJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
		wantNo  int
	}{
		{name: "empty body", body: ""},
		{name: "single object", body: `{"plotNo":2}`, wantNo: 2},
		{name: "single object with whitespace", body: `{"plotNo":3}   `, wantNo: 3},
		{name: "second json value", body: `{"plotNo":2}{}`, wantErr: true},
		{name: "trailing garbage", body: `{"plotNo":2} x`, wantErr: true},
		{name: "unknown field", body: `{"plotNo":2,"unexpected":true}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req gardenMiniAppActionRequest
			err := decodeGardenMiniAppActionRequest(strings.NewReader(tt.body), &req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("decode err = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decode err = %v", err)
			}
			if req.PlotNo != tt.wantNo {
				t.Fatalf("plotNo = %d, want %d", req.PlotNo, tt.wantNo)
			}
		})
	}
}

func TestGardenMiniAppActionRequestDecodeDisallowsUnknownFields(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func decodeGardenMiniAppActionRequest(")
	if start < 0 {
		t.Fatal("decodeGardenMiniAppActionRequest missing")
	}
	end := strings.Index(text[start:], "func buildGardenMiniAppState(")
	if end < 0 {
		t.Fatal("decodeGardenMiniAppActionRequest boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "dec.DisallowUnknownFields()") {
		t.Fatal("Mini App action decoder must reject unknown JSON fields")
	}
}

func TestGardenMiniAppPostBodySizeIsBoundedBeforeActionDecode(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "gardenMiniAppMaxBodyBytes      = 64 * 1024") {
		t.Fatal("Mini App action body size limit constant should remain explicit and bounded")
	}
	start := strings.Index(text, "func gardenMiniAppAuth(")
	if start < 0 {
		t.Fatal("gardenMiniAppAuth missing")
	}
	end := strings.Index(text[start:], "func gardenMiniAppAuthErrorCode(")
	if end < 0 {
		t.Fatal("gardenMiniAppAuth boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"if r.Method == http.MethodPost {",
		"r.Body = http.MaxBytesReader(w, r.Body, gardenMiniAppMaxBodyBytes)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Mini App auth wrapper should bound POST body before action decode, missing %q", want)
		}
	}
}

func TestGardenMiniAppCommittedActionRefreshFailureIsNotRetryableActionFailure(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleGardenMiniAppAction(")
	if start < 0 {
		t.Fatal("handleGardenMiniAppAction missing")
	}
	end := strings.Index(text[start:], "func decodeGardenMiniAppActionRequest(")
	if end < 0 {
		t.Fatal("handleGardenMiniAppAction boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`Code:    "STATE_REFRESH_FAILED"`,
		`Message: "操作已完成，园况刷新失败，正在重新同步"`,
		`writeGardenMiniAppJSON(w, http.StatusOK, gardenMiniAppResponse{`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("handleGardenMiniAppAction refresh failure response missing %q", want)
		}
	}
	if strings.Contains(block, `writeGardenMiniAppError(w, http.StatusInternalServerError, "STATE_FAILED"`) {
		t.Fatal("committed action refresh failure must not return retryable 500 action error")
	}

	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	jsText := string(js)
	for _, want := range []string{
		"function markCommittedActionNeedsSync(",
		"if (!payload.state) {",
		"app.offlineMode = true;",
		"scheduleRetry();",
	} {
		if !strings.Contains(jsText, want) {
			t.Fatalf("garden mini app frontend committed-action sync guard missing %q", want)
		}
	}
}

func TestGardenMiniAppCacheNormalizesAgedSnapshot(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	start := strings.Index(text, "function loadGardenStateCache()")
	if start < 0 {
		t.Fatal("loadGardenStateCache missing")
	}
	end := strings.Index(text[start:], "function normalizeCachedGardenState(")
	if end < 0 {
		t.Fatal("loadGardenStateCache boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "const normalized = normalizeCachedGardenState(cached.state, cached.savedAt);") {
		t.Fatal("cached garden state must be normalized before offline display")
	}
	if strings.Contains(block, "return cached.state;") {
		t.Fatal("cached garden state should not be returned without aging normalization")
	}
	for _, want := range []string{
		"if (!isGardenStatePayload(normalized)) return null;",
		"return normalized;",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("cached garden state validation missing %q", want)
		}
	}
}

func TestGardenMiniAppCacheUsesSafeLocalStorageAccessor(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"function gardenLocalStorage()",
		"return window.localStorage || null;",
		"const storage = gardenLocalStorage();",
		"if (!storage || !isGardenStatePayload(state)) return;",
		"storage.setItem(gardenStateCacheKey",
		"storage.getItem(gardenStateCacheKey)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden cache safe localStorage guard missing %q", want)
		}
	}
	for _, fn := range []struct {
		name string
		end  string
	}{
		{name: "function saveGardenStateCache(", end: "function loadGardenStateCache("},
		{name: "function loadGardenStateCache(", end: "function normalizeCachedGardenState("},
	} {
		start := strings.Index(text, fn.name)
		if start < 0 {
			t.Fatalf("%s missing", fn.name)
		}
		end := strings.Index(text[start:], fn.end)
		if end < 0 {
			t.Fatalf("%s boundary missing", fn.name)
		}
		block := text[start : start+end]
		if strings.Contains(block, "window.localStorage") {
			t.Fatalf("%s should use gardenLocalStorage instead of direct window.localStorage", fn.name)
		}
	}
}

func TestGardenMiniAppAPIRejectsInvalidJSONAndMissingOK(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	start := strings.Index(text, "async function retryWithBackoff(")
	if start < 0 {
		t.Fatal("retryWithBackoff missing")
	}
	end := strings.Index(text[start:], "function wait(")
	if end < 0 {
		t.Fatal("retryWithBackoff boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"try {",
		"payload = await response.json();",
		`const message = "响应格式异常，请稍后再试";`,
		"payload.ok !== true",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden api response validation missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"response.json().catch(() => ({}))",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("garden api must not treat invalid or unacknowledged responses as success: %q", unsafe)
		}
	}
	validateIdx := strings.Index(block, "payload.ok !== true")
	returnIdx := strings.Index(block, "return payload;")
	if validateIdx < 0 || returnIdx < 0 || validateIdx > returnIdx {
		t.Fatal("garden api must validate payload.ok before returning payload")
	}
}

func TestGardenMiniAppStatePayloadIsValidatedBeforeAssignment(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"function requireGardenStatePayload(payload)",
		"function isGardenStatePayload(state)",
		"function isGardenStateCounts(counts)",
		"function isGardenStateNextPlot(nextPlot)",
		"function isGardenStatePlot(plot)",
		"function isGardenPlotStatus(value)",
		"function isGardenStateSeed(seed)",
		"function isGardenStateHerb(herb)",
		"function isGardenStateRecipe(recipe)",
		"function isGardenStateRecipeMaterial(material)",
		"function isGardenStateMarketOffer(offer)",
		`throw new Error("园况数据异常，请稍后再试")`,
		"Array.isArray(state.plots)",
		"Array.isArray(state.seeds)",
		"Array.isArray(state.herbs)",
		"Array.isArray(state.recipes)",
		"Array.isArray(state.market)",
		"isGardenNonNegativeInteger(state.points)",
		"isGardenStateCounts(state.counts)",
		"isGardenStateNextPlot(state.nextPlot)",
		"state.plots.every(isGardenStatePlot)",
		"state.seeds.every(isGardenStateSeed)",
		"state.herbs.every(isGardenStateHerb)",
		"state.recipes.every(isGardenStateRecipe)",
		"state.market.every(isGardenStateMarketOffer)",
		"recipe.materials.every(isGardenStateRecipeMaterial)",
		"isGardenPositivePlotNo(plot.plotNo)",
		"isGardenPlotStatus(plot.status)",
		"isGardenString(seed.key)",
		"isGardenString(herb.key)",
		"isGardenString(recipe.key)",
		"isGardenString(material.itemName)",
		"isGardenString(offer.seedKey)",
		"isGardenBoolean(seed.purchasable)",
		"isGardenBoolean(herb.urgent)",
		"isGardenBoolean(herb.sellable)",
		"isGardenBoolean(recipe.unlocked)",
		"isGardenBoolean(material.enough)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden state payload validation missing %q", want)
		}
	}
	if strings.Contains(text, "app.state = payload.state") {
		t.Fatal("garden state assignments must validate payload.state before storing it")
	}
	if got := strings.Count(text, "app.state = requireGardenStatePayload(payload);"); got != 4 {
		t.Fatalf("validated garden state assignment count = %d, want 4", got)
	}
}

func TestGardenMiniAppPlotStatusValidationIsEnumerated(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	start := strings.Index(text, "function isGardenPlotStatus(")
	if start < 0 {
		t.Fatal("isGardenPlotStatus missing")
	}
	end := strings.Index(text[start:], "function isGardenStatePlot(")
	if end < 0 {
		t.Fatal("isGardenPlotStatus boundary missing")
	}
	statusBlock := text[start : start+end]
	for _, want := range []string{`value === "empty"`, `value === "growing"`, `value === "ready"`} {
		if !strings.Contains(statusBlock, want) {
			t.Fatalf("garden plot status enum missing %q", want)
		}
	}
	plotStart := strings.Index(text, "function isGardenStatePlot(")
	if plotStart < 0 {
		t.Fatal("isGardenStatePlot missing")
	}
	plotEnd := strings.Index(text[plotStart:], "function isGardenStateSeed(")
	if plotEnd < 0 {
		t.Fatal("isGardenStatePlot boundary missing")
	}
	plotBlock := text[plotStart : plotStart+plotEnd]
	if !strings.Contains(plotBlock, "isGardenPlotStatus(plot.status)") {
		t.Fatal("garden plot payload validation should reject unknown status values")
	}
	if strings.Contains(plotBlock, "isGardenString(plot.status)") {
		t.Fatal("garden plot status validation should not accept arbitrary strings")
	}
}

func TestGardenMiniAppStateBooleanValidationIsStrict(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"function isGardenBoolean(value)",
		`return typeof value === "boolean";`,
		"isGardenBoolean(seed.purchasable)",
		"isGardenBoolean(herb.urgent)",
		"isGardenBoolean(herb.sellable)",
		"isGardenBoolean(recipe.unlocked)",
		"isGardenBoolean(material.enough)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden state boolean validation missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"!!seed.purchasable",
		"!!herb.urgent",
		"!!herb.sellable",
		"!!recipe.unlocked",
		"!!material.enough",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("garden state boolean validation should not rely on truthy coercion: %q", unsafe)
		}
	}
}

func TestGardenMiniAppStateNumericValidationUsesIntegerBounds(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"function isGardenNonNegativeInteger(value)",
		"Number.isInteger(value) && value >= 0",
		"function isGardenPositivePlotNo(value)",
		"Number.isInteger(value) && value >= 1 && value <= maxGardenPlots",
		"function isGardenStateCounts(counts)",
		"isGardenNonNegativeInteger(counts.plots)",
		"isGardenNonNegativeInteger(counts.readyPlots)",
		"isGardenNonNegativeInteger(counts.seedInventory)",
		"isGardenNonNegativeInteger(counts.herbInventory)",
		"isGardenNonNegativeInteger(counts.recipeUnlocked)",
		"function isGardenStateNextPlot(nextPlot)",
		"isGardenPositivePlotNo(nextPlot.plotNo)",
		"isGardenNonNegativeInteger(nextPlot.cost)",
		"isGardenPositivePlotNo(plot.plotNo)",
		"isGardenNonNegativeInteger(plot.remainingSeconds)",
		"isGardenNonNegativeInteger(seed.price)",
		"isGardenNonNegativeInteger(seed.growSeconds)",
		"isGardenNonNegativeInteger(seed.dailyLimit)",
		"isGardenNonNegativeInteger(seed.leftToday)",
		"isGardenNonNegativeInteger(seed.inventory)",
		"isGardenNonNegativeInteger(herb.inventory)",
		"isGardenNonNegativeInteger(recipe.alchemyCost)",
		"isGardenNonNegativeInteger(material.need)",
		"isGardenNonNegativeInteger(material.owned)",
		"isGardenNonNegativeInteger(offer.left)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden state numeric validation missing %q", want)
		}
	}
	if strings.Contains(text, "function isGardenFiniteNumber(value)") {
		t.Fatal("garden state validation should not keep broad finite-number helper")
	}
}

func TestGardenMiniAppStateListsEncodeAsArrays(t *testing.T) {
	state := gardenMiniAppState{
		Plots:   make([]gardenMiniAppPlot, 0),
		Seeds:   make([]gardenMiniAppSeed, 0),
		Herbs:   make([]gardenMiniAppHerb, 0),
		Recipes: []gardenMiniAppRecipe{{Materials: make([]gardenMiniAppRecipeMaterial, 0)}},
		Market:  make([]gardenMiniAppOffer, 0),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal garden mini app state: %v", err)
	}
	raw := string(data)
	for _, want := range []string{
		`"plots":[]`,
		`"seeds":[]`,
		`"herbs":[]`,
		`"recipes":[`,
		`"materials":[]`,
		`"market":[]`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("garden mini app state JSON missing array field %q: %s", want, raw)
		}
	}
	for _, unsafe := range []string{
		`"plots":null`,
		`"seeds":null`,
		`"herbs":null`,
		`"recipes":null`,
		`"materials":null`,
		`"market":null`,
	} {
		if strings.Contains(raw, unsafe) {
			t.Fatalf("garden mini app state JSON should not encode null list %q: %s", unsafe, raw)
		}
	}
}

func TestGardenMiniAppFarmLayoutSourceGuards(t *testing.T) {
	indexData, err := os.ReadFile("web/garden/index.html")
	if err != nil {
		t.Fatalf("read garden index err = %v", err)
	}
	indexText := string(indexData)
	for _, want := range []string{
		`class="game-header"`,
		`id="points"`,
		`id="plotCount"`,
		`id="readyCount"`,
		`id="gardenPulse"`,
		`id="refreshBtn"`,
	} {
		if !strings.Contains(indexText, want) {
			t.Fatalf("garden resource header missing %q", want)
		}
	}

	cssData, err := os.ReadFile("web/garden/styles.css")
	if err != nil {
		t.Fatalf("read garden styles err = %v", err)
	}
	cssText := string(cssData)
	for _, want := range []string{
		"grid-template-columns: repeat(2, minmax(0, 1fr));",
		"grid-template-columns: repeat(5, minmax(0, 1fr));",
		"width: min(100%, 760px);",
		"@media (max-width: 359px)",
		"@media (prefers-reduced-motion: reduce)",
	} {
		if !strings.Contains(cssText, want) {
			t.Fatalf("garden responsive layout guard missing %q", want)
		}
	}
	if strings.Contains(cssText, ".empty,\n.shelf-empty") {
		t.Fatal("generic empty state selector must not make empty farm plots span the grid")
	}

	jsData, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read garden app err = %v", err)
	}
	jsText := string(jsData)
	for _, want := range []string{
		`const localMockEnabled = isLocalDevHost() && new URLSearchParams(window.location.search).get("mock") === "1";`,
		`data-action="use-seed"`,
		`data-action="open-market"`,
		`renderFarmGuide(activeSeed, readyCount, emptyCount)`,
		`renderFarmActionDock(activeSeed, readyCount, emptyCount)`,
		`class="yard-hut"`,
		`class="yard-spring"`,
		`class="yard-foreground"`,
		`class="growing-crop-logo stage-mark-${stage}"`,
		`itemLogo("herb", plot.seedKey || plot.herbName`,
	} {
		if !strings.Contains(jsText, want) {
			t.Fatalf("garden interaction shortcut missing %q", want)
		}
	}
	for _, want := range []string{
		`herbName: "凝露草"`,
		`herbName: "青灵叶"`,
		`herbName: "赤阳花"`,
		`herbName: "月华藤"`,
		`herbName: "玄参根"`,
		`herbName: "紫玉芝"`,
		`herbName: "龙血果"`,
		`herbName: "天心莲"`,
		`name: "聚灵丹方"`,
		`name: "筑基丹方"`,
		`name: "降尘丹方"`,
		`name: "九转造化丹方"`,
		`name: "九曲灵参丹方"`,
		`name: "补天丹方"`,
	} {
		if !strings.Contains(jsText, want) {
			t.Fatalf("garden local mock must use production catalog name %q", want)
		}
	}
	for _, stale := range []string{"紫纹灵芝", "青元草", "玄霜花"} {
		if strings.Contains(jsText, stale) {
			t.Fatalf("garden local mock still contains non-production herb %q", stale)
		}
	}
	for _, want := range []string{
		"@keyframes crop-sway",
		"@keyframes aura-rise",
		"@keyframes harvest-pop",
		"@keyframes furnace-fire",
		"@keyframes furnace-smoke",
		"@keyframes selected-item-float",
		"@keyframes suspended-pill",
		"@keyframes pill-orbit",
		"@keyframes diagram-awaken",
	} {
		if !strings.Contains(cssText, want) {
			t.Fatalf("garden scene animation guard missing %q", want)
		}
	}
	for _, want := range []string{
		`class="furnace-vessel"`,
		`class="furnace-rune-ring"`,
		`class="furnace-orbit`,
		`function gardenPillPalette(`,
		`function recipeDiagramSVG(`,
		`function furnaceMarkSVG(`,
	} {
		if !strings.Contains(jsText, want) {
			t.Fatalf("garden illustrated alchemy guard missing %q", want)
		}
	}
	pillStart := strings.Index(jsText, "function pillLogoSVG(")
	pillEnd := strings.Index(jsText, "function pillMotifSVG(")
	if pillStart < 0 || pillEnd <= pillStart {
		t.Fatal("garden pill illustration function boundary missing")
	}
	if strings.Contains(jsText[pillStart:pillEnd], "<text") {
		t.Fatal("garden pill illustration must not fall back to a text glyph")
	}
}

func TestGardenMiniAppMarketGuideEscapesMatchedHerbName(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	start := strings.Index(text, "function renderMarket()")
	if start < 0 {
		t.Fatal("renderMarket missing")
	}
	end := strings.Index(text[start:], "function renderWarehouseKeeper(")
	if end < 0 {
		t.Fatal("renderMarket boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "escapeHtml(matched.herbName)") {
		t.Fatal("market guide should escape matched herb name before HTML rendering")
	}
	if strings.Contains(block, "`${matched.herbName} 可走急收`") {
		t.Fatal("market guide still renders raw matched herb name")
	}
}

func TestGardenMiniAppFarmMapSelectedTextIsEscaped(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	start := strings.Index(text, "function renderFarmMap(")
	if start < 0 {
		t.Fatal("renderFarmMap missing")
	}
	end := strings.Index(text[start:], "function renderYardToolBadge(")
	if end < 0 {
		t.Fatal("renderFarmMap boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "escapeHtml(next ?") || !strings.Contains(block, ": selectedText)") {
		t.Fatal("farm map selected text should be escaped before HTML rendering")
	}
	if strings.Contains(block, " : selectedText}</span>") {
		t.Fatal("farm map still renders raw selected text")
	}
}

func TestGardenMiniAppSeedTimingTextsAreEscaped(t *testing.T) {
	js, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"escapeHtml(seed.growText)",
		"escapeHtml(seed.yieldText)",
		"escapeHtml(activeSeed.growText)",
		"escapeHtml(activeSeed.yieldText)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden seed timing text escape missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"${seed.growText}",
		"${seed.yieldText}",
		"${activeSeed.growText}",
		"${activeSeed.yieldText}",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("garden seed timing text should not enter HTML unescaped: %q", unsafe)
		}
	}
}

func TestGardenMiniAppBuildStateInitializesListFields(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func buildGardenMiniAppState(")
	if start < 0 {
		t.Fatal("buildGardenMiniAppState missing")
	}
	end := strings.Index(text[start:], "func gardenMiniAppSeedPurchases(")
	if end < 0 {
		t.Fatal("buildGardenMiniAppState boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"Plots:      make([]gardenMiniAppPlot, 0)",
		"Seeds:      make([]gardenMiniAppSeed, 0, len(gardenSeeds))",
		"Herbs:      make([]gardenMiniAppHerb, 0, len(gardenSeeds))",
		"Recipes:    make([]gardenMiniAppRecipe, 0, len(gardenRecipes))",
		"Market:     make([]gardenMiniAppOffer, 0)",
		"Materials:        make([]gardenMiniAppRecipeMaterial, 0, len(cfg.Materials))",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden mini app state list initialization missing %q", want)
		}
	}
}

func TestGardenBaseSellPriceDoesNotCreateExpectedProfit(t *testing.T) {
	for _, cfg := range gardenSeeds {
		if !cfg.Purchasable {
			continue
		}
		base := gardenHerbBaseSellPrice(cfg)
		expectedReturnTimesTwo := base * (cfg.YieldMin + cfg.YieldMax)
		costTimesTwo := cfg.Price * 2
		if expectedReturnTimesTwo > costTimesTwo {
			t.Fatalf("%s expected base return x2 = %d, want at most seed cost x2 %d", cfg.HerbName, expectedReturnTimesTwo, costTimesTwo)
		}
	}
}

func TestGardenSellHerbValidatesSeedBeforePricing(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func gardenSellHerbQuantity(")
	if start < 0 {
		t.Fatal("gardenSellHerbQuantity missing")
	}
	end := strings.Index(text[start:], "func gardenBuyRecipe(")
	if end < 0 {
		t.Fatal("gardenSellHerbQuantity boundary missing")
	}
	block := text[start : start+end]
	lookupIdx := strings.Index(block, "cfg, ok := gardenSeedByKey(seedKey)")
	okIdx := strings.Index(block, "if !ok {")
	priceIdx := strings.Index(block, "basePrice := gardenHerbBaseSellPrice(cfg)")
	if lookupIdx < 0 || okIdx < 0 || priceIdx < 0 {
		t.Fatal("gardenSellHerbQuantity seed validation markers missing")
	}
	if !(lookupIdx < okIdx && okIdx < priceIdx) {
		t.Fatal("gardenSellHerbQuantity should reject unknown seed before calculating sell price")
	}
	if strings.Contains(block, "if !ok || basePrice <= 0") {
		t.Fatal("gardenSellHerbQuantity should not calculate basePrice before checking seed existence")
	}
}

func TestGardenSellHerbRequiresPositiveCustomQuantity(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func gardenSellHerbQuantity(")
	if start < 0 {
		t.Fatal("gardenSellHerbQuantity missing")
	}
	end := strings.Index(text[start:], "func gardenBuyRecipe(")
	if end < 0 {
		t.Fatal("gardenSellHerbQuantity boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"if requestedQty <= 0",
		"return 0, 0, errGardenHerbQuantityInvalid",
		"qty := requestedQty",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("gardenSellHerbQuantity custom quantity guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"requestedQty == -1",
		"requestedQty < -1",
		"quantity: -1",
		"sellall",
		"sell-all",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("garden custom quantity flow should not contain %q", forbidden)
		}
	}

	appSource, err := os.ReadFile("web/garden/app.js")
	if err != nil {
		t.Fatalf("read web/garden/app.js err = %v", err)
	}
	appText := string(appSource)
	for _, want := range []string{
		`data-action="sell-custom"`,
		"readMarketSellQuantity(dataset.seed)",
		"quantity },",
	} {
		if !strings.Contains(appText, want) {
			t.Fatalf("mini app custom sell flow missing %q", want)
		}
	}
	for _, forbidden := range []string{`"sell-all"`, "quantity: -1", "整箱回收"} {
		if strings.Contains(appText, forbidden) {
			t.Fatalf("mini app should not expose all-sell flow %q", forbidden)
		}
	}
}

func TestGardenZiyuzhiOnlyProfitsOnUrgentMarket(t *testing.T) {
	cfg, ok := gardenSeedByKey("ziyuzhi")
	if !ok {
		t.Fatal("missing ziyuzhi seed")
	}
	base := gardenHerbBaseSellPrice(cfg)
	if base > cfg.Price {
		t.Fatalf("紫玉芝 base price = %d, want at most seed cost %d", base, cfg.Price)
	}
	market := gardenHerbMarketPrice(cfg, "2026-06-16")
	if market < 140 || market > 148 {
		t.Fatalf("紫玉芝 market price = %d, want within 140-148", market)
	}
	if limit := gardenHerbMarketLimit(cfg, "2026-06-16"); limit != 1 {
		t.Fatalf("紫玉芝 market limit = %d, want 1", limit)
	}
}

func TestGardenHerbMarketDayKeyRefreshesAtBeijingTwentyTwo(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "before 22 keeps previous market day",
			t:    time.Date(2026, 6, 17, 21, 59, 59, 0, loc),
			want: "2026-06-16",
		},
		{
			name: "at 22 starts new market day",
			t:    time.Date(2026, 6, 17, 22, 0, 0, 0, loc),
			want: "2026-06-17",
		},
		{
			name: "utc input still uses beijing boundary",
			t:    time.Date(2026, 6, 17, 13, 59, 59, 0, time.UTC),
			want: "2026-06-16",
		},
		{
			name: "utc input at beijing 22 starts new market day",
			t:    time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC),
			want: "2026-06-17",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gardenHerbMarketDayKey(tt.t); got != tt.want {
				t.Fatalf("gardenHerbMarketDayKey() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGardenJulingRecipeCostCloserToShopPrice(t *testing.T) {
	recipe, ok := gardenRecipeByKey("juling")
	if !ok {
		t.Fatal("missing juling recipe")
	}
	materials := map[string]int{}
	for _, material := range recipe.Materials {
		materials[material.ItemName] = material.Quantity
	}
	if materials["凝露草"] != 8 || materials["青灵叶"] != 5 {
		t.Fatalf("juling materials = %#v, want 凝露草 x8 and 青灵叶 x5", materials)
	}
}

func TestGardenPlotCallbacksCheckParseErrors(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "plantlist":`)
	if start < 0 {
		t.Fatal("garden plantlist callback missing")
	}
	end := strings.Index(text[start:], `case "harvestall":`)
	if end < 0 {
		t.Fatal("garden plot callback boundary missing")
	}
	block := text[start : start+end]
	if strings.Count(block, `plotNo, err := strconv.Atoi(parts[2])`) != 3 {
		t.Fatalf("garden plot callbacks should parse plotNo with checked errors: %s", block)
	}
	if strings.Count(block, `if err != nil || plotNo <= 0`) != 3 {
		t.Fatalf("garden plot callbacks should reject invalid plotNo: %s", block)
	}
	if strings.Contains(block, `plotNo, _ := strconv.Atoi(parts[2])`) {
		t.Fatal("garden plot callbacks still ignore plotNo parse errors")
	}
}

func TestGardenJiuzhuanRecipeCostCloserToShopPrice(t *testing.T) {
	recipe, ok := gardenRecipeByKey("jiuzhuan")
	if !ok {
		t.Fatal("missing jiuzhuan recipe")
	}
	materials := map[string]int{}
	for _, material := range recipe.Materials {
		materials[material.ItemName] = material.Quantity
	}
	if recipe.AlchemyCost != 30 {
		t.Fatalf("jiuzhuan alchemy cost = %d, want 30", recipe.AlchemyCost)
	}
	if materials["青灵叶"] != 8 || materials["玄参根"] != 3 || materials["紫玉芝"] != 1 {
		t.Fatalf("jiuzhuan materials = %#v, want 青灵叶 x8, 玄参根 x3 and 紫玉芝 x1", materials)
	}
}

func TestPillEffectSummaryCoversCultivationPills(t *testing.T) {
	tests := []string{"聚灵丹", "九转造化丹", "筑基丹", "降尘丹", "九曲灵参丹", "补天丹"}
	for _, name := range tests {
		if got := pillEffectSummary(name); got == "" {
			t.Fatalf("pillEffectSummary(%s) is empty", name)
		}
	}
	if got := pillEffectSummary("凝露草"); got != "" {
		t.Fatalf("non-pill effect summary = %q", got)
	}
}

func TestTreasureShopItemMarkdownTextIncludesPillEffect(t *testing.T) {
	text := treasureShopItemMarkdownText(treasureShopItem{ID: "1", Name: "聚灵丹", Price: 120})
	if !strings.Contains(text, "功效：") || !strings.Contains(text, "本周最多 3 颗") {
		t.Fatalf("treasure shop item text missing pill effect: %s", text)
	}
}

func TestTreasureShopItemButtonLabelIncludesEffectBadge(t *testing.T) {
	if got := treasureShopItemButtonLabel(treasureShopItem{Name: "聚灵丹", Price: 120}); got != "聚灵丹（120｜修为+1.0h）" {
		t.Fatalf("treasure shop item button label = %q", got)
	}
	if got := treasureShopItemButtonLabel(treasureShopItem{Name: "补天丹", Price: 1000}); got != "补天丹（1000｜自动破境）" {
		t.Fatalf("treasure shop item button label = %q", got)
	}
	if got := treasureShopItemButtonLabel(treasureShopItem{Name: "普通物品", Price: 12}); got != "普通物品（12）" {
		t.Fatalf("treasure shop item button label fallback = %q", got)
	}
}

func TestTreasureShopBuyConfirmMarkdownTextIncludesPillEffect(t *testing.T) {
	text := treasureShopBuyConfirmMarkdownText(treasureShopItem{ID: "2", Name: "九转造化丹", Price: 350})
	if !strings.Contains(text, "功效：") || !strings.Contains(text, "增加 3.0 小时丹药修为") {
		t.Fatalf("treasure shop buy confirm text missing pill effect: %s", text)
	}
}

func TestTreasureShopHomeTextMentionsPillEffect(t *testing.T) {
	text := treasureShopHomeMarkdownText(88, true, 2, true)
	if !strings.Contains(text, "丹药功效速览") || !strings.Contains(text, "聚灵丹") || !strings.Contains(text, "增加 1.0 小时丹药修为") {
		t.Fatalf("treasure shop home text missing pill effect overview: %s", text)
	}
	if !strings.Contains(text, "补天丹") || !strings.Contains(text, "化神突破专用丹药") {
		t.Fatalf("treasure shop home text missing breakthrough pill effect overview: %s", text)
	}
	if !strings.Contains(text, "乾坤袋物品：`2` 种") {
		t.Fatalf("treasure shop home text missing item count: %s", text)
	}
	if got := treasureShopHomeMarkdownText(88, true, 0, false); !strings.Contains(got, "乾坤袋物品：`读取失败` 种") {
		t.Fatalf("treasure shop home text should show unavailable item count: %s", got)
	}
	if got := treasureShopHomeMarkdownText(0, false, 2, true); !strings.Contains(got, "可用积分：`读取失败`") {
		t.Fatalf("treasure shop home text should show unavailable points: %s", got)
	}
}

func TestGardenShopWalletDisplaysReadFailure(t *testing.T) {
	for _, file := range []string{"garden.go", "menu_callbacks.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s err = %v", file, err)
		}
		text := string(source)
		if !strings.Contains(text, "pointsAvailable := true") {
			t.Fatalf("%s missing wallet availability flag", file)
		}
		if !strings.Contains(text, "gardenCountText(int64(points), pointsAvailable)") {
			t.Fatalf("%s should use read-failure aware points text", file)
		}
	}
}

func TestGardenDiagnosticsUseSanitizedErrors(t *testing.T) {
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	for _, file := range []string{"garden.go", "menu_callbacks.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s err = %v", file, err)
		}
		text := string(source)
		if !strings.Contains(text, "formatPlainError(err)") {
			t.Fatalf("%s diagnostics should use formatPlainError", file)
		}
		if strings.Contains(text, rawErrFormat) {
			t.Fatalf("%s diagnostics should not log raw error values", file)
		}
	}
}

func TestGardenMiniAppAuthFailureLogIsSanitized(t *testing.T) {
	source, err := os.ReadFile("garden_miniapp.go")
	if err != nil {
		t.Fatalf("read garden_miniapp.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func gardenMiniAppAuth(")
	if start < 0 {
		t.Fatal("gardenMiniAppAuth missing")
	}
	end := strings.Index(text[start:], "func gardenMiniAppAuthErrorCode(")
	if end < 0 {
		t.Fatal("gardenMiniAppAuth boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"Garden Mini App auth failed: code=%s method=%s path=%s has_init_data=%t ua=%s",
		"gardenMiniAppAuthErrorCode(err)",
		"formatPlainValue(r.Method)",
		"formatPlainValue(r.URL.Path)",
		"strings.TrimSpace(r.Header.Get(\"X-Telegram-Init-Data\")) != \"\"",
		"formatPlainValue(r.UserAgent())",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden mini app auth diagnostic missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"formatPlainValue(r.Header.Get(\"X-Telegram-Init-Data\"))",
		"r.Header.Get(\"X-Telegram-Init-Data\"),",
		"r.UserAgent(),",
		"r.URL.Path,",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("garden mini app auth diagnostic may log raw request data: %q", unsafe)
		}
	}
}

func TestGardenGrantInventoryChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func gardenGrantInventoryInTx(")
	if start < 0 {
		t.Fatal("gardenGrantInventoryInTx missing")
	}
	end := strings.Index(text[start:], "func inventoryQuantityUpsertClause(")
	if end < 0 {
		t.Fatal("gardenGrantInventoryInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Clauses(inventoryQuantityUpsertClause(quantity)).Create(&Inventory{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"INVENTORY_GRANT_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden inventory grant guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("garden inventory grant still checks only create error")
	}
}

func TestInventoryQuantityUpsertTargetsPartialUniqueIndex(t *testing.T) {
	onConflict := inventoryQuantityUpsertClause(3)
	if len(onConflict.Columns) != 2 ||
		onConflict.Columns[0].Name != "user_id" ||
		onConflict.Columns[1].Name != "item_name" {
		t.Fatalf("inventory upsert columns = %#v", onConflict.Columns)
	}
	if len(onConflict.TargetWhere.Exprs) != 1 {
		t.Fatalf("inventory upsert target where = %#v", onConflict.TargetWhere.Exprs)
	}
	eq, ok := onConflict.TargetWhere.Exprs[0].(clause.Eq)
	if !ok {
		t.Fatalf("inventory upsert target where should use clause.Eq, got %#v", onConflict.TargetWhere.Exprs[0])
	}
	col, ok := eq.Column.(clause.Column)
	if !ok || col.Name != "deleted_at" || eq.Value != nil {
		t.Fatalf("inventory upsert target where should match deleted_at IS NULL, got %#v", eq)
	}
}

func TestInventoryMigrationCreatesPartialUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("inventories(user_id, item_name)"`)
	if start < 0 {
		t.Fatal("inventory migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("red_packet_grabs(packet_id, user_id)"`)
	if end < 0 {
		t.Fatal("inventory migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"WHERE deleted_at IS NULL",
		"ensureInventoryPartialUniqueIndex(DB)",
		"inventory unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("inventory migration missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureInventoryPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureInventoryPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureDiceDailyProfitPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("ensureInventoryPartialUniqueIndex boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"FROM sqlite_master",
		"idx_inventory_user_item_unique",
		"DROP INDEX IF EXISTS idx_inventory_user_item_unique",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_user_item_unique",
		"ON inventories(user_id, item_name)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("inventory partial index helper missing %q", want)
		}
	}
}

func TestGardenLimitMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("garden_seed_purchases(user_id, seed_key, day_key)"`)
	if start < 0 {
		t.Fatal("garden seed purchase migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_cave_retreats(active user)"`)
	if end < 0 {
		t.Fatal("garden migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM garden_seed_purchases",
		"FROM garden_herb_market_sales",
		"FROM garden_recipe_unlocks",
		"WHERE deleted_at IS NULL",
		"ensureGardenSeedPurchasePartialUniqueIndex(DB)",
		"ensureGardenHerbMarketSalePartialUniqueIndex(DB)",
		"ensureGardenRecipeUnlockPartialUniqueIndex(DB)",
		"garden seed purchase unique index migration failed; startup blocked",
		"garden herb market sale unique index migration failed; startup blocked",
		"garden recipe unlock unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSoftDeletePartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSoftDeletePartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("garden partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"FROM sqlite_master",
		"sqliteIndexDefinitionsEqual(existing.SQL, createSQL)",
		"DROP INDEX IF EXISTS %s",
		"idx_garden_seed_purchases_unique",
		"ON garden_seed_purchases(user_id, seed_key, day_key)",
		"idx_garden_herb_market_sales_unique",
		"ON garden_herb_market_sales(user_id, seed_key, day_key)",
		"idx_garden_recipe_unlocks_unique",
		"ON garden_recipe_unlocks(user_id, recipe_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("garden partial index helper missing %q", want)
		}
	}
}

func TestGardenPlotMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("garden_plots(user_id, plot_no)"`)
	if start < 0 {
		t.Fatal("garden plot migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("garden_seed_purchases(user_id, seed_key, day_key)"`)
	if end < 0 {
		t.Fatal("garden plot migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM garden_plots",
		"FROM garden_plantings",
		"WHERE deleted_at IS NULL",
		"WHERE deleted_at IS NULL AND status = 'growing'",
		"ensureGardenPlotPartialUniqueIndexes(DB)",
		"garden plot unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden plot migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureGardenPlotPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureGardenPlotPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("garden plot partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_garden_plots_user_plot_unique",
		"ON garden_plots(user_id, plot_no)",
		"idx_garden_plantings_active_plot_unique",
		"ON garden_plantings(plot_id)",
		"WHERE deleted_at IS NULL",
		"WHERE deleted_at IS NULL AND status = 'growing'",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("garden plot partial index helper missing %q", want)
		}
	}
}

func TestGardenInitialPlotCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func createGardenInitialPlotIfMissing(")
	if start < 0 {
		t.Fatal("createGardenInitialPlotIfMissing missing")
	}
	end := strings.Index(text[start:], "func createGardenPlotInTx(")
	if end < 0 {
		t.Fatal("createGardenInitialPlotIfMissing boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"GARDEN_INITIAL_PLOT_INVALID",
		"res := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&plot)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"return nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("garden initial plot helper guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("garden initial plot helper still checks only create error")
	}

	accessStart := strings.Index(text, "func ensureGardenAccessible(")
	if accessStart < 0 {
		t.Fatal("ensureGardenAccessible missing")
	}
	accessEnd := strings.Index(text[accessStart:], "func renderGardenHome(")
	if accessEnd < 0 {
		t.Fatal("ensureGardenAccessible boundary missing")
	}
	accessBlock := text[accessStart : accessStart+accessEnd]
	if !strings.Contains(accessBlock, "createGardenInitialPlotIfMissing(userID)") {
		t.Fatal("ensureGardenAccessible should create initial plot through helper")
	}
	if strings.Contains(accessBlock, "Create(&GardenPlot{") {
		t.Fatal("ensureGardenAccessible still creates initial plot without RowsAffected guard")
	}
}

func TestGardenAssetCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)

	helperBounds := []struct {
		name string
		next string
		want []string
	}{
		{
			name: "createGardenPlotInTx",
			next: "func createGardenPlantingInTx(",
			want: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errGardenPlotMax",
				"res.RowsAffected == 0",
				"GARDEN_PLOT_CREATE_MISSED",
			},
		},
		{
			name: "createGardenPlantingInTx",
			next: "func createGardenRecipeUnlockInTx(",
			want: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errGardenPlotBusy",
				"res.RowsAffected == 0",
				"GARDEN_PLANTING_CREATE_MISSED",
			},
		},
		{
			name: "createGardenRecipeUnlockInTx",
			next: "func createGardenSeedPurchaseIfMissingInTx(",
			want: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errGardenRecipeUnlocked",
				"res.RowsAffected == 0",
				"GARDEN_RECIPE_UNLOCK_CREATE_MISSED",
			},
		},
	}
	for _, tc := range helperBounds {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing guard %q", tc.name, want)
			}
		}
	}

	pathChecks := []struct {
		name   string
		next   string
		want   string
		unsafe string
	}{
		{
			name:   "gardenOpenNextPlot",
			next:   "func gardenBuySeed(",
			want:   "createGardenPlotInTx(tx, &plot)",
			unsafe: "tx.Create(&GardenPlot{",
		},
		{
			name:   "gardenPlantSeed",
			next:   "func gardenPlantAllSeeds(",
			want:   "createGardenPlantingInTx(tx, &planting)",
			unsafe: "tx.Create(&GardenPlanting{",
		},
		{
			name:   "gardenPlantAllSeeds",
			next:   "func gardenHarvestPlot(",
			want:   "createGardenPlantingInTx(tx, &planting)",
			unsafe: "tx.Create(&GardenPlanting{",
		},
		{
			name:   "gardenBuyRecipe",
			next:   "func gardenAlchemy(",
			want:   "createGardenRecipeUnlockInTx(tx, &unlock)",
			unsafe: "tx.Create(&GardenRecipeUnlock{",
		},
	}
	for _, tc := range pathChecks {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		if !strings.Contains(block, tc.want) {
			t.Fatalf("%s missing helper call %q", tc.name, tc.want)
		}
		if strings.Contains(block, tc.unsafe) {
			t.Fatalf("%s still creates asset record without RowsAffected guard", tc.name)
		}
	}
}

func TestGardenLimitRecordsCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)

	helperBounds := []struct {
		name string
		next string
		want []string
	}{
		{
			name: "createGardenSeedPurchaseIfMissingInTx",
			next: "func createGardenHerbMarketSaleIfMissingInTx(",
			want: []string{
				"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"return nil",
				"GARDEN_SEED_PURCHASE_INVALID",
			},
		},
		{
			name: "createGardenHerbMarketSaleIfMissingInTx",
			next: "func gardenOpenNextPlot(",
			want: []string{
				"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"return nil",
				"GARDEN_HERB_MARKET_SALE_INVALID",
			},
		},
	}
	for _, tc := range helperBounds {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing guard %q", tc.name, want)
			}
		}
		if strings.Contains(block, "}).Error") {
			t.Fatalf("%s still checks only create error", tc.name)
		}
	}

	pathChecks := []struct {
		name   string
		next   string
		want   string
		unsafe string
	}{
		{
			name:   "gardenBuySeed",
			next:   "func gardenPlantSeed(",
			want:   "createGardenSeedPurchaseIfMissingInTx(tx, &purchase)",
			unsafe: "Create(&GardenSeedPurchase{",
		},
		{
			name:   "gardenSellHerbQuantity",
			next:   "func gardenBuyRecipe(",
			want:   "createGardenHerbMarketSaleIfMissingInTx(tx, &marketSale)",
			unsafe: "Create(&GardenHerbMarketSale{",
		},
	}
	for _, tc := range pathChecks {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		if !strings.Contains(block, tc.want) {
			t.Fatalf("%s missing helper call %q", tc.name, tc.want)
		}
		if strings.Contains(block, tc.unsafe) {
			t.Fatalf("%s still creates limit record without RowsAffected guard", tc.name)
		}
	}
}

func TestGardenTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
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
			name:      "plant all",
			startFunc: "func gardenPlantAllSeeds(",
			endFunc:   "func gardenHarvestPlot(",
			wants: []string{
				"txPlanted := 0",
				"planted = txPlanted",
				"if err != nil {\n\t\treturn 0, err\n\t}",
				"return planted, nil",
			},
			forbidden: []string{"return planted, err"},
		},
		{
			name:      "harvest all",
			startFunc: "func gardenHarvestAll(",
			endFunc:   "func gardenSellHerb(",
			wants: []string{
				"txHarvestedPlots := 0",
				"txHarvestedQty := 0",
				"harvestedPlots = txHarvestedPlots",
				"harvestedQty = txHarvestedQty",
				"if err != nil {\n\t\treturn 0, 0, err\n\t}",
				"return harvestedPlots, harvestedQty, nil",
			},
			forbidden: []string{"return harvestedPlots, harvestedQty, err"},
		},
		{
			name:      "sell herb quantity",
			startFunc: "func gardenSellHerbQuantity(",
			endFunc:   "func gardenBuyRecipe(",
			wants: []string{
				"txGained := 0",
				"txSoldQty := 0",
				"gained = txGained",
				"soldQty = txSoldQty",
				"if err != nil {\n\t\treturn 0, 0, err\n\t}",
				"return gained, soldQty, nil",
			},
			forbidden: []string{"return gained, soldQty, err"},
		},
		{
			name:      "alchemy",
			startFunc: "func gardenAlchemy(",
			endFunc:   "func gardenGrantInventoryInTx(",
			wants: []string{
				"if err != nil {\n\t\treturn \"\", err\n\t}",
				"return cfg.ProductName, nil",
			},
			forbidden: []string{"return cfg.ProductName, err"},
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
				t.Fatalf("%s transactional return guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still returns possibly rolled-back values: %s", tt.name, unsafe)
			}
		}
	}
}

func TestGardenDayKeyDiagnosticsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	if got := strings.Count(text, "userID, formatPlainValue(dayKey), formatPlainError(err)"); got != 2 {
		t.Fatalf("garden day key diagnostics using formatPlainValue = %d, want 2", got)
	}
	if strings.Contains(text, "userID, dayKey, formatPlainError(err)") {
		t.Fatal("garden day key diagnostics should not log raw dayKey")
	}
}

func TestGardenPointDescriptionsSanitizeDynamicNames(t *testing.T) {
	if got := gardenPointDescriptionName("  herb\nalpha\tbeta  "); got != "herb alpha beta" {
		t.Fatalf("gardenPointDescriptionName() = %q", got)
	}
	if got := gardenPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty garden point description name fallback = %q", got)
	}

	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		`fmt.Sprintf("购买【%s】，消耗 %d 积分", cfg.SeedName, cfg.Price)`,
		`fmt.Sprintf("药铺回收【%s】x%d，获得 %d 积分", cfg.HerbName, qty, gained)`,
		`fmt.Sprintf("药铺回收【%s】x%d，急收 x%d，获得 %d 积分", cfg.HerbName, qty, urgentQty, gained)`,
		`fmt.Sprintf("参悟【%s】，消耗 %d 积分", cfg.Name, cfg.UnlockPrice)`,
		`fmt.Sprintf("炼制【%s】，炉火消耗 %d 积分", cfg.ProductName, cfg.AlchemyCost)`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("garden point description should not persist raw dynamic name: %s", unsafe)
		}
	}
	for _, want := range []string{
		`fmt.Sprintf("购买【%s】，消耗 %d 积分", gardenPointDescriptionName(cfg.SeedName), cfg.Price)`,
		`fmt.Sprintf("药铺回收【%s】x%d，获得 %d 积分", gardenPointDescriptionName(cfg.HerbName), qty, txGained)`,
		`fmt.Sprintf("药铺回收【%s】x%d，急收 x%d，获得 %d 积分", gardenPointDescriptionName(cfg.HerbName), qty, urgentQty, txGained)`,
		`fmt.Sprintf("参悟【%s】，消耗 %d 积分", gardenPointDescriptionName(cfg.Name), cfg.UnlockPrice)`,
		`fmt.Sprintf("炼制【%s】，炉火消耗 %d 积分", gardenPointDescriptionName(cfg.ProductName), cfg.AlchemyCost)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("garden point description sanitizer missing %q", want)
		}
	}
}

func TestTreasureShopPointDescriptionSanitizesItemName(t *testing.T) {
	if got := treasureShopPointDescriptionName("  pill\nalpha\tbeta  "); got != "pill alpha beta" {
		t.Fatalf("treasureShopPointDescriptionName() = %q", got)
	}
	if got := treasureShopPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty treasure shop point description name fallback = %q", got)
	}

	source, err := os.ReadFile("menu_callbacks.go")
	if err != nil {
		t.Fatalf("read menu_callbacks.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `fmt.Sprintf("购买【%s】，消耗 %d 积分", itemName, price)`) {
		t.Fatal("treasure shop point description should not persist raw item name")
	}
	if !strings.Contains(text, `fmt.Sprintf("购买【%s】，消耗 %d 积分", treasureShopPointDescriptionName(itemName), price)`) {
		t.Fatal("treasure shop point description should sanitize item name")
	}
}

func TestTreasureShopPurchaseRejectsNonPositivePrice(t *testing.T) {
	if err := purchaseTreasureShopItem(1001, "聚灵丹", 0); err == nil {
		t.Fatal("treasure shop purchase should reject zero price before asset mutation")
	}
	if err := purchaseTreasureShopItem(1001, "聚灵丹", -1); err == nil {
		t.Fatal("treasure shop purchase should reject negative price before asset mutation")
	}
}

func TestTreasureShopInventoryGrantChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("menu_callbacks.go")
	if err != nil {
		t.Fatalf("read menu_callbacks.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func purchaseTreasureShopItem(")
	if start < 0 {
		t.Fatal("purchaseTreasureShopItem missing")
	}
	end := strings.Index(text[start:], "func menuCommandText(")
	if end < 0 {
		t.Fatal("purchaseTreasureShopItem boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Clauses(inventoryQuantityUpsertClause(1)).Create(&Inventory{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SHOP_INVENTORY_GRANT_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("treasure shop inventory grant guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("treasure shop inventory grant still checks only create error")
	}
}

func TestTreasureShopConfirmChecksSessionPriceParseError(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_CONFIRM_SHOP_BUY":`)
	if start < 0 {
		t.Fatal("WAITING_CONFIRM_SHOP_BUY missing")
	}
	end := strings.Index(text[start:], `case "WAITING_INVENTORY_ACTION":`)
	if end < 0 {
		t.Fatal("WAITING_CONFIRM_SHOP_BUY boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`price, err := strconv.Atoi(session.GetTemp("buy_item_price"))`,
		`if err != nil || price <= 0`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("treasure shop confirm session price guard missing %q", want)
		}
	}
	if strings.Contains(block, `price, _ := strconv.Atoi(session.GetTemp("buy_item_price"))`) {
		t.Fatal("treasure shop confirm still ignores session price parse error")
	}
}

func TestTreasureShopBuySuccessMarkdownTextIncludesPillEffect(t *testing.T) {
	text := treasureShopBuySuccessMarkdownText("聚灵丹")
	if !strings.Contains(text, "功效：") || !strings.Contains(text, "本周最多 3 颗") {
		t.Fatalf("treasure shop buy success text missing pill effect: %s", text)
	}
}

func TestManualPillUsageCountText(t *testing.T) {
	if got := manualPillUsageCountText(2, 3, true); got != "2/3" {
		t.Fatalf("manualPillUsageCountText available = %q", got)
	}
	if got := manualPillUsageCountText(0, 2, false); got != "读取失败/2" {
		t.Fatalf("manualPillUsageCountText unavailable = %q", got)
	}
}

func TestGardenCountText(t *testing.T) {
	if got := gardenCountText(8, true); got != "8" {
		t.Fatalf("gardenCountText available = %q", got)
	}
	if got := gardenCountText(0, false); got != "读取失败" {
		t.Fatalf("gardenCountText unavailable = %q", got)
	}
}

func TestGardenPanelsUseCheckedInventoryReads(t *testing.T) {
	source, err := os.ReadFile("garden.go")
	if err != nil {
		t.Fatalf("read garden.go err = %v", err)
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
			name:      "fields",
			startFunc: "func renderGardenFields(",
			endFunc:   "func renderGardenPlantList(",
			wants: []string{
				"gardenUserPlotsWithError(userID)",
				"gardenActivePlantingsByPlotWithError(userID)",
				"灵田状态读取失败，请稍后再试。",
				"if plotErr == nil && plantingErr == nil",
			},
			forbidden: []string{
				"gardenUserPlots(userID)",
				"gardenActivePlantingsByPlot(userID)",
			},
		},
		{
			name:      "plant list",
			startFunc: "func renderGardenPlantList(",
			endFunc:   "func renderGardenSeedShop(",
			wants: []string{
				"gardenInventoryByNamesWithError(userID, gardenSeedItemNames())",
				"种子库存读取失败，请稍后再试。",
			},
			forbidden: []string{"gardenInventoryByNames(userID, gardenSeedItemNames())"},
		},
		{
			name:      "seed shop",
			startFunc: "func renderGardenSeedShop(",
			endFunc:   "func renderGardenHerbs(",
			wants: []string{
				"gardenTodaySeedPurchasesWithError(userID, dayKey)",
				"gardenInventoryByNamesWithError(userID, gardenSeedItemNames())",
				"今日剩余 `%s`，持有 `%s`",
				"额度读取失败",
			},
			forbidden: []string{
				"gardenTodaySeedPurchases(userID, dayKey)",
				"gardenInventoryByNames(userID, gardenSeedItemNames())",
			},
		},
		{
			name:      "herb bag",
			startFunc: "func renderGardenHerbs(",
			endFunc:   "func renderGardenHerbMarket(",
			wants: []string{
				"gardenInventoryByNamesWithError(userID, gardenHerbItemNames())",
				"草药库存读取失败，请稍后再试。",
			},
			forbidden: []string{"gardenInventoryByNames(userID, gardenHerbItemNames())"},
		},
		{
			name:      "herb market",
			startFunc: "func renderGardenHerbMarket(",
			endFunc:   "func renderGardenRecipes(",
			wants: []string{
				"gardenInventoryByNamesWithError(userID, gardenHerbItemNames())",
				"gardenTodayHerbMarketSalesWithError(userID, dayKey)",
				"药草库存读取失败，请稍后再试。",
				"剩余 `%s`",
			},
			forbidden: []string{
				"gardenInventoryByNames(userID, gardenHerbItemNames())",
				"gardenTodayHerbMarketSales(userID, dayKey)",
			},
		},
		{
			name:      "recipes",
			startFunc: "func renderGardenRecipes(",
			endFunc:   "func createGardenInitialPlotIfMissing(",
			wants: []string{
				"gardenUnlockedRecipesWithError(userID)",
				"gardenInventoryByNamesWithError(userID, append(gardenHerbItemNames(), gardenPillItemNames()...))",
				"丹方解锁状态读取失败，请稍后再试。",
				"炼丹材料库存读取失败，请稍后再试。",
				"state = \"读取失败\"",
				"gardenCountText(int64(inv[cfg.ProductName]), invErr == nil)",
			},
			forbidden: []string{
				"gardenUnlockedRecipes(userID)",
				"gardenInventoryByNames(userID, append(gardenHerbItemNames(), gardenPillItemNames()...))",
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
				t.Fatalf("%s checked read guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still uses unchecked read: %s", tt.name, unsafe)
			}
		}
	}
}

func TestGardenNoEmptyPlotErrorCode(t *testing.T) {
	if got := gardenErrorCode(errGardenNoEmptyPlot); got != "GARDEN_NO_EMPTY_PLOT" {
		t.Fatalf("gardenErrorCode(errGardenNoEmptyPlot) = %q", got)
	}
	if got := gardenActionErrorText(errGardenNoEmptyPlot); got == "" {
		t.Fatal("gardenActionErrorText(errGardenNoEmptyPlot) is empty")
	}
}

func TestGardenMaturityNoticeTextSortsAndEscapesItems(t *testing.T) {
	text := gardenMaturityNoticeText([]GardenPlanting{
		{PlotNo: 2, HerbName: "A_B"},
		{PlotNo: 1, HerbName: "Herb"},
	})

	first := strings.Index(text, "`1`")
	second := strings.Index(text, "`2`")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("expected notice rows sorted by plot number, got: %s", text)
	}
	if !strings.Contains(text, "A\\_B") {
		t.Fatalf("expected markdown-escaped herb name, got: %s", text)
	}
	if !strings.Contains(text, "`药园`") {
		t.Fatalf("expected garden entry hint, got: %s", text)
	}
}
