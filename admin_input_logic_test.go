package main

import (
	"os"
	"strings"
	"testing"
)

func TestValidateAdminReasonRejectsUnsafeInput(t *testing.T) {
	if got, ok := validateAdminReason("正常操作原因"); !ok || got != "正常操作原因" {
		t.Fatalf("validateAdminReason(valid) = %q/%v", got, ok)
	}
	if _, ok := validateAdminReason("短"); ok {
		t.Fatal("short admin reason should be rejected")
	}
	if _, ok := validateAdminReason("包含\n换行原因"); ok {
		t.Fatal("admin reason with newline should be rejected")
	}
	if _, ok := validateAdminReason("包含\t制表符原因"); ok {
		t.Fatal("admin reason with tab should be rejected")
	}
}

func TestAdminAdjustDailyLimitExceeded(t *testing.T) {
	if adminAdjustDailyLimitExceeded(15000, 5000) {
		t.Fatal("exactly reaching daily limit should be allowed")
	}
	if !adminAdjustDailyLimitExceeded(15001, 5000) {
		t.Fatal("exceeding daily limit should be rejected")
	}
	if !adminAdjustDailyLimitExceeded(19999, -2) {
		t.Fatal("negative adjustment should count by absolute value")
	}
}

func TestValidateServerLinesContentAndMarkdownBody(t *testing.T) {
	content, ok := validateServerLinesContent("  线路_A：https://example.com/path?a=1&b=2\r\n备用[二]  ")
	if !ok {
		t.Fatal("valid server lines should pass")
	}
	if !strings.Contains(content, "\n") || strings.Contains(content, "\r") {
		t.Fatalf("server lines should normalize line endings: %q", content)
	}

	body := serverLinesMarkdownBody(content)
	for _, want := range []string{"线路\\_A", "备用\\[二]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("server lines markdown body missing escaped %q: %q", want, body)
		}
	}
	if strings.Contains(body, "线路_A") || strings.Contains(body, "备用[二]") {
		t.Fatalf("server lines markdown body contains unescaped markdown text: %q", body)
	}

	if _, ok := validateServerLinesContent("线路\tA"); ok {
		t.Fatal("server lines with tab should be rejected")
	}
	if got := serverLinesMarkdownBody("线路\tA"); !strings.Contains(got, "线路配置异常") {
		t.Fatalf("invalid server lines markdown body = %q", got)
	}
}

func TestInventoryItemMarkdownNameNormalizesUnsafeText(t *testing.T) {
	got := inventoryItemMarkdownName("  A_B\nC\tD\u2028E\u2029F\x00G  ")
	if got != "A\\_B C D E F G" {
		t.Fatalf("inventoryItemMarkdownName normalized = %q", got)
	}
	if got := inventoryItemMarkdownName("\x00\u2028\t"); got != "-" {
		t.Fatalf("inventoryItemMarkdownName empty fallback = %q", got)
	}
}

func TestBookRequestAdminNotificationChecksAdminListReadError(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"求书工单通知管理员列表读取失败",
		"Find(&dbAdmins).Error",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("book request admin notification guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"role IN ?\", []string{\"admin\", \"super_admin\"}).Find(&dbAdmins)\n") ||
		strings.Contains(text, "DB.Where(\"role IN ?\", []string{\"admin\", \"super_admin\"}).Find(&dbAdmins)\r\n") {
		t.Fatal("book request admin notification still ignores DB admin list read errors")
	}
}

func TestBookRequestClaimReloadAndMessageIDWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func reloadBookRequestAfterClaim(db *gorm.DB, req *BookRequest, reqID uint, adminID int64, adminName string, now time.Time) error",
		"book request claim reload failed",
		"求书工单管理员消息ID记录失败",
		"reloadBookRequestAfterClaim(DB, &req, reqID, cb.From.ID, adminName, now)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("book request claim guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"id = ?\", reqID).First(&req)\n") ||
		strings.Contains(text, "DB.Where(\"id = ?\", reqID).First(&req)\r\n") {
		t.Fatal("book request claim still ignores reload errors")
	}
}

func TestBookRequestAdminMessageIDWritesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func recordBookRequestAdminMessageID(")
	if start < 0 {
		t.Fatal("recordBookRequestAdminMessageID missing")
	}
	end := strings.Index(text[start:], "func createBookRequestLog(")
	if end < 0 {
		t.Fatal("recordBookRequestAdminMessageID boundary missing")
	}
	helper := text[start : start+end]
	for _, want := range []string{
		"res := db.Model(&BookRequest{})",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"req.AdminChatID = chatID",
		"req.AdminMessageID = messageID",
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("book request admin message id helper guard missing %q", want)
		}
	}

	callbackStart := strings.Index(text, "func handleBookRequestCallback(")
	if callbackStart < 0 {
		t.Fatal("handleBookRequestCallback missing")
	}
	callbackEnd := strings.Index(text[callbackStart:], "func getTodayAuditDeltaTotalTx(")
	if callbackEnd < 0 {
		t.Fatal("handleBookRequestCallback boundary missing")
	}
	callbackBlock := text[callbackStart : callbackStart+callbackEnd]
	if !strings.Contains(callbackBlock, "recordBookRequestAdminMessageID(DB, &req") {
		t.Fatal("book request callback should use checked admin message id helper")
	}
	if strings.Contains(callbackBlock, "DB.Model(&BookRequest{})") {
		t.Fatal("book request callback still writes admin message id without checked helper")
	}
}

func TestBookRequestReadsDistinguishMissingFromDBErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func loadBookRequestByID(")
	if helperStart < 0 {
		t.Fatal("book request load helper missing")
	}
	helperEnd := strings.Index(text[helperStart:], "const callbackAlertTextMaxRunes")
	if helperEnd < 0 {
		t.Fatal("book request load helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"求书工单读取失败",
		"formatPlainError(err)",
		"return req, false, err",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("book request load helper guard missing %q", want)
		}
	}

	callbackStart := strings.Index(text, "func handleBookRequestCallback(")
	if callbackStart < 0 {
		t.Fatal("handleBookRequestCallback missing")
	}
	callbackEnd := strings.Index(text[callbackStart:], "func getTodayAuditDeltaTotalTx(")
	if callbackEnd < 0 {
		t.Fatal("handleBookRequestCallback boundary missing")
	}
	callbackBlock := text[callbackStart : callbackStart+callbackEnd]
	for _, want := range []string{
		`loadBookRequestByID(DB, reqID, "callback view")`,
		`loadBookRequestByID(DB, reqID, "callback claim")`,
		`loadBookRequestByID(DB, reqID, "callback need info")`,
		`loadBookRequestByID(DB, reqID, "callback note")`,
		`loadBookRequestByID(DB, reqID, "callback finish")`,
		`answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")`,
	} {
		if !strings.Contains(callbackBlock, want) {
			t.Fatalf("book request callback DB-error branch missing %q", want)
		}
	}
	if strings.Contains(callbackBlock, `DB.Where("id = ?", reqID).First(&req).Error`) {
		t.Fatal("book request callback still maps direct request read errors to missing")
	}

	for _, tt := range []struct {
		name      string
		startCase string
		endCase   string
		want      string
	}{
		{
			name:      "admin note",
			startCase: `case "WAITING_BOOK_ADMIN_NOTE":`,
			endCase:   `case "WAITING_BOOK_NEED_INFO_NOTE":`,
			want:      `loadBookRequestByID(DB, reqID, "admin note input")`,
		},
		{
			name:      "need info",
			startCase: `case "WAITING_BOOK_NEED_INFO_NOTE":`,
			endCase:   `case "WAITING_SET_INVITE_PRICE":`,
			want:      `loadBookRequestByID(DB, reqID, "need info input")`,
		},
	} {
		start := strings.Index(text, tt.startCase)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endCase)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			tt.want,
			`sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")`,
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s DB-error branch missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, `DB.Where("id = ?", reqID).First(&currentReq).Error`) {
			t.Fatalf("%s still maps direct request read errors to missing", tt.name)
		}
	}
}

