package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	gardenMiniAppInitDataMaxAge    = 24 * time.Hour
	gardenMiniAppInitDataMaxLen    = 8192
	gardenMiniAppMaxBodyBytes      = 64 * 1024
	gardenMiniAppReadHeaderTimeout = 5 * time.Second
	gardenMiniAppReadTimeout       = 10 * time.Second
	gardenMiniAppWriteTimeout      = 30 * time.Second
	gardenMiniAppIdleTimeout       = 60 * time.Second
)

//go:embed web/garden/*
var gardenMiniAppFiles embed.FS

type gardenMiniAppResponse struct {
	OK      bool                `json:"ok"`
	Message string              `json:"message,omitempty"`
	Code    string              `json:"code,omitempty"`
	State   *gardenMiniAppState `json:"state,omitempty"`
	Result  interface{}         `json:"result,omitempty"`
}

type gardenMiniAppState struct {
	ServerTime string                 `json:"serverTime"`
	Points     int                    `json:"points"`
	Counts     gardenMiniAppCounts    `json:"counts"`
	Plots      []gardenMiniAppPlot    `json:"plots"`
	Seeds      []gardenMiniAppSeed    `json:"seeds"`
	Herbs      []gardenMiniAppHerb    `json:"herbs"`
	Recipes    []gardenMiniAppRecipe  `json:"recipes"`
	Market     []gardenMiniAppOffer   `json:"market"`
	NextPlot   *gardenMiniAppNextPlot `json:"nextPlot,omitempty"`
}

type gardenMiniAppCounts struct {
	Plots          int `json:"plots"`
	ReadyPlots     int `json:"readyPlots"`
	SeedInventory  int `json:"seedInventory"`
	HerbInventory  int `json:"herbInventory"`
	RecipeUnlocked int `json:"recipeUnlocked"`
}

type gardenMiniAppNextPlot struct {
	PlotNo int `json:"plotNo"`
	Cost   int `json:"cost"`
}

type gardenMiniAppPlot struct {
	PlotNo           int    `json:"plotNo"`
	Status           string `json:"status"`
	SeedKey          string `json:"seedKey,omitempty"`
	SeedName         string `json:"seedName,omitempty"`
	HerbName         string `json:"herbName,omitempty"`
	PlantedAt        string `json:"plantedAt,omitempty"`
	MaturesAt        string `json:"maturesAt,omitempty"`
	RemainingSeconds int64  `json:"remainingSeconds"`
}

type gardenMiniAppSeed struct {
	Key         string `json:"key"`
	SeedName    string `json:"seedName"`
	HerbName    string `json:"herbName"`
	Price       int    `json:"price"`
	GrowSeconds int64  `json:"growSeconds"`
	GrowText    string `json:"growText"`
	YieldText   string `json:"yieldText"`
	DailyLimit  int    `json:"dailyLimit"`
	BoughtToday int    `json:"boughtToday"`
	LeftToday   int    `json:"leftToday"`
	Inventory   int    `json:"inventory"`
	Purchasable bool   `json:"purchasable"`
}

type gardenMiniAppHerb struct {
	Key         string `json:"key"`
	HerbName    string `json:"herbName"`
	Inventory   int    `json:"inventory"`
	BasePrice   int    `json:"basePrice"`
	MarketPrice int    `json:"marketPrice"`
	MarketLimit int    `json:"marketLimit"`
	MarketSold  int    `json:"marketSold"`
	MarketLeft  int    `json:"marketLeft"`
	Urgent      bool   `json:"urgent"`
	Sellable    bool   `json:"sellable"`
}

type gardenMiniAppOffer struct {
	SeedKey  string `json:"seedKey"`
	HerbName string `json:"herbName"`
	Price    int    `json:"price"`
	Limit    int    `json:"limit"`
	Sold     int    `json:"sold"`
	Left     int    `json:"left"`
}

type gardenMiniAppRecipe struct {
	Key              string                        `json:"key"`
	Name             string                        `json:"name"`
	ProductName      string                        `json:"productName"`
	UnlockPrice      int                           `json:"unlockPrice"`
	AlchemyCost      int                           `json:"alchemyCost"`
	Unlocked         bool                          `json:"unlocked"`
	ProductInventory int                           `json:"productInventory"`
	Effect           string                        `json:"effect,omitempty"`
	Materials        []gardenMiniAppRecipeMaterial `json:"materials"`
}

