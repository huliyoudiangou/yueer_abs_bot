package main

import (
	"os"
	"strings"
	"testing"
)

func TestUsersAbsUserIDMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("users(abs_user_id)"`)
	if start < 0 {
		t.Fatal("users abs_user_id migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_daily_task_claims(user_id, day_key)"`)
	if end < 0 {
		t.Fatal("users abs_user_id migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM users",
		"WHERE abs_user_id <> '' AND deleted_at IS NULL",
		"ensureUsersAbsUserIDPartialUniqueIndex(DB)",
		"users abs_user_id unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("users abs_user_id migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureUsersAbsUserIDPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureUsersAbsUserIDPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenSeedPurchasePartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("users abs_user_id partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_users_abs_user_id_unique",
		"ON users(abs_user_id)",
		"WHERE abs_user_id <> '' AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("users abs_user_id partial index helper missing %q", want)
		}
	}
}

func TestBindLocalUserCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("account_binding.go")
	if err != nil {
		t.Fatalf("read account_binding.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func createBoundLocalUserInTx(")
	if helperStart < 0 {
		t.Fatal("createBoundLocalUserInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func rebindLocalUserWithAudit(")
	if helperEnd < 0 {
		t.Fatal("createBoundLocalUserInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		`"BOUND_USER_CREATE_MISSED"`,
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("bound local user create guard missing %q", want)
		}
	}

	bindStart := strings.Index(text, "func bindLocalUserWithAudit(")
	if bindStart < 0 {
		t.Fatal("bindLocalUserWithAudit missing")
	}
	bindEnd := strings.Index(text[bindStart:], "func createBoundLocalUserInTx(")
	if bindEnd < 0 {
		t.Fatal("bindLocalUserWithAudit boundary missing")
	}
	bindBlock := text[bindStart : bindStart+bindEnd]
	if !strings.Contains(bindBlock, "createBoundLocalUserInTx(tx, &user)") {
		t.Fatal("bindLocalUserWithAudit does not use createBoundLocalUserInTx")
	}
	if strings.Contains(bindBlock, "tx.Create(&User{") {
		t.Fatal("bindLocalUserWithAudit still creates User directly")
	}
}

func TestRebindLocalUserWithAuditChecksOldTelegramID(t *testing.T) {
	source, err := os.ReadFile("account_binding.go")
	if err != nil {
		t.Fatalf("read account_binding.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func rebindLocalUserWithAudit(")
	if start < 0 {
		t.Fatal("rebindLocalUserWithAudit missing")
	}
	end := strings.Index(text[start:], "func unbindLocalUserWithAudit(")
	if end < 0 {
		t.Fatal("rebindLocalUserWithAudit boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"oldTelegramID := target.TelegramID",
		`Where("id = ? AND telegram_id = ? AND abs_user_id = ?", target.ID, oldTelegramID, expectedAbsUserID)`,
		"res.RowsAffected == 0",
		`writeAuditLogInTx(`,
		`"REBIND_USER"`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("rebind local user guard missing %q", want)
		}
	}
	if strings.Contains(block, `Where("id = ? AND abs_user_id = ?", target.ID, expectedAbsUserID)`) {
		t.Fatal("rebind local user still updates without checking old telegram_id")
	}
}

func TestAccountBindingAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("account_binding.go")
	if err != nil {
		t.Fatalf("read account_binding.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"formatPlainValue(oldUsername)",
		"formatPlainValue(oldAbsUserID)",
		"formatPlainValue(username)",
		"formatPlainValue(absUserID)",
		"formatPlainValue(target.Username)",
		"formatPlainValue(target.AbsUserID)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("account binding audit detail missing sanitized value %q", want)
		}
	}
	for _, unsafe := range []string{
		"oldUsername, oldAbsUserID, username, absUserID",
		"username, absUserID, expireAt",
		"target.Username, target.AbsUserID, oldTelegramID",
		"target.Username, target.AbsUserID, actorID",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("account binding audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}

func TestRenameLocalUsernameWithAuditGuards(t *testing.T) {
	source, err := os.ReadFile("account_binding.go")
	if err != nil {
		t.Fatalf("read account_binding.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func renameLocalUsernameWithAudit(")
	if start < 0 {
		t.Fatal("renameLocalUsernameWithAudit missing")
	}
	end := strings.Index(text[start:], "func createBoundLocalUserInTx(")
	if end < 0 {
		t.Fatal("renameLocalUsernameWithAudit boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"DB.Transaction(",
		`Where("username = ? AND telegram_id <> ?", newUsername, actorID)`, // 唯一性二次校验，排除自身
		"return errUsernameTaken",
		"return errUsernameUnchanged",
		`Where("id = ? AND telegram_id = ? AND username = ?", target.ID, actorID, target.Username)`, // CAS 更新
		"res.RowsAffected == 0",
		"isUniqueConstraintError(res.Error)",
		`"RENAME_USERNAME"`,
		"formatPlainValue(oldUsername)",
		"formatPlainValue(newUsername)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("renameLocalUsernameWithAudit guard missing %q", want)
		}
	}
}

func TestUpdateAbsUsernameDoesNotTouchPassword(t *testing.T) {
	source, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func (c *AbsClient) UpdateAbsUsername(")
	if start < 0 {
		t.Fatal("UpdateAbsUsername missing")
	}
	end := strings.Index(text[start:], "func (c *AbsClient) DeleteUser(")
	if end < 0 {
		t.Fatal("UpdateAbsUsername boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`"username":            newUsername`,
		"PATCH",
		"absUserPath(absUserID)",
		"AbsAPIError",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("UpdateAbsUsername missing %q", want)
		}
	}
	// 改名时绝不能携带 password 字段，否则会把用户密码覆盖成空/错误值。
	if strings.Contains(block, `"password"`) {
		t.Fatal("UpdateAbsUsername must not send a password field in the payload")
	}
}

func TestUsernameChangeFlowRequiresPinAndPassword(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	// 三段式流程必须串联：安全码 -> 密码 -> 新用户名。
	authIdx := strings.Index(text, `case "WAITING_USERNAME_AUTH":`)
	pwIdx := strings.Index(text, `case "WAITING_USERNAME_PASSWORD":`)
	newIdx := strings.Index(text, `case "WAITING_NEW_USERNAME":`)
	if authIdx < 0 || pwIdx < 0 || newIdx < 0 {
		t.Fatal("username change flow is missing one of the WAITING_USERNAME_* steps")
	}

	authBlock := text[authIdx:pwIdx]
	if !strings.Contains(authBlock, "verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode)") {
		t.Fatal("WAITING_USERNAME_AUTH must verify the security code (PIN)")
	}
	if !strings.Contains(authBlock, `session.SetStep("WAITING_USERNAME_PASSWORD")`) {
		t.Fatal("WAITING_USERNAME_AUTH must advance to password verification")
	}

	pwBlock := text[pwIdx:newIdx]
	if !strings.Contains(pwBlock, "absClient.VerifyUser(u.Username, text)") {
		t.Fatal("WAITING_USERNAME_PASSWORD must verify the current password via VerifyUser")
	}
	if !strings.Contains(pwBlock, `session.SetStep("WAITING_NEW_USERNAME")`) {
		t.Fatal("WAITING_USERNAME_PASSWORD must advance to new username entry")
	}

	newBlock := text[newIdx : strings.Index(text[newIdx:], `case "WAITING_DELETE_AUTH":`)+newIdx]
	for _, want := range []string{
		"^[a-zA-Z0-9_]{3,20}$",                                  // 用户名格式校验
		"absClient.UpdateAbsUsername(u.AbsUserID",               // 先改服务端
		"renameLocalUsernameWithAudit(userID",                   // 再改本地
		"absClient.UpdateAbsUsername(u.AbsUserID, oldUsername)", // 本地失败时回滚 ABS
		"errUsernameTaken",
	} {
		if !strings.Contains(newBlock, want) {
			t.Fatalf("WAITING_NEW_USERNAME flow missing %q", want)
		}
	}

	// 入口与菜单连线。
	if !strings.Contains(text, `session.SetStep("WAITING_USERNAME_AUTH")`) {
		t.Fatal("username change entry must set WAITING_USERNAME_AUTH step")
	}
}