func TestBookRequestCallbackChecksRequestIDParseError(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleBookRequestCallback(")
	if start < 0 {
		t.Fatal("handleBookRequestCallback missing")
	}
	end := strings.Index(text[start:], "func getTodayAuditDeltaTotalTx(")
	if end < 0 {
		t.Fatal("handleBookRequestCallback boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`reqID64, parseErr := strconv.ParseUint(data, 10, 64)`,
		`if parseErr != nil || reqID64 == 0`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("book request callback request id parse guard missing %q", want)
		}
	}
	if strings.Contains(block, `reqID64, _ := strconv.ParseUint(data, 10, 64)`) {
		t.Fatal("book request callback still ignores request id parse error")
	}
}

func TestBookRequestAdminMessageIDsCheckParseErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startCase string
		endCase   string
		unsafe    []string
	}{
		{
			name:      "admin note",
			startCase: `case "WAITING_BOOK_ADMIN_NOTE":`,
			endCase:   `case "WAITING_BOOK_NEED_INFO_NOTE":`,
			unsafe: []string{
				`adminMsgChatID, _ := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)`,
				`adminMsgID64, _ := strconv.ParseInt(adminMsgIDRaw, 10, 64)`,
			},
		},
		{
			name:      "need info",
			startCase: `case "WAITING_BOOK_NEED_INFO_NOTE":`,
			endCase:   `case "WAITING_SET_INVITE_PRICE":`,
			unsafe: []string{
				`adminMsgChatID, _ := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)`,
				`adminMsgID64, _ := strconv.ParseInt(adminMsgIDRaw, 10, 64)`,
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startCase)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endCase)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			`adminMsgChatID, chatParseErr := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)`,
			`adminMsgID64, msgParseErr := strconv.ParseInt(adminMsgIDRaw, 10, 64)`,
			`if chatParseErr != nil || msgParseErr != nil || adminMsgChatID == 0 || adminMsgID64 == 0`,
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s admin message id parse guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.unsafe {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still ignores admin message id parse error: %q", tt.name, unsafe)
			}
		}
	}
}

func TestValidateBookRequestNoteRejectsUnsafeInput(t *testing.T) {
	if got, ok := validateBookRequestNote("  valid note\nsecond line  "); !ok || got != "valid note\nsecond line" {
		t.Fatalf("validateBookRequestNote(valid) = %q/%v", got, ok)
	}
	if _, ok := validateBookRequestNote(strings.Repeat("a", bookRequestNoteMaxLen+1)); ok {
		t.Fatal("overlong book request note should be rejected")
	}
	for _, input := range []string{
		"note\twith tab",
		"note\x00with nul",
		"note\u2028with separator",
		"note\u2029with separator",
	} {
		if _, ok := validateBookRequestNote(input); ok {
			t.Fatalf("unsafe book request note should be rejected: %q", input)
		}
	}
}

func TestBookRequestNoteInputsUseSharedValidation(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"bookRequestNoteInvalidText",
		"replyNote, ok := validateBookRequestNote(text)",
		"if normalizedNote, ok := validateBookRequestNote(userNote); !ok {",
		"adminNote, ok := validateBookRequestNote(text)",
		"needInfoNote, ok := validateBookRequestNote(text)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("book request note validation guard missing %q", want)
		}
	}
}