type gardenMiniAppRecipeMaterial struct {
	ItemName string `json:"itemName"`
	Need     int    `json:"need"`
	Owned    int    `json:"owned"`
	Enough   bool   `json:"enough"`
}

type gardenMiniAppAuthUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	UserName  string `json:"username"`
}

type gardenMiniAppActionRequest struct {
	SeedKey   string `json:"seedKey"`
	RecipeKey string `json:"recipeKey"`
	PlotNo    int    `json:"plotNo"`
	Quantity  int    `json:"quantity"`
}

type telegramMiniAppWebAppInfo struct {
	URL string `json:"url"`
}

type telegramMiniAppInlineButton struct {
	Text         string                     `json:"text"`
	CallbackData *string                    `json:"callback_data,omitempty"`
	WebApp       *telegramMiniAppWebAppInfo `json:"web_app,omitempty"`
	URL          *string                    `json:"url,omitempty"`
}

type telegramMiniAppInlineKeyboardMarkup struct {
	InlineKeyboard [][]telegramMiniAppInlineButton `json:"inline_keyboard"`
}

func StartGardenMiniAppServer() {
	if AppConfig == nil || !AppConfig.GardenMiniAppEnabled {
		return
	}

	mux := http.NewServeMux()
	registerGardenMiniAppRoutes(mux)

	server := &http.Server{
		Addr:              AppConfig.GardenMiniAppListen,
		Handler:           mux,
		ReadHeaderTimeout: gardenMiniAppReadHeaderTimeout,
		ReadTimeout:       gardenMiniAppReadTimeout,
		WriteTimeout:      gardenMiniAppWriteTimeout,
		IdleTimeout:       gardenMiniAppIdleTimeout,
	}

	go func() {
		log.Printf("Garden Mini App server listening on %s", formatPlainValue(AppConfig.GardenMiniAppListen))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Garden Mini App server failed: %s", formatPlainError(err))
		}
	}()
}

func registerGardenMiniAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", gardenMiniAppRootHandler)
	mux.HandleFunc("/garden", gardenMiniAppIndexHandler)
	mux.HandleFunc("/garden/", gardenMiniAppStaticHandler)
	mux.HandleFunc("/api/garden/state", gardenMiniAppAuth(gardenMiniAppStateHandler))
	mux.HandleFunc("/api/garden/open-plot", gardenMiniAppAuth(gardenMiniAppOpenPlotHandler))
	mux.HandleFunc("/api/garden/buy-seed", gardenMiniAppAuth(gardenMiniAppBuySeedHandler))
	mux.HandleFunc("/api/garden/plant", gardenMiniAppAuth(gardenMiniAppPlantHandler))
	mux.HandleFunc("/api/garden/plant-all", gardenMiniAppAuth(gardenMiniAppPlantAllHandler))
	mux.HandleFunc("/api/garden/harvest", gardenMiniAppAuth(gardenMiniAppHarvestHandler))
	mux.HandleFunc("/api/garden/harvest-all", gardenMiniAppAuth(gardenMiniAppHarvestAllHandler))
	mux.HandleFunc("/api/garden/sell-herb", gardenMiniAppAuth(gardenMiniAppSellHerbHandler))
	mux.HandleFunc("/api/garden/buy-recipe", gardenMiniAppAuth(gardenMiniAppBuyRecipeHandler))
	mux.HandleFunc("/api/garden/alchemy", gardenMiniAppAuth(gardenMiniAppAlchemyHandler))
}

func gardenMiniAppRootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeGardenMiniAppNotFound(w)
		return
	}
	if r.Method != http.MethodGet {
		writeGardenMiniAppMethodNotAllowed(w, http.MethodGet)
		return
	}
	setGardenMiniAppNoStoreHeaders(w)
	http.Redirect(w, r, "/garden", http.StatusFound)
}

func gardenMiniAppIndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeGardenMiniAppMethodNotAllowed(w, http.MethodGet)
		return
	}
	serveGardenMiniAppFile(w, "web/garden/index.html", "text/html; charset=utf-8")
}

func gardenMiniAppStaticHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeGardenMiniAppMethodNotAllowed(w, http.MethodGet)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/garden/")
	switch path {
	case "", "index.html":
		serveGardenMiniAppFile(w, "web/garden/index.html", "text/html; charset=utf-8")
	case "app.js":
		serveGardenMiniAppFile(w, "web/garden/app.js", "application/javascript; charset=utf-8")
	case "styles.css":
		serveGardenMiniAppFile(w, "web/garden/styles.css", "text/css; charset=utf-8")
	default:
		writeGardenMiniAppNotFound(w)
	}
}

func serveGardenMiniAppFile(w http.ResponseWriter, name string, contentType string) {
	data, err := fs.ReadFile(gardenMiniAppFiles, name)
	if err != nil {
		writeGardenMiniAppNotFound(w)
		return
	}
	w.Header().Set("Content-Type", contentType)
	setGardenMiniAppNoStoreHeaders(w)
	_, _ = w.Write(data)
}

func gardenMiniAppAuth(next func(http.ResponseWriter, *http.Request, *tgbotapi.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, gardenMiniAppMaxBodyBytes)
		}

		user, err := validateGardenMiniAppInitData(r.Header.Get("X-Telegram-Init-Data"))
		if err != nil {
			log.Printf("Garden Mini App auth failed: code=%s method=%s path=%s has_init_data=%t ua=%s",
				gardenMiniAppAuthErrorCode(err),
				formatPlainValue(r.Method),
				formatPlainValue(r.URL.Path),
				strings.TrimSpace(r.Header.Get("X-Telegram-Init-Data")) != "",
				formatPlainValue(r.UserAgent()),
			)
			writeGardenMiniAppError(w, http.StatusUnauthorized, "AUTH_FAILED", "登录已失效，请在 Telegram 私聊发送「药园」后点击「打开药园」重新打开")
			return
		}

		unlock := lockUser(user.ID)
		defer unlock()

		if _, _, err := ensureUserWallet(user); err != nil {
			log.Printf("Garden Mini App wallet init failed: user=%d err=%s", user.ID, formatPlainError(err))
			writeGardenMiniAppError(w, http.StatusInternalServerError, "WALLET_INIT_FAILED", "钱包初始化失败，请稍后再试")
			return
		}
		if ok, reason := ensureGardenAccessible(user.ID); !ok {
			writeGardenMiniAppError(w, http.StatusForbidden, "GARDEN_NOT_ACCESSIBLE", reason)
			return
		}

		next(w, r, user)
	}
}

func gardenMiniAppAuthErrorCode(err error) string {
	if err == nil {
		return "OK"
	}
	code := strings.TrimSpace(err.Error())
	switch code {
	case "BOT_TOKEN_MISSING", "INIT_DATA_TOO_LARGE", "HASH_MISSING", "HASH_MISMATCH", "AUTH_DATE_INVALID", "AUTH_DATE_EXPIRED", "USER_MISSING":
		return code
	default:
		return "INIT_DATA_INVALID"
	}
}