func TestBookRequestStateTransitionsWriteLogAndAuditInTransaction(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func createBookRequestLogInTx(tx *gorm.DB, requestID uint, actorID int64, actorName string, action string, oldStatus string, newStatus string, note string) error") {
		t.Fatal("transactional book request log helper missing")
	}
	txStart := "err := DB." + "Transaction(func(tx *gorm.DB) error {"
	txAssignStart := "err = DB." + "Transaction(func(tx *gorm.DB) error {"

	createStart := strings.Index(text, "func createBookRequestWithinLimits(")
	if createStart < 0 {
		t.Fatal("book request create helper missing")
	}
	createEnd := strings.Index(text[createStart:], "func formatBookRequestAdminText(")
	if createEnd < 0 {
		t.Fatal("book request create helper boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		txStart,
		"checkBookRequestLimitsWithDB(tx, req.UserID, now)",
		"res := tx.Create(req)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"BOOK_REQUEST_CREATE_MISSED",
		`createBookRequestLogInTx(tx, req.ID, req.UserID, req.UserName, "create"`,
		`writeAuditLogInTx(tx, req.UserID, "CREATE_BOOK_REQUEST"`,
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("book request create transactional guard missing %q", want)
		}
	}
	if strings.Contains(createBlock, "tx.Create(req).Error") {
		t.Fatal("book request create still checks only create error")
	}

	claimStart := strings.Index(text, `if reqID, ok := parseBookRequestCallbackID(data, "br_claim_"); ok {`)
	if claimStart < 0 {
		t.Fatal("book request claim block missing")
	}
	claimEnd := strings.Index(text[claimStart:], `if reqID, ok := parseBookRequestCallbackID(data, "br_need_info_"); ok {`)
	if claimEnd < 0 {
		t.Fatal("book request claim block boundary missing")
	}
	claimBlock := text[claimStart : claimStart+claimEnd]
	if !strings.Contains(claimBlock, txStart) && !strings.Contains(claimBlock, txAssignStart) {
		t.Fatal("book request claim transaction guard missing")
	}
	for _, want := range []string{
		"claimed := false",
		"claimed = true",
		`createBookRequestLogInTx(tx, reqID, cb.From.ID, adminName, "claim"`,
		`writeAuditLogInTx(tx, cb.From.ID, "CLAIM_BOOK_REQUEST"`,
		"res.RowsAffected == 0",
		"if !claimed {",
		"book request claim update failed",
	} {
		if !strings.Contains(claimBlock, want) {
			t.Fatalf("book request claim transactional guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`writeAuditLog(msg.From.ID, "CREATE_BOOK_REQUEST"`,
		`createBookRequestLog(req.ID, msg.From.ID, userName, "create"`,
		`createBookRequestLog(req.ID, cb.From.ID, adminName, "claim"`,
		`writeAuditLog(cb.From.ID, "CLAIM_BOOK_REQUEST"`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("book request state transition still writes log/audit after transaction: %s", unsafe)
		}
	}

	finishStart := strings.Index(text, `if reqID, ok := parseBookRequestCallbackID(data, "br_done_"); ok {`)
	if finishStart < 0 {
		t.Fatal("book request finish block missing")
	}
	finishEnd := strings.Index(text[finishStart:], "func getTodayAuditDeltaTotalTx(")
	if finishEnd < 0 {
		t.Fatal("book request finish block boundary missing")
	}
	finishBlock := text[finishStart : finishStart+finishEnd]
	if !strings.Contains(finishBlock, txStart) && !strings.Contains(finishBlock, txAssignStart) {
		t.Fatal("book request finish transaction guard missing")
	}
	for _, want := range []string{
		"finished := false",
		"finished = true",
		`createBookRequestLogInTx(tx, reqID, cb.From.ID, adminName, "finish"`,
		`writeAuditLogInTx(tx, cb.From.ID, "HANDLE_BOOK_REQUEST"`,
		"res.RowsAffected == 0",
		"if !finished {",
	} {
		if !strings.Contains(finishBlock, want) {
			t.Fatalf("book request finish transactional guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`createBookRequestLog(req.ID, cb.From.ID, adminName, "finish"`,
		`writeAuditLog(cb.From.ID, "HANDLE_BOOK_REQUEST"`,
	} {
		if strings.Contains(finishBlock, unsafe) {
			t.Fatalf("book request finish still writes log/audit after transaction: %s", unsafe)
		}
	}

	userReplyStart := strings.Index(text, "func markBookRequestUserReplied(")
	if userReplyStart < 0 {
		t.Fatal("book request user reply helper missing")
	}
	userReplyEnd := strings.Index(text[userReplyStart:], "func reloadBookRequestAfterClaim(")
	if userReplyEnd < 0 {
		t.Fatal("book request user reply helper boundary missing")
	}
	userReplyBlock := text[userReplyStart : userReplyStart+userReplyEnd]
	localTxStart := "err := db." + "Transaction(func(tx *gorm.DB) error {"
	for _, want := range []string{
		localTxStart,
		`Where("id = ? AND user_id = ? AND status = ?", req.ID, req.UserID, bookRequestStatusNeedInfo)`,
		"res.RowsAffected == 0",
		`createBookRequestLogInTx(tx, req.ID, req.UserID, actorName, "user_reply"`,
	} {
		if !strings.Contains(userReplyBlock, want) {
			t.Fatalf("book request user reply transactional guard missing %q", want)
		}
	}
	if strings.Contains(text, `createBookRequestLog(needInfoReq.ID`) ||
		strings.Contains(text, `createBookRequestLog(req.ID, req.UserID, actorName, "user_reply"`) {
		t.Fatal("book request user reply still writes log after transactional helper")
	}

	adminNoteStart := strings.Index(text, `case "WAITING_BOOK_ADMIN_NOTE":`)
	if adminNoteStart < 0 {
		t.Fatal("book request admin note block missing")
	}
	adminNoteEnd := strings.Index(text[adminNoteStart:], `case "WAITING_BOOK_NEED_INFO_NOTE":`)
	if adminNoteEnd < 0 {
		t.Fatal("book request admin note block boundary missing")
	}
	adminNoteBlock := text[adminNoteStart : adminNoteStart+adminNoteEnd]
	if !strings.Contains(adminNoteBlock, txStart) && !strings.Contains(adminNoteBlock, txAssignStart) {
		t.Fatal("book request admin note transaction guard missing")
	}
	for _, want := range []string{
		"noteSaved := false",
		"noteSaved = true",
		"res.RowsAffected == 0",
		`createBookRequestLogInTx(tx, reqID, userID, adminName, "admin_note"`,
		`writeAuditLogInTx(tx, userID, "BOOK_REQUEST_ADMIN_NOTE"`,
		"if !noteSaved {",
		"book request admin note update failed",
	} {
		if !strings.Contains(adminNoteBlock, want) {
			t.Fatalf("book request admin note transactional guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`createBookRequestLog(reqID, userID, adminName, "admin_note"`,
		`writeAuditLog(userID, "BOOK_REQUEST_ADMIN_NOTE"`,
	} {
		if strings.Contains(adminNoteBlock, unsafe) {
			t.Fatalf("book request admin note still writes log/audit after transaction: %s", unsafe)
		}
	}

	needInfoStart := strings.Index(text, `case "WAITING_BOOK_NEED_INFO_NOTE":`)
	if needInfoStart < 0 {
		t.Fatal("book request need-info block missing")
	}
	needInfoEnd := strings.Index(text[needInfoStart:], `case "WAITING_SET_INVITE_PRICE":`)
	if needInfoEnd < 0 {
		t.Fatal("book request need-info block boundary missing")
	}
	needInfoBlock := text[needInfoStart : needInfoStart+needInfoEnd]
	if !strings.Contains(needInfoBlock, txStart) && !strings.Contains(needInfoBlock, txAssignStart) {
		t.Fatal("book request need-info transaction guard missing")
	}
	for _, want := range []string{
		"needInfoSaved := false",
		"needInfoSaved = true",
		"res.RowsAffected == 0",
		`createBookRequestLogInTx(tx, reqID, userID, adminName, "need_info"`,
		`writeAuditLogInTx(tx, userID, "BOOK_REQUEST_NEED_INFO"`,
		"if !needInfoSaved {",
		"book request need info update failed",
	} {
		if !strings.Contains(needInfoBlock, want) {
			t.Fatalf("book request need-info transactional guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`createBookRequestLog(reqID, userID, adminName, "need_info"`,
		`writeAuditLog(userID, "BOOK_REQUEST_NEED_INFO"`,
	} {
		if strings.Contains(needInfoBlock, unsafe) {
			t.Fatalf("book request need-info still writes log/audit after transaction: %s", unsafe)
		}
	}
}

func TestBookRequestUploadedAnnouncementUsesPermanentGroupMessage(t *testing.T) {
	source, err := os.ReadFile("book_request_announcement.go")
	if err != nil {
		t.Fatalf("read book_request_announcement.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"bookRequestAnnouncementWindow         = 20 * time.Minute",
		"bookRequestAnnouncementCandidateLimit = 5",
		"bookRequestAnnouncementPreviewTTL     = 30 * time.Minute",
		"GetRecentBookAnnouncementCandidate(bookRequestAnnouncementWindow, time.Now())",
		"getRecentAbsLibraryItems(library.ID, bookRequestAnnouncementCandidateLimit)",
		"tgbotapi.NewPhoto(AppConfig.NoticeGroupID",
		"sendNoAutoDelete(bot, photo)",
		"sendNoAutoDelete(bot, msg)",
		"formatBookAnnouncementCaption",
		"DownloadBookAnnouncementCover",
		"storeBookAnnouncementPreviewCandidate(reqID, candidate.ItemID)",
		"resolveBookAnnouncementPreviewCandidate(reqID, token, time.Now())",
		"公告预览已失效，请重新生成预览",
		"封面发送失败，降级为纯文本",
		"br_ann_pub_",
		"br_ann_skip_",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("book request uploaded announcement guard missing %q", want)
		}
	}

	publishStart := strings.Index(text, "func publishBookRequestGroupAnnouncement(")
	if publishStart < 0 {
		t.Fatal("publishBookRequestGroupAnnouncement missing")
	}
	publishEnd := strings.Index(text[publishStart:], "func removeBookAnnouncementPreviewButtons(")
	if publishEnd < 0 {
		t.Fatal("publishBookRequestGroupAnnouncement boundary missing")
	}
	publishBlock := text[publishStart : publishStart+publishEnd]
	if strings.Contains(publishBlock, "sendAutoDelete") {
		t.Fatal("book request group announcement must not use auto-delete sender")
	}
	if strings.Contains(publishBlock, "PinChatMessage") ||
		strings.Contains(publishBlock, "NewPinChatMessage") {
		t.Fatal("book request group announcement must not pin messages")
	}
}

func TestBookRequestUploadedAnnouncementCallbackIsHookedAfterFinish(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"if handleBookRequestAnnouncementCallback(bot, cb) {",
		"callbackText := \"已处理\"",
		"if status == bookRequestStatusUploaded {",
		"callbackText = maybePromptBookRequestGroupAnnouncement(bot, cb.From.ID, req)",
		"answerCallback(bot, cb.ID, callbackText)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("book request announcement callback hook missing %q", want)
		}
	}
}

func TestBookRequestLogInTxChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createBookRequestLogInTx(")
	if start < 0 {
		t.Fatal("createBookRequestLogInTx missing")
	}
	end := strings.Index(text[start:], "func isMenuLikeBookRequestReply(")
	if end < 0 {
		t.Fatal("createBookRequestLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Create(&BookRequestLog{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"BOOK_REQUEST_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("book request log create guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("createBookRequestLogInTx still checks only create error")
	}
}

func TestBestEffortTraceCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	autoStart := strings.Index(text, "func createAutoDeleteMessageRecord(")
	if autoStart < 0 {
		t.Fatal("createAutoDeleteMessageRecord missing")
	}
	autoEnd := strings.Index(text[autoStart:], "func registerAutoDeleteMessage(")
	if autoEnd < 0 {
		t.Fatal("createAutoDeleteMessageRecord boundary missing")
	}
	autoBlock := text[autoStart : autoStart+autoEnd]
	for _, want := range []string{
		"DB == nil",
		"res := DB.Create(&AutoDeleteMsg{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"AUTO_DELETE_MSG_CREATE_MISSED",
	} {
		if !strings.Contains(autoBlock, want) {
			t.Fatalf("auto delete message create guard missing %q", want)
		}
	}
	if strings.Contains(autoBlock, "}).Error") {
		t.Fatal("createAutoDeleteMessageRecord still checks only create error")
	}

	registerStart := strings.Index(text, "func registerAutoDeleteMessage(")
	if registerStart < 0 {
		t.Fatal("registerAutoDeleteMessage missing")
	}
	registerEnd := strings.Index(text[registerStart:], "func registerIncomingGroupCommandForAutoDelete(")
	if registerEnd < 0 {
		t.Fatal("registerAutoDeleteMessage boundary missing")
	}
	registerBlock := text[registerStart : registerStart+registerEnd]
	if !strings.Contains(registerBlock, "createAutoDeleteMessageRecord(chatID, messageID") {
		t.Fatal("registerAutoDeleteMessage should use checked auto delete helper")
	}
	if strings.Contains(registerBlock, "DB.Create(&AutoDeleteMsg{") {
		t.Fatal("registerAutoDeleteMessage still creates auto delete record directly")
	}

	logStart := strings.Index(text, "func createBookRequestLog(")
	if logStart < 0 {
		t.Fatal("createBookRequestLog missing")
	}
	logEnd := strings.Index(text[logStart:], "func createBookRequestLogInTx(")
	if logEnd < 0 {
		t.Fatal("createBookRequestLog boundary missing")
	}
	logBlock := text[logStart : logStart+logEnd]
	for _, want := range []string{
		"res := DB.Create(&BookRequestLog{",
		"res.Error != nil",
		"res.Error == nil && res.RowsAffected == 0",
		"BOOK_REQUEST_LOG_CREATE_MISSED",
		"写入求书工单日志未命中",
		"formatPlainValue(action), formatPlainError(err)",
	} {
		if !strings.Contains(logBlock, want) {
			t.Fatalf("best-effort book request log guard missing %q", want)
		}
	}
	if strings.Contains(logBlock, "}).Error") {
		t.Fatal("createBookRequestLog still checks only create error")
	}
	if strings.Contains(logBlock, "actorID, action, formatPlainError(err)") {
		t.Fatal("createBookRequestLog diagnostics should format action")
	}
	if strings.Contains(logBlock, "\u923f") || strings.Contains(logBlock, "\u9350\u6b13\u53c6\u59f9") {
		t.Fatal("createBookRequestLog diagnostics contain mojibake")
	}
}

func TestPasswordSecurityFlowChecksLocalUserReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"安全码验证读取本地档案失败",
		"修改密码读取本地档案失败",
		"修改密码缺少 ABS 用户ID",
		"strings.TrimSpace(u.AbsUserID) == \"\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("password security flow guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\n\t\tif ok, errMsg := verifyUserSecurityCodeWithCooldown") ||
		strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\r\n\t\tif ok, errMsg := verifyUserSecurityCodeWithCooldown") {
		t.Fatal("security code verification still ignores local user read errors")
	}
	if strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\n\t\treplyText(bot, chatID, \"⏳ 正在同步密码...\")") ||
		strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\r\n\t\treplyText(bot, chatID, \"⏳ 正在同步密码...\")") {
		t.Fatal("password update still ignores local user read errors")
	}
}

func TestQueryCodeHandlesDatabaseReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_QUERY_CODE":`)
	if start < 0 {
		t.Fatal("WAITING_QUERY_CODE block missing")
	}
	blockStart := start + len(`case "WAITING_QUERY_CODE":`)
	end := strings.Index(text[blockStart:], `case "WAITING_`)
	if end < 0 {
		end = strings.Index(text[blockStart:], "// ==========================================")
	}
	if end < 0 {
		t.Fatal("WAITING_QUERY_CODE boundary missing")
	}
	block := text[start : blockStart+end]
	for _, want := range []string{
		"卡密溯源邀请码读取失败",
		"卡密溯源续期卡读取失败",
		"卡密溯源使用者档案读取失败",
		"卡密查询失败，请稍后重试。",
		"使用者档案**: `读取失败`",
		"errors.Is(inviteErr, gorm.ErrRecordNotFound)",
		"errors.Is(renewErr, gorm.ErrRecordNotFound)",
		"errors.Is(userErr, gorm.ErrRecordNotFound)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("query code read guard missing %q", want)
		}
	}
	if strings.Contains(block, `if DB.Where("code_hash = ?", queryHash).First(&invCode).Error == nil`) ||
		strings.Contains(block, `if DB.Where("code_hash = ?", queryHash).First(&renCode).Error == nil`) ||
		strings.Contains(block, `if DB.Where("telegram_id = ?", usedByID).First(&user).Error == nil`) {
		t.Fatal("query code flow still treats DB read errors as not found")
	}
}

func TestUserReadOnlyPanelsHandleDatabaseReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"天道奖池读取失败",
		"progressText = \"`读取失败`\"",
		"用户获取线路读取配置失败",
		"线路配置暂时读取失败，请稍后再试。",
		"我的信息读取本地档案失败",
		"账户档案暂时读取失败，请稍后重试。",
		"errors.Is(userErr, gorm.ErrRecordNotFound)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("read-only panel DB error guard missing %q", want)
		}
	}
}

func TestAccountEntryFlowsHandleLocalUserReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"修仙榜刷新用户档案读取失败",
		"听书报告入口读取本地档案失败",
		"注册入口读取本地正式账号失败",
		"绑定入口读取本地正式账号失败",
		"绑定校验后读取既有绑定失败",
		"账户安全入口读取本地档案失败",
		"换绑安全码校验读取旧档案失败",
		"注销安全码校验读取本地档案失败",
		"解绑安全码校验读取本地档案失败",
		"本地绑定状态读取失败，请稍后重试。",
		"本地档案读取失败，请稍后重试。",
		"自助注销读取本地档案失败",
		"本地档案读取失败，注销未执行，请稍后重试。",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"errors.Is(existingErr, gorm.ErrRecordNotFound)",
		"errors.Is(userErr, gorm.ErrRecordNotFound)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("account entry read guard missing %q", want)
		}
	}
}

func TestAdminTargetReadsDistinguishNotFoundFromDatabaseErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"授权管理员目标用户读取失败",
		"白名单目标用户读取失败",
		"调账目标用户读取失败",
		"模拟过期目标用户读取失败",
		"封禁入口目标用户读取失败",
		"封禁确认目标用户读取失败",
		"物理删号目标用户读取失败",
		"物理删号确认目标用户读取失败",
		"目标用户读取失败，请稍后重试。",
		"errors.Is(err, gorm.ErrRecordNotFound)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("admin target read guard missing %q", want)
		}
	}
}