func validateGardenMiniAppInitData(initData string) (*tgbotapi.User, error) {
	if AppConfig == nil || strings.TrimSpace(AppConfig.TgToken) == "" {
		return nil, fmt.Errorf("BOT_TOKEN_MISSING")
	}
	if len(initData) > gardenMiniAppInitDataMaxLen {
		return nil, fmt.Errorf("INIT_DATA_TOO_LARGE")
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, err
	}
	receivedHash := values.Get("hash")
	if receivedHash == "" {
		return nil, fmt.Errorf("HASH_MISSING")
	}
	values.Del("hash")

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		if len(values[key]) == 0 {
			continue
		}
		pairs = append(pairs, key+"="+values[key][0])
	}
	dataCheckString := strings.Join(pairs, "\n")

	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(AppConfig.TgToken))
	secret := secretMAC.Sum(nil)

	checkMAC := hmac.New(sha256.New, secret)
	_, _ = checkMAC.Write([]byte(dataCheckString))
	calculated := hex.EncodeToString(checkMAC.Sum(nil))
	if !hmac.Equal([]byte(calculated), []byte(strings.ToLower(receivedHash))) {
		return nil, fmt.Errorf("HASH_MISMATCH")
	}

	authDateRaw := values.Get("auth_date")
	authUnix, err := strconv.ParseInt(authDateRaw, 10, 64)
	if err != nil || authUnix <= 0 {
		return nil, fmt.Errorf("AUTH_DATE_INVALID")
	}
	authAt := time.Unix(authUnix, 0)
	if time.Since(authAt) > gardenMiniAppInitDataMaxAge || time.Until(authAt) > 5*time.Minute {
		return nil, fmt.Errorf("AUTH_DATE_EXPIRED")
	}

	var authUser gardenMiniAppAuthUser
	if err := json.Unmarshal([]byte(values.Get("user")), &authUser); err != nil {
		return nil, err
	}
	if authUser.ID == 0 {
		return nil, fmt.Errorf("USER_MISSING")
	}

	return &tgbotapi.User{
		ID:        authUser.ID,
		FirstName: authUser.FirstName,
		LastName:  authUser.LastName,
		UserName:  authUser.UserName,
	}, nil
}

func gardenMiniAppStateHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	if r.Method != http.MethodGet {
		writeGardenMiniAppMethodNotAllowed(w, http.MethodGet)
		return
	}
	state, err := buildGardenMiniAppState(user.ID)
	if err != nil {
		log.Printf("Garden Mini App state failed: user=%d err=%s", user.ID, formatPlainError(err))
		writeGardenMiniAppError(w, http.StatusInternalServerError, "STATE_FAILED", "药园读取失败，请稍后再试")
		return
	}
	writeGardenMiniAppJSON(w, http.StatusOK, gardenMiniAppResponse{OK: true, State: state})
}

func gardenMiniAppOpenPlotHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		return nil, gardenOpenNextPlot(user.ID)
	}, "灵田开垦成功")
}

func gardenMiniAppBuySeedHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		return nil, gardenBuySeed(user.ID, strings.TrimSpace(req.SeedKey))
	}, "种子已入袋")
}

func gardenMiniAppPlantHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		return nil, gardenPlantSeed(user.ID, req.PlotNo, strings.TrimSpace(req.SeedKey))
	}, "种植成功")
}

func gardenMiniAppPlantAllHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		count, err := gardenPlantAllSeeds(user.ID, strings.TrimSpace(req.SeedKey))
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"plots": count}, nil
	}, "一键种植完成")
}

func gardenMiniAppHarvestHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		qty, herb, err := gardenHarvestPlot(user.ID, req.PlotNo)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"quantity": qty, "herbName": herb}, nil
	}, "收获成功")
}

func gardenMiniAppHarvestAllHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		plots, qty, err := gardenHarvestAll(user.ID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"plots": plots, "quantity": qty}, nil
	}, "一键收获完成")
}

func gardenMiniAppSellHerbHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		points, qty, err := gardenSellHerbQuantity(user.ID, strings.TrimSpace(req.SeedKey), req.Quantity)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"points": points, "quantity": qty}, nil
	}, "药草回收完成")
}

func gardenMiniAppBuyRecipeHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		return nil, gardenBuyRecipe(user.ID, strings.TrimSpace(req.RecipeKey))
	}, "丹方已参悟")
}

func gardenMiniAppAlchemyHandler(w http.ResponseWriter, r *http.Request, user *tgbotapi.User) {
	handleGardenMiniAppAction(w, r, user, func(req gardenMiniAppActionRequest) (interface{}, error) {
		product, err := gardenAlchemy(user.ID, strings.TrimSpace(req.RecipeKey))
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"productName": product}, nil
	}, "炼丹完成")
}