func TestAdminQueryUserReadErrorsDoNotLookNotFound(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_QUERY_USER":`)
	if start < 0 {
		t.Fatal("WAITING_QUERY_USER missing")
	}
	end := strings.Index(text[start:], `case "WAITING_SUSPEND_USER":`)
	if end < 0 {
		t.Fatal("WAITING_QUERY_USER boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"foundUser := false",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"查询用户 TG ID 读取失败",
		"查询用户用户名回退读取失败",
		"查询用户用户名读取失败",
		"用户档案读取失败，请稍后重试。",
		"formatPlainValue(cleanQuery)",
		"formatPlainError(err)",
		"if !foundUser {",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("admin query user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `if err != nil {
			replyText(bot, chatID, "❌ 数据库中未查找到该用户。")`) {
		t.Fatal("WAITING_QUERY_USER still treats all read errors as user not found")
	}
	if strings.Contains(block, `if err != nil {
				err = DB.Where("username = ?", cleanQuery).First(&targetUser).Error
			}`) {
		t.Fatal("WAITING_QUERY_USER still falls back to username after any TG ID read error")
	}
}

func TestAdminLocalStatusMutationsCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden []string
	}{
		{
			name:      "suspend local status",
			startFunc: "func applySuspendLocalStatusWithAudit(",
			endFunc:   "func applyRenewReactivateLocalStatusWithAudit(",
			markers: []string{
				`Where("id = ? AND abs_user_id = ? AND role <> ?", target.ID, expectedAbsUserID, "super_admin")`,
				"res.RowsAffected == 0",
				`fmt.Errorf("target_state_changed")`,
			},
			forbidden: []string{
				`tx.Model(&target).Update("is_suspended", suspended)`,
			},
		},
		{
			name:      "renew reactivation local status",
			startFunc: "func applyRenewReactivateLocalStatusWithAudit(",
			endFunc:   "func deleteLocalUserWithAudit(",
			markers: []string{
				`Where("id = ? AND abs_user_id = ?", target.ID, expectedAbsUserID)`,
				"res.RowsAffected == 0",
				`fmt.Errorf("target_state_changed")`,
			},
			forbidden: []string{
				`tx.Model(&target).Update("is_suspended", false)`,
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
			t.Fatalf("%s end missing", tt.name)
		}
		block := text[start : start+end]
		for _, marker := range tt.markers {
			if !strings.Contains(block, marker) {
				t.Fatalf("%s missing marker %s", tt.name, marker)
			}
		}
		for _, forbidden := range tt.forbidden {
			if strings.Contains(block, forbidden) {
				t.Fatalf("%s still contains forbidden update %s", tt.name, forbidden)
			}
		}
	}
}

func TestHighRiskAdminAuditDetailsUsePlainValue(t *testing.T) {
	adminSource, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	adminText := string(adminSource)
	for _, want := range []string{
		`value, formatPlainValue(reason))`,
		`newLen, formatPlainValue(reason))`,
		`count, formatPlainValue(reason))`,
		`days, count, formatPlainValue(reason))`,
		`formatPlainValue(target.Username), targetID, suspended, formatPlainValue(reason)`,
		`formatPlainValue(target.Username), target.TelegramID, formatPlainValue(target.AbsUserID)`,
		`formatPlainValue(target.Username), targetID, formatPlainValue(reason)`,
		`targetID, expireAt.Format(time.RFC3339), formatPlainValue(reason)`,
	} {
		if !strings.Contains(adminText, want) {
			t.Fatalf("admin mutation audit detail missing sanitized dynamic field pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		`value, reason)`,
		`newLen, reason)`,
		`count, reason)`,
		`days, count, reason)`,
		`target.Username, targetID, suspended, reason`,
		`target.Username, target.TelegramID, target.AbsUserID`,
		`target.Username, targetID, reason`,
		`targetID, expireAt.Format(time.RFC3339), reason`,
	} {
		if strings.Contains(adminText, unsafe) {
			t.Fatalf("admin mutation audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}

	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	stateText := string(stateSource)
	for _, want := range []string{
		`successCount, failCount, formatPlainValue(reason))`,
		`formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(apiErr)`,
		`formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(err)`,
		`formatPlainValue(reason), formatPlainError(err)`,
		`messageID, formatPlainValue(reason)`,
	} {
		if !strings.Contains(stateText, want) {
			t.Fatalf("state machine high-risk audit detail missing sanitized dynamic field pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		`len(targetIDs), successCount, failCount, reason)`,
		`tUser.Username, tgtID, actionText, reason, formatPlainError(apiErr)`,
		`tUser.Username, tgtID, actionText, reason, formatPlainError(err)`,
		`fmt.Sprintf("手动触发加密数据库备份失败，原因：%s，错误：%s", reason, formatPlainError(err))`,
		`messageID, reason))`,
	} {
		if strings.Contains(stateText, unsafe) {
			t.Fatalf("state machine high-risk audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}

func TestAdminAdjustReasonStepChecksSessionParseErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_MANAGE_POINTS_REASON":`)
	if start < 0 {
		t.Fatal("WAITING_MANAGE_POINTS_REASON missing")
	}
	end := strings.Index(text[start:], `case "WAITING_CONFIRM_MANAGE_POINTS":`)
	if end < 0 {
		t.Fatal("WAITING_MANAGE_POINTS_REASON boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`tID, err := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)`,
		`if err != nil || tID == 0`,
		`val, err := strconv.Atoi(session.GetTemp("points_delta"))`,
		`if err != nil || val == 0`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("admin adjust reason step session parse guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`tID, _ := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)`,
		`val, _ := strconv.Atoi(session.GetTemp("points_delta"))`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("admin adjust reason step still ignores session parse error: %q", unsafe)
		}
	}
}

func TestAdminGenerateCodeSessionValuesCheckParseErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startCase string
		endCase   string
		wants     []string
		unsafe    []string
	}{
		{
			name:      "invite reason",
			startCase: `case "WAITING_GEN_INVITE_REASON":`,
			endCase:   `case "WAITING_CONFIRM_GEN_INVITE":`,
			wants: []string{
				`count, err := strconv.Atoi(session.GetTemp("invite_count"))`,
				`if err != nil || count <= 0 || count > 100`,
			},
			unsafe: []string{
				`count, _ := strconv.Atoi(session.GetTemp("invite_count"))`,
			},
		},
		{
			name:      "invite confirm",
			startCase: `case "WAITING_CONFIRM_GEN_INVITE":`,
			endCase:   `case "WAITING_GEN_RENEW_DAYS":`,
			wants: []string{
				`count, err := strconv.Atoi(session.GetTemp("invite_count"))`,
				`if err != nil || count <= 0 || count > 100`,
			},
			unsafe: []string{
				`count, _ := strconv.Atoi(session.GetTemp("invite_count"))`,
			},
		},
		{
			name:      "renew reason",
			startCase: `case "WAITING_GEN_RENEW_REASON":`,
			endCase:   `case "WAITING_CONFIRM_GEN_RENEW":`,
			wants: []string{
				`days, err := strconv.Atoi(session.GetTemp("days"))`,
				`if err != nil || days <= 0 || days > 365`,
				`count, err := strconv.Atoi(session.GetTemp("renew_count"))`,
				`if err != nil || count <= 0 || count > 100`,
			},
			unsafe: []string{
				`days, _ := strconv.Atoi(session.GetTemp("days"))`,
				`count, _ := strconv.Atoi(session.GetTemp("renew_count"))`,
			},
		},
		{
			name:      "renew confirm",
			startCase: `case "WAITING_CONFIRM_GEN_RENEW":`,
			endCase:   `case "WAITING_SIMULATE_EXPIRE":`,
			wants: []string{
				`days, err := strconv.Atoi(session.GetTemp("days"))`,
				`if err != nil || days <= 0 || days > 365`,
				`count, err := strconv.Atoi(session.GetTemp("renew_count"))`,
				`if err != nil || count <= 0 || count > 100`,
			},
			unsafe: []string{
				`days, _ := strconv.Atoi(session.GetTemp("days"))`,
				`count, _ := strconv.Atoi(session.GetTemp("renew_count"))`,
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startCase)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endCase)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s session parse guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.unsafe {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still ignores session parse error: %q", tt.name, unsafe)
			}
		}
	}
}

func TestSetWhitelistRejectsSuperAdminTargets(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func setWhitelistWithAudit(")
	if start < 0 {
		t.Fatal("setWhitelistWithAudit missing")
	}
	end := strings.Index(text[start:], "func simulateExpireWithAudit(")
	if end < 0 {
		t.Fatal("setWhitelistWithAudit boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`if target.Role == "super_admin"`,
		"adminMutationTargetSuperAdmin",
		`Where("id = ? AND is_whitelist = ? AND role <> ?", target.ID, false, "super_admin")`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("whitelist super admin guard missing %q", want)
		}
	}

	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	stateText := string(stateSource)
	idStart := strings.Index(stateText, `case "WAITING_WHITELIST_ID":`)
	if idStart < 0 {
		t.Fatal("WAITING_WHITELIST_ID block missing")
	}
	idEnd := strings.Index(stateText[idStart:], `case "WAITING_WHITELIST_REASON":`)
	if idEnd < 0 {
		t.Fatal("WAITING_WHITELIST_ID boundary missing")
	}
	idBlock := stateText[idStart : idStart+idEnd]
	if !strings.Contains(idBlock, `if tUser.Role == "super_admin"`) {
		t.Fatal("whitelist entry should reject super admin targets before confirmation")
	}

	confirmStart := strings.Index(stateText, `case "WAITING_CONFIRM_WHITELIST":`)
	if confirmStart < 0 {
		t.Fatal("WAITING_CONFIRM_WHITELIST block missing")
	}
	confirmEnd := strings.Index(stateText[confirmStart:], `case "WAITING_SET_SERVER_LINES":`)
	if confirmEnd < 0 {
		t.Fatal("WAITING_CONFIRM_WHITELIST boundary missing")
	}
	confirmBlock := stateText[confirmStart : confirmStart+confirmEnd]
	if !strings.Contains(confirmBlock, "case adminMutationTargetSuperAdmin:") {
		t.Fatal("whitelist confirmation should handle super admin target status")
	}
}

func TestAdminMutationStatusReturnValuesOnlyAfterSuccess(t *testing.T) {
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
			name:      "promote admin",
			startFunc: "func promoteAdminWithAudit(",
			endFunc:   "func setWhitelistWithAudit(",
			wants: []string{
				"txStatus := adminMutationOK",
				"txStatus = adminMutationNotFound",
				"txStatus = adminMutationTargetSuperAdmin",
				"txStatus = adminMutationAlreadyAdmin",
				"txStatus = adminMutationTargetStateChanged",
				"return adminMutationOK, err",
				"return txStatus, nil",
			},
			forbidden: []string{
				"status := adminMutationOK",
				"return status, err",
			},
		},
		{
			name:      "set whitelist",
			startFunc: "func setWhitelistWithAudit(",
			endFunc:   "func simulateExpireWithAudit(",
			wants: []string{
				"txStatus := adminMutationOK",
				"txStatus = adminMutationNotFound",
				"txStatus = adminMutationTargetSuperAdmin",
				"txStatus = adminMutationAlreadyWhitelisted",
				"txStatus = adminMutationTargetStateChanged",
				"return adminMutationOK, err",
				"return txStatus, nil",
			},
			forbidden: []string{
				"status := adminMutationOK",
				"return status, err",
			},
		},
		{
			name:      "simulate expire",
			startFunc: "func simulateExpireWithAudit(",
			endFunc:   "",
			wants: []string{
				"txStatus := adminMutationOK",
				"txStatus = adminMutationNotFound",
				"txStatus = adminMutationTargetSuperAdmin",
				"txStatus = adminMutationTargetStateChanged",
				"return adminMutationOK, time.Time{}, err",
				"return txStatus, expireAt, nil",
			},
			forbidden: []string{
				"status := adminMutationOK",
				"return status, expireAt, err",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := len(text) - start
		if tt.endFunc != "" {
			end = strings.Index(text[start:], tt.endFunc)
			if end < 0 {
				t.Fatalf("%s boundary missing", tt.name)
			}
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing post-transaction status return guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still exposes transactional intermediate status: %s", tt.name, unsafe)
			}
		}
	}
}

func TestAdminCodeGenerationReturnValuesOnlyAfterAuditSuccess(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		audit     string
	}{
		{
			name:      "invite codes",
			startFunc: "func generateInviteCodesWithAudit(",
			endFunc:   "func generateRenewCodesWithAudit(",
			audit:     `"GENERATE_INVITE_CODES"`,
		},
		{
			name:      "renew codes",
			startFunc: "func generateRenewCodesWithAudit(",
			endFunc:   "func createAdminInviteCodeInTx(",
			audit:     `"GENERATE_RENEW_CODES"`,
		},
	}

	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s function missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			"var codes []string",
			"txCodes := make([]string, 0, count)",
			"txCodes = append(txCodes, code)",
			"codes = txCodes",
			"return nil, err",
			"return codes, nil",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s code generation return guard missing %q", tt.name, want)
			}
		}
		auditPos := strings.Index(block, tt.audit)
		publishPos := strings.Index(block, "codes = txCodes")
		if auditPos < 0 || publishPos < 0 || publishPos < auditPos {
			t.Fatalf("%s must publish generated codes only after audit write", tt.name)
		}
		for _, unsafe := range []string{
			"codes := make([]string, 0, count)",
			"codes = append(codes, code)",
		} {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still appends transactional codes directly to outer result: %s", tt.name, unsafe)
			}
		}
	}
}

func TestSuspendAndDeleteRecheckSuperAdminBeforeABSCalls(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name       string
		startCase  string
		endCase    string
		beforeCall string
	}{
		{
			name:       "suspend confirmation",
			startCase:  `case "WAITING_CONFIRM_SUSPEND_USER":`,
			endCase:    `case "WAITING_FORCE_DELETE_USER":`,
			beforeCall: `absClient.SetUserActiveStatus`,
		},
		{
			name:       "force delete entry",
			startCase:  `case "WAITING_FORCE_DELETE_USER":`,
			endCase:    `case "WAITING_FORCE_DELETE_REASON":`,
			beforeCall: `session.SetTemp("delete_tgt_uid"`,
		},
		{
			name:       "force delete confirmation",
			startCase:  `case "WAITING_CONFIRM_FORCE_DELETE":`,
			endCase:    `case "WAITING_QUERY_CODE":`,
			beforeCall: `absClient.DeleteUser`,
		},
	}

	for _, tt := range tests {
		start := strings.Index(text, tt.startCase)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endCase)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		guard := strings.Index(block, `if tUser.Role == "super_admin"`)
		if guard < 0 {
			t.Fatalf("%s missing target super admin recheck", tt.name)
		}
		call := strings.Index(block, tt.beforeCall)
		if call < 0 {
			t.Fatalf("%s protected operation marker missing", tt.name)
		}
		if guard > call {
			t.Fatalf("%s checks super admin after protected operation", tt.name)
		}
	}
}

func TestAdminAdjustAndForceDeleteAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	adjustStart := strings.Index(text, `case "WAITING_CONFIRM_MANAGE_POINTS":`)
	if adjustStart < 0 {
		t.Fatal("WAITING_CONFIRM_MANAGE_POINTS missing")
	}
	adjustEnd := strings.Index(text[adjustStart:], `case "WAITING_REG_USER":`)
	if adjustEnd < 0 {
		t.Fatal("WAITING_CONFIRM_MANAGE_POINTS boundary missing")
	}
	adjustBlock := text[adjustStart : adjustStart+adjustEnd]
	for _, want := range []string{
		`fmt.Sprintf("管理员调账：%s", formatPlainValue(reason))`,
		`formatPlainValue(targetName), tID, beforePoints, afterPoints, val, actualDelta, formatPlainValue(reason)`,
	} {
		if !strings.Contains(adjustBlock, want) {
			t.Fatalf("admin adjust audit detail guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`fmt.Sprintf("管理员调账：%s", reason)`,
		`targetName, tID, beforePoints, afterPoints, val, actualDelta, reason`,
	} {
		if strings.Contains(adjustBlock, unsafe) {
			t.Fatalf("admin adjust detail still contains raw dynamic fields: %q", unsafe)
		}
	}

	deleteStart := strings.Index(text, `case "WAITING_CONFIRM_FORCE_DELETE":`)
	if deleteStart < 0 {
		t.Fatal("WAITING_CONFIRM_FORCE_DELETE missing")
	}
	deleteEnd := strings.Index(text[deleteStart:], `case "WAITING_QUERY_CODE":`)
	if deleteEnd < 0 {
		t.Fatal("WAITING_CONFIRM_FORCE_DELETE boundary missing")
	}
	deleteBlock := text[deleteStart : deleteStart+deleteEnd]
	for _, want := range []string{
		`formatPlainValue(deleted.Username), tgtID, formatPlainValue(deleted.AbsUserID), formatPlainValue(reason)`,
		`"FORCE_DELETE_USER"`,
	} {
		if !strings.Contains(deleteBlock, want) {
			t.Fatalf("force delete audit detail guard missing %q", want)
		}
	}
	if strings.Contains(deleteBlock, `deleted.Username, tgtID, deleted.AbsUserID, reason`) {
		t.Fatal("force delete audit detail still contains raw dynamic fields")
	}
}

func TestSuspendFailureAuditWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_CONFIRM_SUSPEND_USER":`)
	if start < 0 {
		t.Fatal("WAITING_CONFIRM_SUSPEND_USER missing")
	}
	end := strings.Index(text[start:], `case "WAITING_FORCE_DELETE_USER":`)
	if end < 0 {
		t.Fatal("WAITING_CONFIRM_SUSPEND_USER boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`writeAuditLogInTx(DB, userID, auditAction+"_FAILED"`,
		`writeAuditLogInTx(DB, userID, auditAction+"_LOCAL_FAILED"`,
		`formatPlainValue(auditAction+"_FAILED")`,
		`formatPlainValue(auditAction+"_LOCAL_FAILED")`,
		"formatPlainError(auditErr)",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("suspend checked failure audit guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`writeAuditLog(userID, auditAction+"_FAILED"`,
		`writeAuditLog(userID, auditAction+"_LOCAL_FAILED"`,
		`auditAction+"_FAILED", formatPlainError(auditErr)`,
		`auditAction+"_LOCAL_FAILED", formatPlainError(auditErr)`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("suspend failure audit still uses unchecked helper %q", forbidden)
		}
	}
}

func TestRenewReactivateFailureAuditWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `func sendRenewRedeemResult(`)
	if start < 0 {
		t.Fatal("sendRenewRedeemResult missing")
	}
	end := strings.Index(text[start:], `func getUserRoleFromDBChecked(`)
	if end < 0 {
		t.Fatal("sendRenewRedeemResult boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_FAILED"`,
		`writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_LOCAL_FAILED"`,
		"formatPlainError(auditErr)",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("renew reactivation checked failure audit guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`writeAuditLog(userID, "RENEW_REACTIVATE_USER_FAILED"`,
		`writeAuditLog(userID, "RENEW_REACTIVATE_USER_LOCAL_FAILED"`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("renew reactivation failure audit still uses unchecked helper %q", forbidden)
		}
	}
}

func TestManualBackupAuditWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func backupDatabaseToTelegram(")
	if start < 0 {
		t.Fatal("backupDatabaseToTelegram missing")
	}
	end := strings.Index(text[start:], "type PlayerBet struct")
	if end < 0 {
		t.Fatal("backupDatabaseToTelegram boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`auditErr := writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP_FAILED"`,
		`writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP"`,
		"手动加密备份失败审计写入失败",
		"手动加密备份已发送，但成功审计写入失败",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("manual backup checked audit guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`writeAuditLog(actorID, "MANUAL_BACKUP_FAILED"`,
		`writeAuditLog(actorID, "MANUAL_BACKUP"`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("manual backup audit still uses unchecked helper %q", forbidden)
		}
	}
}

func TestCleanWidowsAuditWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_CONFIRM_CLEAN_WIDOWS":`)
	if start < 0 {
		t.Fatal("WAITING_CONFIRM_CLEAN_WIDOWS missing")
	}
	end := strings.Index(text[start:], `case "WAITING_QUERY_USER":`)
	if end < 0 {
		t.Fatal("WAITING_CONFIRM_CLEAN_WIDOWS boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`auditErr := writeAuditLogInTx(DB, userID, "CLEAN_WIDOWS"`,
		"formatPlainError(auditErr)",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"审计写入失败",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("clean widows checked audit guard missing %q", want)
		}
	}
	if strings.Contains(block, `writeAuditLog(userID, "CLEAN_WIDOWS"`) {
		t.Fatal("clean widows audit still uses unchecked helper")
	}
}

func TestCultivationAdminWriteAuditsAreTransactional(t *testing.T) {
	source, err := os.ReadFile("cultivation_admin_write_commands.go")
	if err != nil {
		t.Fatalf("read cultivation_admin_write_commands.go err = %v", err)
	}
	text := string(source)

	execStart := strings.Index(text, "func executeCultivationAdminWriteCommand(")
	if execStart < 0 {
		t.Fatal("executeCultivationAdminWriteCommand missing")
	}
	execEnd := strings.Index(text[execStart:], "\n\tparts := strings.Fields(text)")
	if execEnd < 0 {
		t.Fatal("executeCultivationAdminWriteCommand reload boundary missing")
	}
	reloadBlock := text[execStart : execStart+execEnd]
	for _, want := range []string{
		`writeAuditLogInTx(DB, actorID, "RELOAD_CULTIVATION_RULES"`,
		`formatPlainValue(reason)`,
		`return "", fmt.Errorf("写入修仙配置重载审计失败：%w", err)`,
	} {
		if !strings.Contains(reloadBlock, want) {
			t.Fatalf("cultivation reload checked audit guard missing %q", want)
		}
	}
	if strings.Contains(reloadBlock, `writeAuditLog(actorID, "RELOAD_CULTIVATION_RULES"`) {
		t.Fatal("cultivation reload audit still uses unchecked helper")
	}

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		action    string
		missed    string
		unsafe    string
	}{
		{
			name:      "breakthrough config",
			startFunc: "func updateBreakthroughConfigField(",
			endFunc:   "func updateRealmThreshold(",
			action:    `"UPDATE_BREAKTHROUGH_CONFIG"`,
			missed:    "突破配置更新未命中",
			unsafe:    "Update(field, value).Error",
		},
		{
			name:      "realm threshold",
			startFunc: "func updateRealmThreshold(",
			endFunc:   "func updateMinorRealmThreshold(",
			action:    `"UPDATE_REALM_THRESHOLD"`,
			missed:    "境界门槛更新未命中",
			unsafe:    "Updates(map[string]interface{}{",
		},
		{
			name:      "minor realm threshold",
			startFunc: "func updateMinorRealmThreshold(",
			endFunc:   "func parseNonNegativeInt(",
			action:    `"UPDATE_MINOR_REALM_THRESHOLD"`,
			missed:    "小境界门槛更新未命中",
			unsafe:    `Update("required_hours", requiredHours).Error`,
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
		txStart := strings.Index(block, "err := DB.Transaction(func(tx *gorm.DB) error {")
		if txStart < 0 {
			t.Fatalf("%s transaction missing", tt.name)
		}
		txEnd := strings.Index(block[txStart:], "\n\t})\n\n\tif err != nil {")
		if txEnd < 0 {
			t.Fatalf("%s transaction boundary missing", tt.name)
		}
		txBlock := block[txStart : txStart+txEnd]
		postTxBlock := block[txStart+txEnd:]
		for _, want := range []string{
			"validateCultivationRuleSet(rules)",
			"writeAuditLogInTx(",
			tt.action,
			"formatPlainValue(reason)",
			"写入修仙配置审计失败",
			"RowsAffected == 0",
			tt.missed,
		} {
			if !strings.Contains(txBlock, want) {
				t.Fatalf("%s transactional audit guard missing %q", tt.name, want)
			}
		}
		if tt.name != "realm threshold" && strings.Contains(txBlock, tt.unsafe) {
			t.Fatalf("%s still checks only update error", tt.name)
		}
		if tt.name == "realm threshold" && strings.Contains(txBlock, "}).Error") {
			t.Fatalf("%s still checks only update error", tt.name)
		}
		if strings.Contains(postTxBlock, "writeAuditLog(") {
			t.Fatalf("%s still writes unchecked audit after transaction", tt.name)
		}
		reload := strings.Index(postTxBlock, "ReloadCultivationRules()")
		if reload < 0 {
			t.Fatalf("%s post-commit cache reload missing", tt.name)
		}
	}
}

func TestCultivationThresholdAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("cultivation_admin_write_commands.go")
	if err != nil {
		t.Fatalf("read cultivation_admin_write_commands.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainValue(realmName), major, minHours, maxHours",
		"major, minor, formatPlainValue(minorName), requiredHours",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cultivation threshold audit detail guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`fmt.Sprintf("境界 %s(%d) 门槛调整为 %.1f - %.1f 小时", realmName`,
		`fmt.Sprintf("小境界 %d-%d(%s) 门槛调整为 %.1f 小时", major, minor, minorName`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("cultivation threshold audit detail uses raw value: %s", unsafe)
		}
	}
}

func TestCultivationAdminPlainRepliesUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("cultivation_admin_write_commands.go")
	if err != nil {
		t.Fatalf("read cultivation_admin_write_commands.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"formatPlainValue(normalizedText)",
		"formatPlainValue(pendingCommand)",
		"formatPlainValue(reason)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cultivation admin plain reply display guard missing %q", want)
		}
	}

	realmStart := strings.Index(text, "func updateRealmThreshold(")
	if realmStart < 0 {
		t.Fatal("updateRealmThreshold missing")
	}
	realmEnd := strings.Index(text[realmStart:], "func updateMinorRealmThreshold(")
	if realmEnd < 0 {
		t.Fatal("updateRealmThreshold boundary missing")
	}
	realmBlock := text[realmStart : realmStart+realmEnd]
	if !strings.Contains(realmBlock, "formatPlainValue(realmName), minHours, maxHours") {
		t.Fatal("realm threshold success reply should display sanitized realm name")
	}
	if strings.Contains(realmBlock, "realmName, minHours, maxHours") {
		t.Fatal("realm threshold success reply still displays raw realm name")
	}

	minorStart := strings.Index(text, "func updateMinorRealmThreshold(")
	if minorStart < 0 {
		t.Fatal("updateMinorRealmThreshold missing")
	}
	minorEnd := strings.Index(text[minorStart:], "func parseNonNegativeInt(")
	if minorEnd < 0 {
		t.Fatal("updateMinorRealmThreshold boundary missing")
	}
	minorBlock := text[minorStart : minorStart+minorEnd]
	if !strings.Contains(minorBlock, "major, minor, formatPlainValue(minorName), requiredHours") {
		t.Fatal("minor realm threshold success reply should display sanitized minor name")
	}
	if strings.Contains(minorBlock, "major, minor, minorName, requiredHours") {
		t.Fatal("minor realm threshold success reply still displays raw minor name")
	}
}

func TestDeleteLocalUserWithAuditChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("admin_mutations.go")
	if err != nil {
		t.Fatalf("read admin_mutations.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func deleteLocalUserWithAudit(")
	if start < 0 {
		t.Fatal("deleteLocalUserWithAudit missing")
	}
	end := strings.Index(text[start:], "func promoteAdminWithAudit(")
	if end < 0 {
		t.Fatal("deleteLocalUserWithAudit boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"deleteRes := tx.Unscoped()",
		`Where("id = ? AND telegram_id = ? AND abs_user_id = ? AND role <> ?", target.ID, targetID, expectedAbsUserID, "super_admin")`,
		"deleteRes.RowsAffected == 0",
		`fmt.Errorf("target_state_changed")`,
		"writeAuditLogInTx(tx, actorID, action",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("delete local user rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `tx.Unscoped().`+`Delete(&target).Error`) {
		t.Fatal("deleteLocalUserWithAudit still deletes without checking RowsAffected")
	}
}

func TestSecurityAttemptUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	createStart := strings.Index(text, "func recordSecurityAttemptFailureInTx(")
	if createStart < 0 {
		t.Fatal("recordSecurityAttemptFailureInTx missing")
	}
	createEnd := strings.Index(text[createStart:], "func updateSecurityAttemptFailureInTx(")
	if createEnd < 0 {
		t.Fatal("recordSecurityAttemptFailureInTx boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		"res := tx.Create(&create)",
		"isUniqueConstraintError(res.Error)",
		"res.RowsAffected == 0",
		"SECURITY_ATTEMPT_CREATE_MISSED",
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("security attempt create guard missing %q", want)
		}
	}
	if strings.Contains(createBlock, "tx.Create(&create).Error") {
		t.Fatal("security attempt create still checks only create error")
	}

	updateStart := strings.Index(text, "func updateSecurityAttemptFailureInTx(")
	if updateStart < 0 {
		t.Fatal("updateSecurityAttemptFailureInTx missing")
	}
	updateEnd := strings.Index(text[updateStart:], "func verifyUserSecurityCodeWithCooldown(")
	if updateEnd < 0 {
		t.Fatal("updateSecurityAttemptFailureInTx boundary missing")
	}
	updateBlock := text[updateStart : updateStart+updateEnd]
	for _, want := range []string{
		"res := tx.Model(&SecurityAttemptLock{})",
		`Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose)`,
		"res.RowsAffected == 0",
		"SECURITY_ATTEMPT_STATE_CHANGED",
	} {
		if !strings.Contains(updateBlock, want) {
			t.Fatalf("security attempt failure update guard missing %q", want)
		}
	}
	if strings.Contains(updateBlock, "tx.Model(attempt).Updates(") {
		t.Fatal("security attempt failure update still uses unchecked model snapshot")
	}

	verifyStart := strings.Index(text, "func verifySensitiveTokenWithPersistentCooldown(")
	if verifyStart < 0 {
		t.Fatal("verifySensitiveTokenWithPersistentCooldown missing")
	}
	verifyEnd := strings.Index(text[verifyStart:], "func escapeMarkdown(")
	if verifyEnd < 0 {
		t.Fatal("verifySensitiveTokenWithPersistentCooldown boundary missing")
	}
	verifyBlock := text[verifyStart : verifyStart+verifyEnd]
	for _, want := range []string{
		`Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose)`,
		"res.RowsAffected == 0",
		"formatPlainError(err)",
	} {
		if !strings.Contains(verifyBlock, want) {
			t.Fatalf("security attempt reset guard missing %q", want)
		}
	}
	if strings.Contains(verifyBlock, "tx.Model(&attempt).Updates(") ||
		strings.Contains(verifyBlock, `err=%v", userID, formatPlainValue(purpose), err`) {
		t.Fatal("security attempt reset/logging still uses unsafe pattern")
	}
}

func TestSecurityAttemptLockMigrationReplacesFullUniqueIndex(t *testing.T) {
	source, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runSecurityAttemptLockMigration()")
	if start < 0 {
		t.Fatal("runSecurityAttemptLockMigration missing")
	}
	end := strings.Index(text[start:], "func migrateUserSecurityCodesInBatches() error")
	if end < 0 {
		t.Fatal("runSecurityAttemptLockMigration boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`runOneTimeMigration("20260623_security_attempt_lock_partial_unique_index"`,
		`assertNoDuplicateGroups("security_attempt_locks(user_id, purpose)"`,
		"WHERE deleted_at IS NULL",
		"ensureSecurityAttemptLockPartialUniqueIndex(DB)",
		"security attempt lock unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("security attempt lock migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSecurityAttemptLockPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSecurityAttemptLockPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenPlotPartialUniqueIndexes(")
	if helperEnd < 0 {
		t.Fatal("security attempt lock partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_security_attempt_locks_user_purpose_unique",
		"ON security_attempt_locks(user_id, purpose)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("security attempt lock partial index helper missing %q", want)
		}
	}
}