func handleGardenMiniAppAction(
	w http.ResponseWriter,
	r *http.Request,
	user *tgbotapi.User,
	action func(gardenMiniAppActionRequest) (interface{}, error),
	successMessage string,
) {
	if r.Method != http.MethodPost {
		writeGardenMiniAppMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req gardenMiniAppActionRequest
	if err := decodeGardenMiniAppActionRequest(r.Body, &req); err != nil {
		writeGardenMiniAppError(w, http.StatusBadRequest, "BAD_REQUEST", "请求参数异常")
		return
	}

	result, err := action(req)
	if err != nil {
		code := gardenErrorCode(err)
		if code == "" {
			code = "ACTION_FAILED"
		}
		writeGardenMiniAppError(w, http.StatusConflict, code, gardenActionErrorText(err))
		return
	}

	state, err := buildGardenMiniAppState(user.ID)
	if err != nil {
		log.Printf("Garden Mini App refresh failed: user=%d err=%s", user.ID, formatPlainError(err))
		writeGardenMiniAppJSON(w, http.StatusOK, gardenMiniAppResponse{
			OK:      true,
			Code:    "STATE_REFRESH_FAILED",
			Message: "操作已完成，园况刷新失败，正在重新同步",
			Result:  result,
		})
		return
	}
	writeGardenMiniAppJSON(w, http.StatusOK, gardenMiniAppResponse{
		OK:      true,
		Message: successMessage,
		Result:  result,
		State:   state,
	})
}

func decodeGardenMiniAppActionRequest(body io.Reader, req *gardenMiniAppActionRequest) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}

	var extra json.RawMessage
	if err := dec.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected trailing JSON value")
}

func buildGardenMiniAppState(userID int64) (*gardenMiniAppState, error) {
	now := time.Now()
	var user User
	if err := DB.Select("telegram_id", "points").Where("telegram_id = ?", userID).First(&user).Error; err != nil {
		return nil, err
	}

	var plots []GardenPlot
	if err := DB.Where("user_id = ?", userID).Order("plot_no asc").Find(&plots).Error; err != nil {
		return nil, err
	}
	var plantings []GardenPlanting
	if err := DB.Where("user_id = ? AND status = ?", userID, gardenStatusGrowing).Find(&plantings).Error; err != nil {
		return nil, err
	}
	activeByPlot := make(map[int]GardenPlanting, len(plantings))
	for _, planting := range plantings {
		activeByPlot[planting.PlotNo] = planting
	}

	seedNames := gardenSeedItemNames()
	herbNames := gardenHerbItemNames()
	pillNames := gardenPillItemNames()
	seedInventory, err := gardenInventoryByNamesWithError(userID, seedNames)
	if err != nil {
		return nil, err
	}
	herbInventory, err := gardenInventoryByNamesWithError(userID, herbNames)
	if err != nil {
		return nil, err
	}
	recipeInventory, err := gardenInventoryByNamesWithError(userID, append(append([]string{}, herbNames...), pillNames...))
	if err != nil {
		return nil, err
	}

	dayKey := signInDateKey(now)
	purchases, err := gardenMiniAppSeedPurchases(userID, dayKey)
	if err != nil {
		return nil, err
	}
	marketDayKey := gardenHerbMarketDayKey(now)
	sales, err := gardenMiniAppMarketSales(userID, marketDayKey)
	if err != nil {
		return nil, err
	}
	unlocked, err := gardenMiniAppUnlockedRecipes(userID)
	if err != nil {
		return nil, err
	}

	state := &gardenMiniAppState{
		ServerTime: now.Format(time.RFC3339),
		Points:     user.Points,
		Plots:      make([]gardenMiniAppPlot, 0),
		Seeds:      make([]gardenMiniAppSeed, 0, len(gardenSeeds)),
		Herbs:      make([]gardenMiniAppHerb, 0, len(gardenSeeds)),
		Recipes:    make([]gardenMiniAppRecipe, 0, len(gardenRecipes)),
		Market:     make([]gardenMiniAppOffer, 0),
	}

	for _, plot := range plots {
		item := gardenMiniAppPlot{
			PlotNo: plot.PlotNo,
			Status: "empty",
		}
		if planting, ok := activeByPlot[plot.PlotNo]; ok {
			item.Status = "growing"
			if !planting.MaturesAt.After(now) {
				item.Status = "ready"
				state.Counts.ReadyPlots++
			}
			item.SeedKey = planting.SeedKey
			item.SeedName = planting.SeedName
			item.HerbName = planting.HerbName
			item.PlantedAt = planting.PlantedAt.Format(time.RFC3339)
			item.MaturesAt = planting.MaturesAt.Format(time.RFC3339)
			item.RemainingSeconds = int64(time.Until(planting.MaturesAt).Seconds())
			if item.RemainingSeconds < 0 {
				item.RemainingSeconds = 0
			}
		}
		state.Plots = append(state.Plots, item)
	}
	state.Counts.Plots = len(plots)
	if len(plots) < gardenMaxPlots {
		nextNo := len(plots) + 1
		state.NextPlot = &gardenMiniAppNextPlot{PlotNo: nextNo, Cost: gardenPlotCosts[nextNo]}
	}

	for _, cfg := range gardenSeeds {
		bought := purchases[cfg.Key]
		left := cfg.DailyLimit - bought
		if left < 0 {
			left = 0
		}
		if !cfg.Purchasable {
			left = 0
		}
		state.Seeds = append(state.Seeds, gardenMiniAppSeed{
			Key:         cfg.Key,
			SeedName:    cfg.SeedName,
			HerbName:    cfg.HerbName,
			Price:       cfg.Price,
			GrowSeconds: int64(cfg.GrowDuration.Seconds()),
			GrowText:    gardenDurationText(cfg.GrowDuration),
			YieldText:   gardenYieldText(cfg),
			DailyLimit:  cfg.DailyLimit,
			BoughtToday: bought,
			LeftToday:   left,
			Inventory:   seedInventory[cfg.SeedName],
			Purchasable: cfg.Purchasable,
		})
		state.Counts.SeedInventory += seedInventory[cfg.SeedName]
	}

	offerMap := gardenTodayHerbMarketOfferMap(now)
	for _, cfg := range gardenSeeds {
		basePrice := gardenHerbBaseSellPrice(cfg)
		sellable := basePrice > 0
		herb := gardenMiniAppHerb{
			Key:       cfg.Key,
			HerbName:  cfg.HerbName,
			Inventory: herbInventory[cfg.HerbName],
			BasePrice: basePrice,
			Sellable:  sellable,
		}
		if offer, ok := offerMap[cfg.Key]; ok {
			sold := sales[cfg.Key]
			left := offer.Limit - sold
			if left < 0 {
				left = 0
			}
			herb.MarketPrice = offer.Price
			herb.MarketLimit = offer.Limit
			herb.MarketSold = sold
			herb.MarketLeft = left
			herb.Urgent = offer.Price > basePrice
			state.Market = append(state.Market, gardenMiniAppOffer{
				SeedKey:  cfg.Key,
				HerbName: cfg.HerbName,
				Price:    offer.Price,
				Limit:    offer.Limit,
				Sold:     sold,
				Left:     left,
			})
		}
		state.Herbs = append(state.Herbs, herb)
		state.Counts.HerbInventory += herb.Inventory
	}

	for _, cfg := range gardenRecipes {
		recipe := gardenMiniAppRecipe{
			Key:              cfg.Key,
			Name:             cfg.Name,
			ProductName:      cfg.ProductName,
			UnlockPrice:      cfg.UnlockPrice,
			AlchemyCost:      cfg.AlchemyCost,
			Unlocked:         unlocked[cfg.Key],
			ProductInventory: recipeInventory[cfg.ProductName],
			Effect:           pillEffectSummary(cfg.ProductName),
			Materials:        make([]gardenMiniAppRecipeMaterial, 0, len(cfg.Materials)),
		}
		for _, material := range cfg.Materials {
			owned := recipeInventory[material.ItemName]
			recipe.Materials = append(recipe.Materials, gardenMiniAppRecipeMaterial{
				ItemName: material.ItemName,
				Need:     material.Quantity,
				Owned:    owned,
				Enough:   owned >= material.Quantity,
			})
		}
		if recipe.Unlocked {
			state.Counts.RecipeUnlocked++
		}
		state.Recipes = append(state.Recipes, recipe)
	}

	return state, nil
}

func gardenMiniAppSeedPurchases(userID int64, dayKey string) (map[string]int, error) {
	var rows []GardenSeedPurchase
	if err := DB.Where("user_id = ? AND day_key = ?", userID, dayKey).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int, len(rows))
	for _, row := range rows {
		result[row.SeedKey] = row.Quantity
	}
	return result, nil
}

func gardenMiniAppMarketSales(userID int64, dayKey string) (map[string]int, error) {
	var rows []GardenHerbMarketSale
	if err := DB.Where("user_id = ? AND day_key = ?", userID, dayKey).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int, len(rows))
	for _, row := range rows {
		result[row.SeedKey] = row.Quantity
	}
	return result, nil
}

func gardenMiniAppUnlockedRecipes(userID int64) (map[string]bool, error) {
	var rows []GardenRecipeUnlock
	if err := DB.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(rows))
	for _, row := range rows {
		result[row.RecipeKey] = true
	}
	return result, nil
}

func writeGardenMiniAppJSON(w http.ResponseWriter, status int, payload gardenMiniAppResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setGardenMiniAppNoStoreHeaders(w)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Garden Mini App response encode failed: err=%s", formatPlainError(err))
	}
}

func setGardenMiniAppNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeGardenMiniAppError(w http.ResponseWriter, status int, code string, message string) {
	if strings.TrimSpace(message) == "" {
		message = "操作失败，请稍后再试"
	}
	writeGardenMiniAppJSON(w, status, gardenMiniAppResponse{
		OK:      false,
		Code:    code,
		Message: message,
	})
}

func writeGardenMiniAppMethodNotAllowed(w http.ResponseWriter, allowed string) {
	allowed = strings.TrimSpace(allowed)
	if allowed != "" {
		w.Header().Set("Allow", allowed)
	}
	writeGardenMiniAppError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不支持")
}

func writeGardenMiniAppNotFound(w http.ResponseWriter) {
	writeGardenMiniAppError(w, http.StatusNotFound, "NOT_FOUND", "not found")
}

func sendGardenMiniAppEntry(bot *tgbotapi.BotAPI, chatID int64) bool {
	if AppConfig == nil || !AppConfig.GardenMiniAppEnabled || AppConfig.GardenMiniAppURL == "" {
		return false
	}

	msg := tgbotapi.NewMessage(chatID, "药园已备好。")
	msg.ReplyMarkup = telegramMiniAppInlineKeyboardMarkup{
		InlineKeyboard: [][]telegramMiniAppInlineButton{
			{
				{
					Text:   "打开药园",
					WebApp: &telegramMiniAppWebAppInfo{URL: AppConfig.GardenMiniAppURL},
				},
			},
		},
	}
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("Send Garden Mini App entry failed: chat=%d err=%s", chatID, formatTelegramSendError(err))
		return false
	}
	return true
}

func gardenInlineMarkupWithMiniApp(markup tgbotapi.InlineKeyboardMarkup) interface{} {
	if AppConfig == nil || !AppConfig.GardenMiniAppEnabled || AppConfig.GardenMiniAppURL == "" {
		return markup
	}

	rows := make([][]telegramMiniAppInlineButton, 0, len(markup.InlineKeyboard)+1)
	for _, row := range markup.InlineKeyboard {
		converted := make([]telegramMiniAppInlineButton, 0, len(row))
		for _, button := range row {
			converted = append(converted, telegramMiniAppInlineButton{
				Text:         button.Text,
				CallbackData: button.CallbackData,
				URL:          button.URL,
			})
		}
		rows = append(rows, converted)
	}
	rows = append(rows, []telegramMiniAppInlineButton{
		{
			Text:   "打开药园",
			WebApp: &telegramMiniAppWebAppInfo{URL: AppConfig.GardenMiniAppURL},
		},
	})

	return telegramMiniAppInlineKeyboardMarkup{InlineKeyboard: rows}
}

func gardenMiniAppFallbackURLButton() telegramMiniAppInlineKeyboardMarkup {
	urlValue := AppConfig.GardenMiniAppURL
	return telegramMiniAppInlineKeyboardMarkup{
		InlineKeyboard: [][]telegramMiniAppInlineButton{
			{
				{
					Text: "打开药园",
					URL:  &urlValue,
				},
			},
		},
	}
}
