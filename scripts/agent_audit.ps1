param(
    [string]$Root = ".",
    [string]$Out = "",
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$goFiles = Get-ChildItem -LiteralPath $rootPath -Filter "*.go" -File

$findings = New-Object System.Collections.Generic.List[object]

function Add-Finding {
    param(
        [string]$Severity,
        [string]$Rule,
        [string]$File,
        [int]$Line,
        [string]$Message,
        [string]$Evidence
    )

    $script:findings.Add([pscustomobject]@{
        severity = $Severity
        rule     = $Rule
        file     = $File
        line     = $Line
        message  = $Message
        evidence = $Evidence.Trim()
    })
}

function Get-RelPath {
    param([string]$Path)
    $full = (Resolve-Path -LiteralPath $Path).Path
    if ($full.StartsWith($rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $full.Substring($rootPath.Length).TrimStart('\', '/')
    }
    return $full
}

function Has-Nearby {
    param(
        [string[]]$Lines,
        [int]$Index,
        [string[]]$Patterns,
        [int]$Before = 18,
        [int]$After = 25
    )

    $start = [Math]::Max(0, $Index - $Before)
    $end = [Math]::Min($Lines.Length - 1, $Index + $After)
    $window = ($Lines[$start..$end] -join "`n")
    foreach ($pattern in $Patterns) {
        if ($window -match $pattern) {
            return $true
        }
    }
    return $false
}

function Get-CodeWindow {
    param(
        [string[]]$Lines,
        [int]$Index,
        [int]$Before = 8,
        [int]$After = 35
    )

    $start = [Math]::Max(0, $Index - $Before)
    $end = [Math]::Min($Lines.Length - 1, $Index + $After)
    return ($Lines[$start..$end] -join "`n")
}

function Get-CallExpression {
    param(
        [string[]]$Lines,
        [int]$Index
    )

    $parts = New-Object System.Collections.Generic.List[string]
    $balance = 0
    $started = $false

    for ($j = $Index; $j -lt $Lines.Length; $j++) {
        $current = $Lines[$j]
        $parts.Add($current)
        foreach ($ch in $current.ToCharArray()) {
            if ($ch -eq '(') {
                $balance++
                $started = $true
            } elseif ($ch -eq ')') {
                $balance--
            }
        }

        if ($started -and $balance -le 0) {
            break
        }
    }

    return ($parts.ToArray() -join "`n")
}

$businessErrorSentinels = @(
    "LOTTERY_NOT_ACTIVE",
    "LOTTERY_WAITING_DRAW",
    "LOTTERY_FULL",
    "ALREADY_JOINED",
    "LOTTERY_CLAIM_EXPIRED",
    "MARKETPLACE_CLOSE_NOT_FOUND",
    "REALM_NOT_ACTIVE",
    "REALM_ALREADY_ACTIVE",
    "REALM_WEEKLY_LIMIT",
    "TECH_LEVEL_CHANGED",
    "POINTS_NOT_ENOUGH",
    "INSUFFICIENT_POINTS",
    "ALREADY_GRABBED",
    "CONCURRENT_REDPACKET_GRAB_RETRY",
    "ALREADY_BET",
    "USAGE_LIMIT_REACHED",
    "ITEM_NOT_ENOUGH",
    "USER_NOT_FOUND",
    "SECURITY_PEPPER_NOT_CONFIGURED",
    "ALREADY_SIGNED",
    "SIGN_DATE_IN_FUTURE",
    "CONCURRENT_SIGN_IN_RETRY",
    "INVALID_INVITE_CODE",
    "INVALID_RENEW_CODE",
    "TELEGRAM_USER_MISSING",
    "ABS_USER_ID_EMPTY",
    "ABS_REFRESH_FAILED",
    "ABS_REFRESH_FAILED_USING_CACHE",
    "target_is_super_admin",
    "adjust_no_effect",
    "daily_adjust_limit_exceeded",
    "ALREADY_IN_SECT",
    "SECT_NAME_EXISTS",
    "SECT_NOT_FOUND",
    "SECT_FULL",
    "NOT_IN_SECT",
    "TARGET_NOT_IN_SECT",
    "NO_PERMISSION",
    "SAME_NAME",
    "FUNDS_NOT_ENOUGH",
    "ONLY_OWNER",
    "MAX_LEVEL",
    "PRESTIGE_NOT_ENOUGH",
    "RESOURCE_NOT_ENOUGH",
    "CANNOT_APPOINT_OWNER",
    "CULTIVATION_NOT_FOUND",
    "MAX_REALM_REACHED",
    "CONSOLIDATING",
    "NOT_READY",
    "INSUFFICIENT_CULTIVATION",
    "NO_PILL",
    "INVALID_BREAKTHROUGH_MODE",
    "CULTIVATION_STATE_CHANGED",
    "RANDOM_FAILED",
    "INVALID_MARKETPLACE_LISTING",
    "MARKETPLACE_DUPLICATE_SECRET",
    "MARKETPLACE_INVENTORY_NOT_ENOUGH",
    "MARKETPLACE_LISTING_NOT_FOUND",
    "MARKETPLACE_SELF_BUY",
    "MARKETPLACE_OUT_OF_STOCK",
    "MARKETPLACE_QUANTITY_TOO_LARGE",
    "MARKETPLACE_INVALID_PRICE",
    "MARKETPLACE_INVALID_TYPE",
    "MARKETPLACE_REALM_TOO_LOW",
    "SECT_HORN_INVALID_SCOPE",
    "SECT_HORN_NO_RECIPIENTS",
    "SECT_HORN_CONTENT_EMPTY",
    "SECT_HORN_CONTENT_SHORT",
    "SECT_HORN_CONTENT_LONG",
    "SECT_HORN_CONTROL_CHAR",
    "SECT_HORN_LINK_BLOCKED",
    "SECT_HORN_COOLDOWN",
    "CREATE_INVITE_CODE_FAILED",
    "CREATE_RENEW_CODE_FAILED",
    "CREATE_REDPACKET_FAILED",
    "GARDEN_PLOT_MAX",
    "GARDEN_DAILY_LIMIT",
    "GARDEN_SEED_NOT_AVAILABLE",
    "GARDEN_SEED_UNKNOWN",
    "GARDEN_PLOT_NOT_FOUND",
    "GARDEN_PLOT_BUSY",
    "GARDEN_SEED_NOT_ENOUGH",
    "GARDEN_NO_ACTIVE_PLANT",
    "GARDEN_NOT_MATURE",
    "GARDEN_ALREADY_HARVESTED",
    "GARDEN_NO_MATURE_PLANT",
    "GARDEN_HERB_NOT_SELLABLE",
    "GARDEN_HERB_NOT_ENOUGH",
    "GARDEN_HERB_QUANTITY_INVALID",
    "GARDEN_RECIPE_UNKNOWN",
    "GARDEN_RECIPE_UNLOCKED",
    "GARDEN_RECIPE_LOCKED",
    "GARDEN_MATERIAL_NOT_ENOUGH"
)
$businessErrorPattern = ($businessErrorSentinels | ForEach-Object { [regex]::Escape($_) }) -join "|"
$businessErrorComparisonPattern = '(?:err\.Error\(\)\s*(?:==|!=)\s*"(' + $businessErrorPattern + ')"|"(' + $businessErrorPattern + ')"\s*(?:==|!=)\s*err\.Error\(\)|strings\.HasPrefix\(err\.Error\(\),\s*"daily_adjust_limit_exceeded:)'
$businessErrorStringReturnPattern = 'fmt\.Errorf\("(?:' + $businessErrorPattern + ')"\)'
$businessErrorCodeRawReturnPattern = 'func\s+\w*ErrorCode\s*\(\s*err\s+error\s*\)\s+string\s*\{(?:(?!\nfunc\s)[\s\S])*?return\s+err\.Error\(\)'
$securityAttemptDirectCreatePattern = 'Create\(&SecurityAttemptLock\s*\{'
$markdownSecretDeliveryPattern = '(?:result\.Codes|strings\.Join\([^)]*\bCodes\b|plainCode\b|decryptMarketplaceSecret\()'
$marketplacePointDescriptionRawPattern = 'applyPointDeltaInTx\([\s\S]*?"marketplace_(?:buy|sell)"\s*,\s*fmt\.Sprintf\([\s\S]*?listing\.Name'
$marketplaceSecretListingNameStalePromptPattern = '\u5546\u54c1\u540d\u79f0.{0,80}2-40 \u4e2a\u5b57(?![\s\S]{0,120}\u63a7\u5236\u5b57\u7b26)'
$marketplaceInventoryItemNameStalePromptPattern = '\u7269\u54c1\u540d\u79f0.{0,80}1-40 \u4e2a\u5b57(?![\s\S]{0,120}\u63a7\u5236\u5b57\u7b26)'
$telegramUsernameDirectMentionPattern = '(?:"@"\s*\+\s*(?:[A-Za-z_][A-Za-z0-9_]*\.){0,3}UserName\b|fmt\.Sprintf\(\s*"@%s"\s*,\s*(?:[A-Za-z_][A-Za-z0-9_]*\.){0,3}UserName\s*\))'
$telegramErrorStringPattern = 'strings\.Contains\([^)]*(?:err\.Error\(\)|strings\.ToLower\(err\.Error\(\)\)|errText)[^)]*,\s*"(?:message is not modified|message to delete not found|message can''t be deleted|not enough rights(?: to delete messages)?)"'
$lotteryTitleLengthOnlyPattern = 'case\s+"WAITING_LOTTERY_TITLE"[\s\S]{0,240}?len\(\[\]rune\(text\)\)'
$lotteryClaimCodeLengthOnlyPattern = 'case\s+"WAITING_LOTTERY_CLAIM_CODE"[\s\S]{0,240}?len\(\[\]rune\(text\)\)'
$lotteryTitleStalePromptPattern = '\u6d3b\u52a8\u540d\u79f0.{0,80}2-60 \u4e2a\u5b57(?![\s\S]{0,120}\u63a7\u5236\u5b57\u7b26)'
$lotteryClaimCodeStalePromptPattern = '\u9886\u5956\u6697\u53f7.{0,80}3-40 \u4e2a\u5b57(?![\s\S]{0,120}\u63a7\u5236\u5b57\u7b26)'
$adminReasonFunctionPattern = 'func\s+validateAdminReason\s*\(\s*text\s+string\s*\)\s*\(\s*string\s*,\s*bool\s*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$adminReasonStalePromptPattern = '\u539f\u56e0.{0,80}\u81f3\u5c11(?:\u8f93\u5165)? 5 \u4e2a\u5b57'
$sectNameFunctionPattern = 'func\s+validateSectName\s*\(\s*name\s+string\s*\)\s*\(\s*string\s*,\s*bool\s*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$sectNameStalePromptPattern = '\u5b97\u95e8\u540d\u683c\u5f0f\u9519\u8bef[\s\S]{0,120}\u4e0d\u80fd\u5305\u542b\u7a7a\u683c\u548c Markdown \u7279\u6b8a\u7b26\u53f7'
$xmlyLinkFunctionPattern = 'func\s+validateXmlyLink\s*\(\s*raw\s+string\s*\)\s*\(\s*string\s*,\s*bool\s*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$xmlyLinkStalePromptPattern = '(?:sendPlainText|replyText)\s*\([\s\S]{0,500}\u559c\u9a6c\u62c9\u96c5[\s\S]{0,500}https://[\s\S]{0,500}ximalaya\.com[\s\S]{0,500}xima\.tv[\s\S]{0,500}\)'
$bookRequestNoteFunctionPattern = 'func\s+validateBookRequestNote\s*\(\s*raw\s+string\s*,\s*allowEmpty\s+bool\s*\)\s*\(\s*string\s*,\s*bool\s*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$serverLinesFunctionPattern = 'func\s+validateServerLinesContent\s*\(\s*raw\s+string\s*\)\s*\(\s*string\s*,\s*bool\s*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$serverLinesUpsertPattern = 'upsertSystemConfigValue\(\s*"server_lines"\s*,'
$auditStorageFunctionPattern = 'func\s+formatAuditTextForStorage\s*\(\s*text\s+string\s*,\s*maxRunes\s+int\s*\)\s+string\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$auditDisplayRawRedactPattern = 'redactSensitiveAuditText\(\s*item\.(?:Target|Detail)\s*\)'
$diagnosticFormatterFunctionPattern = 'func\s+format(?:MarkdownError|PlainValue)\s*\([^)]*\)\s+string\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$systemConfigErrorDisplayPattern = 'escapeMarkdown\s*\(\s*(?:truncateRunes\s*\(\s*)?\b(?:lastError|pinError|refreshError|backupError|dailyRefreshError)\b'
$callbackFormatterFunctionPattern = 'func\s+formatCallbackAlertText\s*\([^)]*\)\s+string\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$answerCallbackFunctionPattern = 'func\s+answerCallback\s*\([^)]*\)\s*\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$telegramSendHelperFunctionPattern = 'func\s+(?:sendPlainText|replyText|sendMenu|sendMenuPanel|editMenuPanel|sendPlainTextNoMarkdown|sendLongPlainText|sendLotteryReplyPlainText|answerCallback|sendGardenScreen|editGardenScreen|notifySuperAdminsPlain|sendAndManageLeaderboardPin|ensureWorldBossLiveBoard|pinLatestBackupMessage|announceLotteryCreated|pinLotteryMessage|unpinLotteryMessage)\s*\([^)]*\)\s*(?:bool\s*)?\{(?<body>(?:(?!\nfunc\s)[\s\S])*?)\n\}'
$telegramSendHelperRawErrLogPattern = 'log\.Printf\([^\r\n]*(?:err=%v|: %v)[^\r\n]*,\s*(?:[^\r\n,]*,\s*)?err\s*\)'
$cultivationIgnoredSendPattern = '^\s*sendAutoDelete\(bot,\s*(?:tgbotapi\.NewMessage\(|msg\))'
$stateMachineUserVisibleIgnoredSendPattern = '^\s*(?:sendAutoDelete\(bot,\s*(?:directMsg|tgbotapi\.NewMessage\((?:userID,\s*msg|chatID,\s*reply)\))|processingMsg,\s*_\s*:=\s*sendAutoDelete\(bot,|bot\.Request\(editMsg\))'
$absRawErrorLogPattern = 'log\.Printf\([^\r\n]{0,240}(?i:abs)[^\r\n]{0,240}(?:err=%v|: %v)[^\r\n]{0,240},\s*err\s*\)'
$visibleRawErrorFormatPattern = 'fmt\.Sprintf\([\s\S]{0,240}%v[\s\S]{0,240},[\s\S]{0,120}\b(?:err|apiErr|dbErr|rollbackErr|sendErr|res\.Error|walletErr)\b'
$plainFormattedErrorPercentVPattern = 'fmt\.Sprintf\([\s\S]{0,240}%v[\s\S]{0,240},[\s\S]{0,120}(?:formatPlainError|formatPlainValue)\('
$fmtErrorfWrapPercentVPattern = 'fmt\.Errorf\([^\r\n]*%v[^\r\n]*,\s*err\s*\)'
$auditRawErrorDetailPattern = 'writeAuditLog(?:WithDelta)?\([^\r\n]*fmt\.Sprintf\([^\r\n]*%v[^\r\n]*,[^\r\n]*\b(?:err|apiErr|dbErr|rollbackErr)\b[^\r\n]*\)'
$dynamicLogFieldTokenPattern = '\b(?:item|name|new_name|reason|abs|user|key|value|url|code_hash|purchase_key|mode|role|status|purpose|realm|boss|race_id|dice_id|title|version|sql)=%s'
$dynamicLogFieldRawPattern = 'log\.(?:Printf|Fatalf)\([\s\S]{0,500}' + $dynamicLogFieldTokenPattern + '[\s\S]{0,500}'
$garbledPlaceholderTextPattern = '(?:\?{2,}|\ufffd|\u951f|\u93ae|\u9422|\u6d93)[\?\s\ufffd\u4e00-\u9fff]{6,}'
$bookRequestUserReplyLogPattern = 'createBookRequestLog\([\s\S]{0,300}"user_reply"'
$bookRequestFinishLogPattern = 'createBookRequestLog\([\s\S]{0,300}"finish"'
$bookRequestNeedInfoLogPattern = 'createBookRequestLog\([\s\S]{0,300}"need_info"'
$bookRequestAdminNoteLogPattern = 'createBookRequestLog\([\s\S]{0,300}"admin_note"'
$inventoryItemNameRawValuePattern = '\b(?:item|inventoryItem|inventory|inv)\.ItemName\b|\b(?:item|treasureShopItem|shopItem)\.Name\b|\breq\.PillName\b|\b(?:itemName|pillName)\b'
$inventoryItemNameSafeCallPattern = 'inventoryItemMarkdownName\s*\(\s*(?:' + $inventoryItemNameRawValuePattern + ')\s*\)'
$markdownVisibleSendPattern = '^\s*(?:replyText|sendMenuPanel|editMenuPanel|sendMenu|sendAutoDelete|sendGroupAutoDeleteMessage)\s*\('
$gardenItemNameRawValuePattern = '\bcfg\.(?:SeedName|HerbName|ProductName|Name)\b|\bmat\.ItemName\b|\bplanting\.HerbName\b|\boffer\.HerbName\b'
$gardenItemNameSafeCallPattern = '(?:inventoryItemMarkdownName|escapeMarkdown)\s*\(\s*(?:' + $gardenItemNameRawValuePattern + ')\s*\)'
$gardenMarkdownTextLinePattern = '^\s*(?:b\.WriteString\(fmt\.Sprintf|replyText)\s*\('

function Has-RawInventoryItemNameInMarkdownCall {
    param([string]$CallText)

    if ($CallText -notmatch $markdownVisibleSendPattern) {
        return $false
    }

    $withoutSafeItemNames = [regex]::Replace($CallText, $inventoryItemNameSafeCallPattern, "inventoryItemMarkdownName(SAFE_ITEM_NAME)")
    return $withoutSafeItemNames -match $inventoryItemNameRawValuePattern
}

function Has-RawGardenItemNameInMarkdownLine {
    param([string]$Line)

    if ($Line -notmatch $gardenMarkdownTextLinePattern) {
        return $false
    }

    $withoutSafeItemNames = [regex]::Replace($Line, $gardenItemNameSafeCallPattern, "SAFE_GARDEN_ITEM_NAME")
    $withoutSafeItemNames = [regex]::Replace($withoutSafeItemNames, '\binv\[[^\]\r\n]+\]', "SAFE_GARDEN_INVENTORY_LOOKUP")
    return $withoutSafeItemNames -match $gardenItemNameRawValuePattern
}

function Has-UnformattedDynamicLogField {
    param([string]$CallText)

    $fieldCount = [regex]::Matches($CallText, $dynamicLogFieldTokenPattern).Count
    if ($fieldCount -eq 0) {
        return $false
    }

    $formattedCount = [regex]::Matches($CallText, 'formatPlainValue\s*\(').Count
    return $formattedCount -lt $fieldCount
}

function Test-AgentAuditRuleSelfChecks {
    $seenSentinels = New-Object System.Collections.Generic.HashSet[string]
    foreach ($sentinel in $businessErrorSentinels) {
        if (-not $seenSentinels.Add($sentinel)) {
            Add-Finding "error" "agent-audit-self-test" "scripts/agent_audit.ps1" 0 `
                "Duplicate business error sentinel in agent_audit self-check input." $sentinel
        }
    }

    $cases = @(
        [pscustomobject]@{
            name        = "registered sentinel fmt.Errorf"
            line        = 'return fmt.Errorf("MARKETPLACE_LISTING_NOT_FOUND")'
            pattern     = $businessErrorStringReturnPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "unregistered technical fmt.Errorf"
            line        = 'return fmt.Errorf("NO_PRIZES")'
            pattern     = $businessErrorStringReturnPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "dynamic technical fmt.Errorf"
            line        = 'return fmt.Errorf("ABS_STATUS_%d", code)'
            pattern     = $businessErrorStringReturnPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "direct err.Error sentinel comparison"
            line        = 'if err.Error() == "LOTTERY_NOT_ACTIVE" {'
            pattern     = $businessErrorComparisonPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "reverse err.Error sentinel comparison"
            line        = 'if "INSUFFICIENT_POINTS" != err.Error() {'
            pattern     = $businessErrorComparisonPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "daily adjust prefix comparison"
            line        = 'if strings.HasPrefix(err.Error(), "daily_adjust_limit_exceeded:") {'
            pattern     = $businessErrorComparisonPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "errors.Is sentinel usage"
            line        = 'if errors.Is(err, ErrLotteryNotActive) {'
            pattern     = $businessErrorComparisonPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "error code helper raw err return"
            line        = "func lotteryJoinErrorCode(err error) string {`nreturn err.Error()`n}"
            pattern     = $businessErrorCodeRawReturnPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "error code helper fallback whitelist"
            line        = "func lotteryJoinErrorCode(err error) string {`nreturn fallbackBusinessErrorCode(err)`n}"
            pattern     = $businessErrorCodeRawReturnPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "security attempt direct create"
            line        = 'return tx.Create(&SecurityAttemptLock{UserID: userID}).Error'
            pattern     = $securityAttemptDirectCreatePattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "security attempt helper"
            line        = 'return recordSecurityAttemptFailureInTx(tx, userID, purpose, max, duration, now, remainingFormat, lockMessage)'
            pattern     = $securityAttemptDirectCreatePattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "card secret delivery through generic visible send"
            line        = 'sendPlainText(bot, chatID, fmt.Sprintf("卡密：%s", strings.Join(result.Codes, "\n")))'
            pattern     = $markdownSecretDeliveryPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "card secret delivery through plain-text send"
            line        = 'sendPlainTextNoMarkdown(bot, chatID, fmt.Sprintf("卡密：%s", strings.Join(result.Codes, "\n")))'
            pattern     = '^(?!.*sendPlainTextNoMarkdown\().*(?:sendPlainText|replyText|answerCallback)\([\s\S]*' + $markdownSecretDeliveryPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "marketplace point description raw listing name"
            line        = 'applyPointDeltaInTx(tx, buyerID, -grossAmount, "marketplace_buy", fmt.Sprintf("market buy %s", listing.Name), "marketplace", fmt.Sprintf("%d", listing.ID))'
            pattern     = $marketplacePointDescriptionRawPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "marketplace point description helper"
            line        = 'applyPointDeltaInTx(tx, buyerID, -grossAmount, "marketplace_buy", marketplaceBuyPointDescription(listing.Name, buyQty), "marketplace", fmt.Sprintf("%d", listing.ID))'
            pattern     = $marketplacePointDescriptionRawPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "marketplace secret listing stale length-only prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u81ea\u7531\u4e0a\u67b6\uff1a\u8bf7\u53d1\u9001\u5546\u54c1\u540d\u79f0\uff0c2-40 \u4e2a\u5b57\u3002")')
            pattern     = $marketplaceSecretListingNameStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "marketplace secret listing complete requirement prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u81ea\u7531\u4e0a\u67b6\uff1a\u8bf7\u53d1\u9001\u5546\u54c1\u540d\u79f0\uff0c"+marketplaceSecretListingNameRequirementText+"\u3002")')
            pattern     = $marketplaceSecretListingNameStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "marketplace inventory item stale length-only prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u7269\u54c1\u540d\u79f0\u9700\u4e3a 1-40 \u4e2a\u5b57\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $marketplaceInventoryItemNameStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "marketplace inventory item complete requirement prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u7269\u54c1\u540d\u79f0\u9700\u4e3a "+marketplaceInventoryItemNameRequirementText+"\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $marketplaceInventoryItemNameStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "telegram username direct mention concat"
            line        = 'displayName = "@" + chat.UserName'
            pattern     = $telegramUsernameDirectMentionPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "telegram username direct mention sprintf"
            line        = 'displayName = fmt.Sprintf("@%s", msg.From.UserName)'
            pattern     = $telegramUsernameDirectMentionPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "telegram username mention helper"
            line        = 'displayName = telegramUsernameMentionMarkdown(chat.UserName)'
            pattern     = $telegramUsernameDirectMentionPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "telegram error direct message-not-modified string check"
            line        = 'if strings.Contains(strings.ToLower(err.Error()), "message is not modified") {'
            pattern     = $telegramErrorStringPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "telegram error direct delete string check"
            line        = 'if strings.Contains(errText, "message to delete not found") {'
            pattern     = $telegramErrorStringPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "telegram error helper usage"
            line        = 'if isTelegramMessageNotModifiedError(err) {'
            pattern     = $telegramErrorStringPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "lottery title length-only validation"
            line        = "case `"WAITING_LOTTERY_TITLE`":`nif l := len([]rune(text)); l < 2 || l > 60 {"
            pattern     = $lotteryTitleLengthOnlyPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "lottery title helper validation"
            line        = "case `"WAITING_LOTTERY_TITLE`":`nif !validLotteryTitle(text) {"
            pattern     = $lotteryTitleLengthOnlyPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "lottery claim code length-only validation"
            line        = "case `"WAITING_LOTTERY_CLAIM_CODE`":`nif l := len([]rune(text)); l < 3 || l > 40 {"
            pattern     = $lotteryClaimCodeLengthOnlyPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "lottery claim code helper validation"
            line        = "case `"WAITING_LOTTERY_CLAIM_CODE`":`nif !validLotteryClaimCode(text) {"
            pattern     = $lotteryClaimCodeLengthOnlyPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "lottery title stale length-only prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u6d3b\u52a8\u540d\u79f0\u957f\u5ea6\u9700\u4e3a 2-60 \u4e2a\u5b57\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $lotteryTitleStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "lottery title complete requirement prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u6d3b\u52a8\u540d\u79f0\u9700\u4e3a "+lotteryTitleRequirementText+"\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $lotteryTitleStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "lottery claim code stale length-only prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u9886\u5956\u6697\u53f7\u957f\u5ea6\u9700\u4e3a 3-40 \u4e2a\u5b57\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $lotteryClaimCodeStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "lottery claim code complete requirement prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u9886\u5956\u6697\u53f7\u9700\u4e3a "+lotteryClaimCodeRequirementText+"\uff0c\u8bf7\u91cd\u65b0\u53d1\u9001\uff1a")')
            pattern     = $lotteryClaimCodeStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "admin reason helper without control guard"
            line        = "func validateAdminReason(text string) (string, bool) {`nreason := strings.TrimSpace(text)`nif len([]rune(reason)) < 5 { return `"\"", false }`nreturn reason, true`n}"
            pattern     = $adminReasonFunctionPattern
            shouldMatch = $true
            customCheck = "admin-reason-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "admin reason helper with control guard"
            line        = "func validateAdminReason(text string) (string, bool) {`nfor _, r := range reason { if unicode.IsControl(r) { return `"\"", false } }`nreturn reason, true`n}"
            pattern     = $adminReasonFunctionPattern
            shouldMatch = $false
            customCheck = "admin-reason-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "admin reason stale short-only failure prompt"
            line        = [regex]::Unescape('replyText(bot, chatID, "\u539f\u56e0\u592a\u77ed\uff0c\u8bf7\u81f3\u5c11\u8f93\u5165 5 \u4e2a\u5b57\uff1a")')
            pattern     = $adminReasonStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "admin reason stale short-only input prompt"
            line        = [regex]::Unescape('replyText(bot, chatID, "\u8bf7\u8f93\u5165\u672c\u6b21\u8c03\u8d26\u539f\u56e0\uff0c\u81f3\u5c11 5 \u4e2a\u5b57\u3002")')
            pattern     = $adminReasonStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "admin reason shared requirement prompt"
            line        = [regex]::Unescape('replyText(bot, chatID, "\u8bf7\u8f93\u5165\u672c\u6b21\u8c03\u8d26\u539f\u56e0\uff0c"+adminReasonRequirementText+"\u3002")')
            pattern     = $adminReasonStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "sect name helper without control guard"
            line        = "func validateSectName(name string) (string, bool) {`nname = strings.TrimSpace(name)`nif len([]rune(name)) < 2 || len([]rune(name)) > 12 { return `"\"", false }`nreturn name, true`n}"
            pattern     = $sectNameFunctionPattern
            shouldMatch = $true
            customCheck = "sect-name-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "sect name helper with control guard"
            line        = "func validateSectName(name string) (string, bool) {`nfor _, r := range name { if unicode.IsControl(r) { return `"\"", false } }`nreturn name, true`n}"
            pattern     = $sectNameFunctionPattern
            shouldMatch = $false
            customCheck = "sect-name-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "sect name stale prompt"
            line        = [regex]::Unescape('replyText(bot, chatID, "\u274c \u5b97\u95e8\u540d\u683c\u5f0f\u9519\u8bef\uff1a\u8bf7\u8f93\u5165 2-12 \u4e2a\u5b57\uff0c\u4e0d\u80fd\u5305\u542b\u7a7a\u683c\u548c Markdown \u7279\u6b8a\u7b26\u53f7\u3002")')
            pattern     = $sectNameStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "sect name shared invalid prompt"
            line        = 'replyText(bot, chatID, sectNameInvalidText)'
            pattern     = $sectNameStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "ximalaya link helper without control guard"
            line        = "func validateXmlyLink(raw string) (string, bool) {`nraw = strings.TrimSpace(raw)`nif strings.ContainsAny(raw, `" \r\n\t`") { return `"\"", false }`nreturn raw, true`n}"
            pattern     = $xmlyLinkFunctionPattern
            shouldMatch = $true
            customCheck = "xmly-link-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "ximalaya link helper with control guard"
            line        = "func validateXmlyLink(raw string) (string, bool) {`nfor _, r := range raw { if unicode.IsControl(r) { return `"\"", false } }`nreturn raw, true`n}"
            pattern     = $xmlyLinkFunctionPattern
            shouldMatch = $false
            customCheck = "xmly-link-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "ximalaya link stale short prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u8bf7\u53d1\u9001\u559c\u9a6c\u62c9\u96c5\u94fe\u63a5\uff0c\u8981\u6c42\uff1a\u5fc5\u987b\u4ee5 https:// \u5f00\u5934\uff0c\u4ec5\u652f\u6301 ximalaya.com / www.ximalaya.com / m.ximalaya.com / xima.tv")')
            pattern     = $xmlyLinkStalePromptPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "ximalaya link shared requirement prompt"
            line        = [regex]::Unescape('sendPlainText(bot, chatID, "\u8bf7\u53d1\u9001\u559c\u9a6c\u62c9\u96c5\u94fe\u63a5\uff1a"+bookRequestLinkRequirementText+"\u3002")')
            pattern     = $xmlyLinkStalePromptPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "book request note helper without control guard"
            line        = "func validateBookRequestNote(raw string, allowEmpty bool) (string, bool) {`nnote := strings.TrimSpace(raw)`nif len([]rune(note)) > bookRequestNoteMaxLen { return `"\"", false }`nreturn note, true`n}"
            pattern     = $bookRequestNoteFunctionPattern
            shouldMatch = $true
            customCheck = "book-request-note-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "book request note helper with control guard"
            line        = "func validateBookRequestNote(raw string, allowEmpty bool) (string, bool) {`nfor _, r := range note { if r != '\n' && unicode.IsControl(r) { return `"\"", false } }`nreturn note, true`n}"
            pattern     = $bookRequestNoteFunctionPattern
            shouldMatch = $false
            customCheck = "book-request-note-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "server lines helper without control guard"
            line        = "func validateServerLinesContent(raw string) (string, bool) {`ncontent := strings.TrimSpace(raw)`nif len([]rune(content)) > serverLinesMaxLen { return `"\"", false }`nreturn content, true`n}"
            pattern     = $serverLinesFunctionPattern
            shouldMatch = $true
            customCheck = "server-lines-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "server lines helper with control guard"
            line        = "func validateServerLinesContent(raw string) (string, bool) {`nfor _, r := range content { if r != '\n' && unicode.IsControl(r) { return `"\"", false } }`nreturn content, true`n}"
            pattern     = $serverLinesFunctionPattern
            shouldMatch = $false
            customCheck = "server-lines-missing-control-guard"
        },
        [pscustomobject]@{
            name        = "server lines upsert without validation"
            line        = "lines := session.GetTemp(`"server_lines_content`")`nif err := upsertSystemConfigValue(`"server_lines`", lines); err != nil { return }"
            pattern     = $serverLinesUpsertPattern
            shouldMatch = $true
            customCheck = "server-lines-upsert-missing-validation"
        },
        [pscustomobject]@{
            name        = "server lines upsert after validation"
            line        = "lines, ok := validateServerLinesContent(lines)`nif !ok { return }`nif err := upsertSystemConfigValue(`"server_lines`", lines); err != nil { return }"
            pattern     = $serverLinesUpsertPattern
            shouldMatch = $false
            customCheck = "server-lines-upsert-missing-validation"
        },
        [pscustomobject]@{
            name        = "audit storage without display formatter"
            line        = "func formatAuditTextForStorage(text string, maxRunes int) string {`nredacted := redactSensitiveAuditText(text)`nrunes := []rune(redacted)`nreturn string(runes)`n}"
            pattern     = $auditStorageFunctionPattern
            shouldMatch = $true
            customCheck = "audit-storage-missing-display-format"
        },
        [pscustomobject]@{
            name        = "audit storage with display formatter"
            line        = "func formatAuditTextForStorage(text string, maxRunes int) string {`nredacted := formatAuditTextForDisplay(text)`nrunes := []rune(redacted)`nreturn string(runes)`n}"
            pattern     = $auditStorageFunctionPattern
            shouldMatch = $false
            customCheck = "audit-storage-missing-display-format"
        },
        [pscustomobject]@{
            name        = "audit display raw redact"
            line        = "detail := truncateRunes(strings.TrimSpace(redactSensitiveAuditText(item.Detail)), 120)"
            pattern     = $auditDisplayRawRedactPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "audit display formatter"
            line        = "detail := truncateRunes(formatAuditTextForDisplay(item.Detail), 120)"
            pattern     = $auditDisplayRawRedactPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "diagnostic markdown formatter raw redact"
            line        = "func formatMarkdownError(err error) string {`nreturn escapeMarkdown(truncateRunes(redactSensitiveAuditText(err.Error()), 500))`n}"
            pattern     = $diagnosticFormatterFunctionPattern
            shouldMatch = $true
            customCheck = "diagnostic-formatter-missing-display-format"
        },
        [pscustomobject]@{
            name        = "diagnostic plain formatter raw redact"
            line        = "func formatPlainValue(value any) string {`nreturn truncateRunes(redactSensitiveAuditText(fmt.Sprint(value)), 500)`n}"
            pattern     = $diagnosticFormatterFunctionPattern
            shouldMatch = $true
            customCheck = "diagnostic-formatter-missing-display-format"
        },
        [pscustomobject]@{
            name        = "diagnostic formatter display helper"
            line        = "func formatPlainValue(value any) string {`nreturn truncateRunes(formatDiagnosticTextForDisplay(fmt.Sprint(value)), 500)`n}"
            pattern     = $diagnosticFormatterFunctionPattern
            shouldMatch = $false
            customCheck = "diagnostic-formatter-missing-display-format"
        },
        [pscustomobject]@{
            name        = "system config error display raw escape"
            line        = "lastError := getSystemConfigString(autoBackupLastErrorKey)`nreturn escapeMarkdown(truncateRunes(lastError, 500))"
            pattern     = $systemConfigErrorDisplayPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "system config error display helper"
            line        = "lastError := getSystemConfigString(autoBackupLastErrorKey)`nreturn formatSystemConfigErrorForMarkdown(lastError)"
            pattern     = $systemConfigErrorDisplayPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "callback formatter raw truncate"
            line        = "func formatCallbackAlertText(text string) string {`nreturn truncateRunes(strings.TrimSpace(text), callbackAlertTextMaxRunes)`n}"
            pattern     = $callbackFormatterFunctionPattern
            shouldMatch = $true
            customCheck = "callback-formatter-missing-display-format"
        },
        [pscustomobject]@{
            name        = "callback formatter display helper"
            line        = "func formatCallbackAlertText(text string) string {`nformatted := formatDiagnosticTextForDisplay(text)`nreturn truncateRunes(formatted, callbackAlertTextMaxRunes)`n}"
            pattern     = $callbackFormatterFunctionPattern
            shouldMatch = $false
            customCheck = "callback-formatter-missing-display-format"
        },
        [pscustomobject]@{
            name        = "answer callback raw text"
            line        = "func answerCallback(bot *tgbotapi.BotAPI, callbackID string, text string) {`ncb := tgbotapi.NewCallback(callbackID, text)`n}"
            pattern     = $answerCallbackFunctionPattern
            shouldMatch = $true
            customCheck = "answer-callback-missing-display-format"
        },
        [pscustomobject]@{
            name        = "answer callback display helper"
            line        = "func answerCallback(bot *tgbotapi.BotAPI, callbackID string, text string) {`ncb := tgbotapi.NewCallback(callbackID, formatCallbackAlertText(text))`n}"
            pattern     = $answerCallbackFunctionPattern
            shouldMatch = $false
            customCheck = "answer-callback-missing-display-format"
        },
        [pscustomobject]@{
            name        = "telegram send helper raw err log"
            line        = "func sendPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) {`nif _, err := sendAutoDelete(bot, msg); err != nil { log.Printf(`"send failed: %v`", err) }`n}"
            pattern     = $telegramSendHelperFunctionPattern
            shouldMatch = $true
            customCheck = "telegram-send-helper-raw-error-log"
        },
        [pscustomobject]@{
            name        = "telegram send helper formatted err log"
            line        = "func sendPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) {`nif _, err := sendAutoDelete(bot, msg); err != nil { log.Printf(`"send failed: %v`", formatTelegramSendError(err)) }`n}"
            pattern     = $telegramSendHelperFunctionPattern
            shouldMatch = $false
            customCheck = "telegram-send-helper-raw-error-log"
        },
        [pscustomobject]@{
            name        = "cultivation ignored send"
            line        = "sendAutoDelete(bot, tgbotapi.NewMessage(chatID, announce))"
            pattern     = $cultivationIgnoredSendPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "cultivation logged send"
            line        = "if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, announce)); err != nil { log.Printf(`"send failed: %v`", formatTelegramSendError(err)) }"
            pattern     = $cultivationIgnoredSendPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "state machine user-visible ignored send"
            line        = "processingMsg, _ := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, `"processing`"))"
            pattern     = $stateMachineUserVisibleIgnoredSendPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "state machine user-visible logged send"
            line        = "processingMsg, err := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, `"processing`"))"
            pattern     = $stateMachineUserVisibleIgnoredSendPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "abs raw error log"
            line        = "log.Printf(`"ABS request failed: user=%d err=%v`", userID, err)"
            pattern     = $absRawErrorLogPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "abs formatted error log"
            line        = "log.Printf(`"ABS request failed: user=%d err=%v`", userID, formatPlainError(err))"
            pattern     = $absRawErrorLogPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "visible raw fmt error"
            line        = "replyText(bot, chatID, fmt.Sprintf(`"failed: %v`", err))"
            pattern     = $visibleRawErrorFormatPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "visible formatted fmt error"
            line        = "replyText(bot, chatID, fmt.Sprintf(`"failed: %s`", formatMarkdownError(err)))"
            pattern     = $visibleRawErrorFormatPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "plain formatted error with percent v"
            line        = "notifySuperAdminsPlain(bot, fmt.Sprintf(`"failed: %v`", formatPlainError(err)))"
            pattern     = $plainFormattedErrorPercentVPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "plain formatted error with percent s"
            line        = "notifySuperAdminsPlain(bot, fmt.Sprintf(`"failed: %s`", formatPlainError(err)))"
            pattern     = $plainFormattedErrorPercentVPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "fmt.Errorf wraps err with percent v"
            line        = "return fmt.Errorf(`"reload failed: %v`", err)"
            pattern     = $fmtErrorfWrapPercentVPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "fmt.Errorf wraps err with percent w"
            line        = "return fmt.Errorf(`"reload failed: %w`", err)"
            pattern     = $fmtErrorfWrapPercentVPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "audit raw error detail"
            line        = "writeAuditLog(userID, `"ACTION_FAILED`", target, fmt.Sprintf(`"操作失败，错误：%v`", err))"
            pattern     = $auditRawErrorDetailPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "audit formatted error detail"
            line        = "writeAuditLog(userID, `"ACTION_FAILED`", target, fmt.Sprintf(`"操作失败，错误：%s`", formatPlainError(err)))"
            pattern     = $auditRawErrorDetailPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "dynamic log field raw"
            line        = "log.Printf(`"operation failed: item=%s err=%v`", itemName, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log field formatted"
            line        = "log.Printf(`"operation failed: item=%s err=%v`", formatPlainValue(itemName), err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $false
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log abs field raw"
            line        = "log.Printf(`"refresh failed: user=%d abs=%s err=%v`", userID, absUserID, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log string user field formatted"
            line        = "log.Printf(`"delete failed: user=%s tg=%d abs=%s err=%v`", formatPlainValue(username), telegramID, formatPlainValue(absUserID), err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $false
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log config key field raw"
            line        = "log.Printf(`"config write failed: key=%s err=%v`", configKey, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log config value fields formatted"
            line        = "log.Printf(`"config write failed: key=%s value=%s err=%v`", formatPlainValue(configKey), formatPlainValue(newValue), err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $false
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log partially formatted fields"
            line        = "log.Printf(`"config write failed: key=%s value=%s err=%v`", formatPlainValue(configKey), newValue, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log event id field raw"
            line        = "log.Printf(`"settlement failed: boss=%s err=%v`", bossID, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic log event id field formatted"
            line        = "log.Printf(`"refund failed: race_id=%s reason=%s err=%v`", formatPlainValue(raceID), formatPlainValue(reason), err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $false
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic fatal startup field raw"
            line        = "log.Fatalf(`"migration failed: version=%s err=%v`", version, err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $true
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "dynamic fatal startup field formatted"
            line        = "log.Fatalf(`"migration failed: version=%s err=%v`", formatPlainValue(version), err)"
            pattern     = $dynamicLogFieldRawPattern
            shouldMatch = $false
            customCheck = "dynamic-log-field-raw"
        },
        [pscustomobject]@{
            name        = "garbled placeholder diagnostics"
            line        = "log.Printf(`"?? ????????????: chat=%d err=%s`", chatID, formatTelegramSendError(err))"
            pattern     = $garbledPlaceholderTextPattern
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "readable non-ascii diagnostics"
            line        = "log.Printf(`"发送修仙突破公告失败: chat=%d err=%s`", chatID, formatTelegramSendError(err))"
            pattern     = $garbledPlaceholderTextPattern
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "book request user reply log without CAS helper"
            line        = "DB.Model(&BookRequest{}).Where(`"id = ? AND status = ?`", id, bookRequestStatusNeedInfo).Updates(map[string]interface{}{})`ncreateBookRequestLog(id, userID, actor, `"user_reply`", oldStatus, bookRequestStatusClaimed, replyNote)"
            pattern     = $bookRequestUserReplyLogPattern
            shouldMatch = $true
            customCheck = "book-request-user-reply-missing-cas"
        },
        [pscustomobject]@{
            name        = "book request user reply log after CAS helper"
            line        = "updated, err := markBookRequestUserReplied(DB, needInfoReq, replyNote, now)`nif !updated { return }`ncreateBookRequestLog(needInfoReq.ID, userID, actor, `"user_reply`", oldStatus, bookRequestStatusClaimed, replyNote)"
            pattern     = $bookRequestUserReplyLogPattern
            shouldMatch = $false
            customCheck = "book-request-user-reply-missing-cas"
        },
        [pscustomobject]@{
            name        = "book request finish log without reload fallback"
            line        = "DB.Where(`"id = ?`", reqID).First(&req)`ncreateBookRequestLog(req.ID, adminID, actor, `"finish`", oldStatus, status, note)"
            pattern     = $bookRequestFinishLogPattern
            shouldMatch = $true
            customCheck = "book-request-finish-missing-reload-fallback"
        },
        [pscustomobject]@{
            name        = "book request finish log after reload fallback"
            line        = "if err := reloadBookRequestAfterFinish(DB, &req, reqID, status, adminID, actor, now); err != nil { log.Printf(`"reload failed`") }`ncreateBookRequestLog(req.ID, adminID, actor, `"finish`", oldStatus, status, note)"
            pattern     = $bookRequestFinishLogPattern
            shouldMatch = $false
            customCheck = "book-request-finish-missing-reload-fallback"
        },
        [pscustomobject]@{
            name        = "book request need-info log without reload fallback"
            line        = "createBookRequestLog(reqID, adminID, actor, `"need_info`", oldStatus, bookRequestStatusNeedInfo, note)`nif err := DB.Where(`"id = ?`", reqID).First(&updatedReq).Error; err == nil { sendPlainText(bot, updatedReq.UserID, note) }"
            pattern     = $bookRequestNeedInfoLogPattern
            shouldMatch = $true
            customCheck = "book-request-need-info-missing-reload-fallback"
        },
        [pscustomobject]@{
            name        = "book request need-info log before reload fallback"
            line        = "createBookRequestLog(reqID, adminID, actor, `"need_info`", oldStatus, bookRequestStatusNeedInfo, note)`nif err := reloadBookRequestAfterNeedInfo(DB, &updatedReq, reqID, note, adminID, actor, now); err != nil { log.Printf(`"reload failed`") }"
            pattern     = $bookRequestNeedInfoLogPattern
            shouldMatch = $false
            customCheck = "book-request-need-info-missing-reload-fallback"
        },
        [pscustomobject]@{
            name        = "book request admin-note log without reload fallback"
            line        = "createBookRequestLog(reqID, adminID, actor, `"admin_note`", oldStatus, oldStatus, note)`nif err := DB.Where(`"id = ?`", reqID).First(&updatedReq).Error; err == nil { refreshStoredBookRequestAdminMessage(bot, updatedReq, false, 0, 0) }"
            pattern     = $bookRequestAdminNoteLogPattern
            shouldMatch = $true
            customCheck = "book-request-admin-note-missing-reload-fallback"
        },
        [pscustomobject]@{
            name        = "book request admin-note log before reload fallback"
            line        = "createBookRequestLog(reqID, adminID, actor, `"admin_note`", oldStatus, oldStatus, note)`nif err := reloadBookRequestAfterAdminNote(DB, &updatedReq, reqID, note, adminID, actor, now); err != nil { log.Printf(`"reload failed`") }"
            pattern     = $bookRequestAdminNoteLogPattern
            shouldMatch = $false
            customCheck = "book-request-admin-note-missing-reload-fallback"
        }
    )

    foreach ($case in $cases) {
        if ($case.customCheck -eq "admin-reason-missing-control-guard" -or $case.customCheck -eq "sect-name-missing-control-guard" -or $case.customCheck -eq "xmly-link-missing-control-guard" -or $case.customCheck -eq "book-request-note-missing-control-guard" -or $case.customCheck -eq "server-lines-missing-control-guard") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and $match.Groups['body'].Value -notmatch 'unicode\.IsControl\('
        } elseif ($case.customCheck -eq "server-lines-upsert-missing-validation") {
            $match = [regex]::Match($case.line, $case.pattern)
            if ($match.Success) {
                $prefix = $case.line.Substring(0, $match.Index)
                $matched = $prefix -notmatch 'validateServerLinesContent\('
            } else {
                $matched = $false
            }
        } elseif ($case.customCheck -eq "audit-storage-missing-display-format") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and $match.Groups['body'].Value -notmatch 'formatAuditTextForDisplay\('
        } elseif ($case.customCheck -eq "diagnostic-formatter-missing-display-format") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and $match.Groups['body'].Value -notmatch 'formatDiagnosticTextForDisplay\('
        } elseif ($case.customCheck -eq "callback-formatter-missing-display-format") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and ($match.Groups['body'].Value -notmatch 'formatDiagnosticTextForDisplay\(' -or $match.Groups['body'].Value -notmatch 'callbackAlertTextMaxRunes')
        } elseif ($case.customCheck -eq "answer-callback-missing-display-format") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and $match.Groups['body'].Value -notmatch 'formatCallbackAlertText\('
        } elseif ($case.customCheck -eq "telegram-send-helper-raw-error-log") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and [regex]::IsMatch($match.Groups['body'].Value, $telegramSendHelperRawErrLogPattern) -and $match.Groups['body'].Value -notmatch 'formatTelegramSendError\('
        } elseif ($case.customCheck -eq "dynamic-log-field-raw") {
            $match = [regex]::Match($case.line, $case.pattern)
            $matched = $match.Success -and (Has-UnformattedDynamicLogField -CallText $case.line)
        } elseif ($case.customCheck -eq "book-request-user-reply-missing-cas") {
            $match = [regex]::Match($case.line, $case.pattern)
            if ($match.Success) {
                $prefix = $case.line.Substring(0, $match.Index)
                $matched = $prefix -notmatch 'markBookRequestUserReplied\('
            } else {
                $matched = $false
            }
        } elseif ($case.customCheck -eq "book-request-finish-missing-reload-fallback") {
            $match = [regex]::Match($case.line, $case.pattern)
            if ($match.Success) {
                $prefix = $case.line.Substring(0, $match.Index)
                $matched = $prefix -notmatch 'reloadBookRequestAfterFinish\('
            } else {
                $matched = $false
            }
        } elseif ($case.customCheck -eq "book-request-need-info-missing-reload-fallback") {
            $match = [regex]::Match($case.line, $case.pattern)
            if ($match.Success) {
                $suffix = $case.line.Substring($match.Index)
                $matched = $suffix -notmatch 'reloadBookRequestAfterNeedInfo\('
            } else {
                $matched = $false
            }
        } elseif ($case.customCheck -eq "book-request-admin-note-missing-reload-fallback") {
            $match = [regex]::Match($case.line, $case.pattern)
            if ($match.Success) {
                $suffix = $case.line.Substring($match.Index)
                $matched = $suffix -notmatch 'reloadBookRequestAfterAdminNote\('
            } else {
                $matched = $false
            }
        } else {
            $matched = $case.line -match $case.pattern
        }
        if ($matched -ne $case.shouldMatch) {
            Add-Finding "error" "agent-audit-self-test" "scripts/agent_audit.ps1" 0 `
                ("Audit rule self-check failed: {0}; expected match={1}, actual match={2}." -f $case.name, $case.shouldMatch, $matched) $case.line
        }
    }

    $inventoryCases = @(
        [pscustomobject]@{
            name        = "inventory item raw markdown reply"
            line        = 'replyText(bot, chatID, fmt.Sprintf("**%s**", item.ItemName))'
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "inventory item markdown helper"
            line        = 'replyText(bot, chatID, fmt.Sprintf("**%s**", inventoryItemMarkdownName(item.ItemName)))'
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "shop item raw markdown edit"
            line        = 'editMenuPanel(bot, chatID, msgID, fmt.Sprintf("**%s**", item.Name), markup)'
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "button label raw item name"
            line        = 'tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s", item.Name), "shop:item:"+item.ID)'
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "breakthrough pill raw markdown reply"
            line        = 'replyText(bot, chatID, fmt.Sprintf("**%s**", req.PillName))'
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "breakthrough pill markdown helper"
            line        = 'replyText(bot, chatID, fmt.Sprintf("**%s**", inventoryItemMarkdownName(req.PillName)))'
            shouldMatch = $false
        }
    )

    foreach ($case in $inventoryCases) {
        $matched = Has-RawInventoryItemNameInMarkdownCall -CallText $case.line
        if ($matched -ne $case.shouldMatch) {
            Add-Finding "error" "agent-audit-self-test" "scripts/agent_audit.ps1" 0 `
                ("Audit rule self-check failed: {0}; expected match={1}, actual match={2}." -f $case.name, $case.shouldMatch, $matched) $case.line
        }
    }

    $gardenItemCases = @(
        [pscustomobject]@{
            name        = "garden herb raw markdown builder"
            line        = 'b.WriteString(fmt.Sprintf("%s：`%d` 株\n", cfg.HerbName, qty))'
            shouldMatch = $true
        },
        [pscustomobject]@{
            name        = "garden herb markdown helper"
            line        = 'b.WriteString(fmt.Sprintf("%s：`%d` 株\n", inventoryItemMarkdownName(cfg.HerbName), qty))'
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "garden recipe markdown escape"
            line        = 'b.WriteString(fmt.Sprintf("**%s**", escapeMarkdown(cfg.Name)))'
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "garden inventory lookup raw key"
            line        = 'b.WriteString(fmt.Sprintf("持有 `%d`", inv[cfg.ProductName]))'
            shouldMatch = $false
        },
        [pscustomobject]@{
            name        = "garden button raw herb name"
            line        = 'tgbotapi.NewInlineKeyboardButtonData("回收1株 "+cfg.HerbName, "garden:sellone:"+cfg.Key)'
            shouldMatch = $false
        }
    )

    foreach ($case in $gardenItemCases) {
        $matched = Has-RawGardenItemNameInMarkdownLine -Line $case.line
        if ($matched -ne $case.shouldMatch) {
            Add-Finding "error" "agent-audit-self-test" "scripts/agent_audit.ps1" 0 `
                ("Audit rule self-check failed: {0}; expected match={1}, actual match={2}." -f $case.name, $case.shouldMatch, $matched) $case.line
        }
    }
}

Test-AgentAuditRuleSelfChecks

foreach ($file in $goFiles) {
    $rel = Get-RelPath $file.FullName
    $lines = Get-Content -LiteralPath $file.FullName -Encoding UTF8
    $content = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8

    foreach ($match in [regex]::Matches($content, $marketplacePointDescriptionRawPattern)) {
        $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
        Add-Finding "error" "marketplace-point-description" $rel $lineNo `
            "Marketplace point transaction descriptions must use marketplaceBuyPointDescription/marketplaceSellPointDescription so item names are single-line sanitized." $match.Value
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $marketplaceSecretListingNameStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "marketplace-secret-listing-name-prompt-validation" $rel $lineNo `
                "Marketplace secret listing-name prompts must mention the full validMarketplaceSecretListingName rule; reuse marketplaceSecretListingNameRequirementText instead of length-only wording." $match.Value
        }

        foreach ($match in [regex]::Matches($content, $marketplaceInventoryItemNameStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "marketplace-inventory-item-name-prompt-validation" $rel $lineNo `
                "Marketplace inventory item-name prompts must mention the full validMarketplaceInventoryItemName rule; reuse marketplaceInventoryItemNameRequirementText instead of length-only wording." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $telegramUsernameDirectMentionPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $insideRawDisplayNameHelper = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'func\s+getTelegramDisplayName\('
            ) -Before 20 -After 0
            if ($insideRawDisplayNameHelper) {
                continue
            }
            Add-Finding "error" "telegram-username-mention-markdown" $rel $lineNo `
                "Telegram username mentions in Markdown messages must use telegramUsernameMentionMarkdown instead of direct @ concatenation." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$' -and $content -match $lotteryTitleLengthOnlyPattern) {
        $match = [regex]::Match($content, $lotteryTitleLengthOnlyPattern)
        $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
        Add-Finding "error" "lottery-title-validation" $rel $lineNo `
            "Lottery activity titles must use validLotteryTitle so control characters cannot break announcements, lists, or ledgers." $match.Value
    }

    if ($rel -notmatch '_test\.go$' -and $content -match $lotteryClaimCodeLengthOnlyPattern) {
        $match = [regex]::Match($content, $lotteryClaimCodeLengthOnlyPattern)
        $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
        Add-Finding "error" "lottery-claim-code-validation" $rel $lineNo `
            "Lottery claim codes must use validLotteryClaimCode so control characters cannot break private reminders or operational readability." $match.Value
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $lotteryTitleStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "lottery-title-prompt-validation" $rel $lineNo `
                "Lottery title prompts must mention the full validLotteryTitle rule; reuse lotteryTitleRequirementText instead of length-only wording." $match.Value
        }

        foreach ($match in [regex]::Matches($content, $lotteryClaimCodeStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "lottery-claim-code-prompt-validation" $rel $lineNo `
                "Lottery claim-code prompts must mention the full validLotteryClaimCode rule; reuse lotteryClaimCodeRequirementText instead of length-only wording." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $adminReasonMatch = [regex]::Match($content, $adminReasonFunctionPattern)
        if ($adminReasonMatch.Success -and $adminReasonMatch.Groups['body'].Value -notmatch 'unicode\.IsControl\(') {
            $lineNo = (($content.Substring(0, $adminReasonMatch.Index) -split "`n").Count)
            Add-Finding "error" "admin-reason-control-validation" $rel $lineNo `
                "validateAdminReason must reject control characters so high-risk operation reasons cannot break confirmations, audit logs, or notifications." $adminReasonMatch.Value
        }

        foreach ($match in [regex]::Matches($content, $adminReasonStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "admin-reason-prompt-validation" $rel $lineNo `
                "High-risk operation reason prompts must mention the full validateAdminReason rule; reuse adminReasonRequirementText or adminReasonInvalidText instead of short-only wording." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $sectNameMatch = [regex]::Match($content, $sectNameFunctionPattern)
        if ($sectNameMatch.Success -and $sectNameMatch.Groups['body'].Value -notmatch 'unicode\.IsControl\(') {
            $lineNo = (($content.Substring(0, $sectNameMatch.Index) -split "`n").Count)
            Add-Finding "error" "sect-name-control-validation" $rel $lineNo `
                "validateSectName must reject control characters so sect names cannot break panels, leaderboards, ledgers, or operational readability." $sectNameMatch.Value
        }

        foreach ($match in [regex]::Matches($content, $sectNameStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "sect-name-prompt-validation" $rel $lineNo `
                "Sect name prompts must mention the full validateSectName rule; reuse sectNameInvalidText instead of stale wording." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $xmlyLinkMatch = [regex]::Match($content, $xmlyLinkFunctionPattern)
        if ($xmlyLinkMatch.Success -and $xmlyLinkMatch.Groups['body'].Value -notmatch 'unicode\.IsControl\(') {
            $lineNo = (($content.Substring(0, $xmlyLinkMatch.Index) -split "`n").Count)
            Add-Finding "error" "xmly-link-control-validation" $rel $lineNo `
                "validateXmlyLink must reject control characters so book request links cannot break tickets, notifications, audit logs, or operational readability." $xmlyLinkMatch.Value
        }

        foreach ($match in [regex]::Matches($content, $xmlyLinkStalePromptPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "xmly-link-prompt-validation" $rel $lineNo `
                "Book request link prompts must reuse bookRequestLinkRequirementText so user-facing text matches validateXmlyLink." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $bookRequestNoteMatch = [regex]::Match($content, $bookRequestNoteFunctionPattern)
        if ($bookRequestNoteMatch.Success -and $bookRequestNoteMatch.Groups['body'].Value -notmatch 'unicode\.IsControl\(') {
            $lineNo = (($content.Substring(0, $bookRequestNoteMatch.Index) -split "`n").Count)
            Add-Finding "error" "book-request-note-control-validation" $rel $lineNo `
                "validateBookRequestNote must reject control characters so book request notes cannot break tickets, notifications, logs, or operational readability." $bookRequestNoteMatch.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $serverLinesMatch = [regex]::Match($content, $serverLinesFunctionPattern)
        if ($serverLinesMatch.Success -and $serverLinesMatch.Groups['body'].Value -notmatch 'unicode\.IsControl\(') {
            $lineNo = (($content.Substring(0, $serverLinesMatch.Index) -split "`n").Count)
            Add-Finding "error" "server-lines-control-validation" $rel $lineNo `
                "validateServerLinesContent must reject control characters other than newlines so user-visible server lines cannot break panels, Markdown output, or operational readability." $serverLinesMatch.Value
        }

        foreach ($match in [regex]::Matches($content, $serverLinesUpsertPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $hasServerLinesValidation = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'validateServerLinesContent\('
            ) -Before 18 -After 0
            if (-not $hasServerLinesValidation) {
                Add-Finding "error" "server-lines-upsert-validation" $rel $lineNo `
                    "server_lines SystemConfig writes must validate with validateServerLinesContent immediately before persistence." $match.Value
            }
        }
    }

    if ($rel -notmatch '_test\.go$') {
        $auditStorageMatch = [regex]::Match($content, $auditStorageFunctionPattern)
        if ($auditStorageMatch.Success -and $auditStorageMatch.Groups['body'].Value -notmatch 'formatAuditTextForDisplay\(') {
            $lineNo = (($content.Substring(0, $auditStorageMatch.Index) -split "`n").Count)
            Add-Finding "error" "audit-storage-display-format" $rel $lineNo `
                "formatAuditTextForStorage must use formatAuditTextForDisplay so AuditLog target/detail are redacted and normalized before persistence." $auditStorageMatch.Value
        }

        foreach ($match in [regex]::Matches($content, $auditDisplayRawRedactPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "audit-display-format" $rel $lineNo `
                "AuditLog query display should use formatAuditTextForDisplay so historical target/detail values are redacted and normalized consistently." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $diagnosticFormatterFunctionPattern)) {
            if ($match.Groups['body'].Value -notmatch 'formatDiagnosticTextForDisplay\(') {
                $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                Add-Finding "error" "diagnostic-display-format" $rel $lineNo `
                    "formatMarkdownError and formatPlainValue must use formatDiagnosticTextForDisplay so diagnostic text is redacted, length-limited, and normalized consistently." $match.Value
            }
        }

        foreach ($match in [regex]::Matches($content, $systemConfigErrorDisplayPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "system-config-error-display-format" $rel $lineNo `
                "Persisted SystemConfig error values displayed in Markdown must use formatSystemConfigErrorForMarkdown so old raw values are redacted, normalized, length-limited, and escaped." $match.Value
        }

        foreach ($match in [regex]::Matches($content, $callbackFormatterFunctionPattern)) {
            if ($match.Groups['body'].Value -notmatch 'formatDiagnosticTextForDisplay\(' -or $match.Groups['body'].Value -notmatch 'callbackAlertTextMaxRunes') {
                $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                Add-Finding "error" "callback-display-format" $rel $lineNo `
                    "formatCallbackAlertText must use formatDiagnosticTextForDisplay and callbackAlertTextMaxRunes so callback alerts are redacted, normalized, and kept within Telegram limits." $match.Value
            }
        }

        foreach ($match in [regex]::Matches($content, $answerCallbackFunctionPattern)) {
            if ($match.Groups['body'].Value -notmatch 'formatCallbackAlertText\(') {
                $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                Add-Finding "error" "callback-display-format" $rel $lineNo `
                    "answerCallback must route alert text through formatCallbackAlertText before sending it to Telegram." $match.Value
            }
        }

        foreach ($match in [regex]::Matches($content, $telegramSendHelperFunctionPattern)) {
            $body = $match.Groups['body'].Value
            if ([regex]::IsMatch($body, $telegramSendHelperRawErrLogPattern) -and $body -notmatch 'formatTelegramSendError\(') {
                $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                Add-Finding "error" "telegram-send-error-format" $rel $lineNo `
                    "Shared Telegram send helpers must log send failures through formatTelegramSendError so returned error text is redacted and normalized." $match.Value
            }
        }

        if ($rel -eq "cultivation.go") {
            for ($sendIndex = 0; $sendIndex -lt $lines.Length; $sendIndex++) {
                $sendLine = $lines[$sendIndex]
                if ($sendLine -match $cultivationIgnoredSendPattern) {
                    Add-Finding "error" "telegram-send-error-format" $rel ($sendIndex + 1) `
                        "Cultivation user-visible sendAutoDelete calls must log failures with formatTelegramSendError instead of ignoring Telegram send errors." $sendLine
                }
            }
        }

        if ($rel -eq "state_machine.go") {
            for ($sendIndex = 0; $sendIndex -lt $lines.Length; $sendIndex++) {
                $sendLine = $lines[$sendIndex]
                if ($sendLine -match $stateMachineUserVisibleIgnoredSendPattern) {
                    Add-Finding "error" "telegram-send-error-format" $rel ($sendIndex + 1) `
                        "State machine user-visible Telegram notification sends and edits must log failures with formatTelegramSendError instead of ignoring Telegram errors." $sendLine
                }
            }
        }

        foreach ($match in [regex]::Matches($content, $absRawErrorLogPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "abs-error-log-format" $rel $lineNo `
                "ABS-related error logs must use formatPlainError so external service errors are redacted, normalized, and length-limited before entering logs." $match.Value
        }

        foreach ($match in [regex]::Matches($content, $auditRawErrorDetailPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "audit-error-detail-format" $rel $lineNo `
                "AuditLog detail strings must format dynamic error values with formatPlainError before storage, even though the audit writer has a final redaction fallback." $match.Value
        }
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $bookRequestUserReplyLogPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $hasUserReplyCAS = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'markBookRequestUserReplied\('
            ) -Before 45 -After 0
            if (-not $hasUserReplyCAS) {
                Add-Finding "error" "book-request-user-reply-cas" $rel $lineNo `
                    "Book request user-reply logs must only be written after markBookRequestUserReplied succeeds, so stale need_info replies cannot produce false success notifications." $match.Value
            }
        }
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $bookRequestFinishLogPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $hasFinishReloadFallback = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'reloadBookRequestAfterFinish\('
            ) -Before 18 -After 0
            if (-not $hasFinishReloadFallback) {
                Add-Finding "error" "book-request-finish-reload-fallback" $rel $lineNo `
                    "Book request finish logs must be written after reloadBookRequestAfterFinish, so successful finish updates still notify with the written status if reload fails." $match.Value
            }
        }

        foreach ($match in [regex]::Matches($content, $bookRequestNeedInfoLogPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $hasNeedInfoReloadFallback = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'reloadBookRequestAfterNeedInfo\('
            ) -Before 0 -After 35
            if (-not $hasNeedInfoReloadFallback) {
                Add-Finding "error" "book-request-need-info-reload-fallback" $rel $lineNo `
                    "Book request need-info logs must be followed by reloadBookRequestAfterNeedInfo, so successful need-info updates still notify the user if reload fails." $match.Value
            }
        }

        foreach ($match in [regex]::Matches($content, $bookRequestAdminNoteLogPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $hasAdminNoteReloadFallback = Has-Nearby -Lines $lines -Index ($lineNo - 1) -Patterns @(
                'reloadBookRequestAfterAdminNote\('
            ) -Before 0 -After 35
            if (-not $hasAdminNoteReloadFallback) {
                Add-Finding "error" "book-request-admin-note-reload-fallback" $rel $lineNo `
                    "Book request admin-note logs must be followed by reloadBookRequestAfterAdminNote, so successful admin-note updates do not falsely report message refresh if reload fails." $match.Value
            }
        }
    }

    if ($rel -notmatch '_test\.go$') {
        foreach ($match in [regex]::Matches($content, $businessErrorCodeRawReturnPattern)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            Add-Finding "error" "business-error-code-raw-return" $rel $lineNo `
                "Business error-code helpers must return stable codes, UNKNOWN, or a centralized whitelist fallback; do not return raw err.Error()." $match.Value
        }
    }

    for ($i = 0; $i -lt $lines.Length; $i++) {
        $line = $lines[$i]
        $lineNo = $i + 1

        if ($line -match 'UpdateColumn\("points"|Update\("points"|gorm\.Expr\("points') {
            $insidePointHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+applyPointDeltaInTx\('
            ) -Before 40 -After 0
            if (-not $insidePointHelper) {
                Add-Finding "error" "points-ledger" $rel $lineNo `
                    "Direct User.points update should be routed through applyPointDeltaInTx." $line
            }
        }

        if ($line -match 'Create\(&PointTransaction\s*\{') {
            $insidePointHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+applyPointDeltaInTx\('
            ) -Before 80 -After 0
            if (-not $insidePointHelper) {
                Add-Finding "error" "point-transaction-helper" $rel $lineNo `
                    "PointTransaction rows should be created through applyPointDeltaInTx so balance changes and ledger rows stay atomic." $line
            }
        }

        if ($line -match 'UpdateColumn\("quantity".*gorm\.Expr\("quantity -|gorm\.Expr\("quantity -') {
            $hasGuard = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'quantity\s*>',
                'quantity\s*>=',
                'OnConflict',
                'DoUpdates'
            ) -Before 8 -After 8
            if (-not $hasGuard) {
                Add-Finding "error" "inventory-negative" $rel $lineNo `
                    "Inventory or quota quantity update may lack a non-negative guard near the write." $line
            }
        }

        if ($line -match 'gorm\.Expr\("(funds|prestige|contribution)\s+-') {
            $assetField = $Matches[1]
            $hasGuard = Has-Nearby -Lines $lines -Index $i -Patterns @(
                ($assetField + '\s*>='),
                ('Where\(".*' + $assetField + '\s*>='),
                'RESOURCE_NOT_ENOUGH',
                'CONTRIBUTION_NOT_ENOUGH'
            ) -Before 10 -After 10
            if (-not $hasGuard) {
                Add-Finding "error" "sect-asset-negative" $rel $lineNo `
                    "Sect funds, prestige, or contribution deduction should include a matching non-negative condition near the write." $line
            }
        }

        if ($line -match 'gorm\.Expr\("(left_count|left_points)\s+-') {
            $hasCASGuard = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'left_count\s*=',
                'left_points\s*=',
                'is_finished\s*='
            ) -Before 12 -After 14
            if (-not $hasCASGuard) {
                Add-Finding "error" "redpacket-cas" $rel $lineNo `
                    "Red packet remaining count/points deduction should use a CAS condition on left_count, left_points, and is_finished." $line
            }
        }

        if ($line -match '"(race_win|dice_win)"') {
            $pointType = $Matches[1]
            $insidePointDeltaCall = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'applyPointDeltaInTx\('
            ) -Before 12 -After 2
            if ($insidePointDeltaCall) {
                $requiredHelper = if ($pointType -eq "race_win") { 'updateRaceBetStatusCAS\(' } else { 'updateDiceBetStatusCAS\(' }
                $hasSettlementClaim = Has-Nearby -Lines $lines -Index $i -Patterns @(
                    $requiredHelper
                ) -Before 35 -After 0
                if (-not $hasSettlementClaim) {
                    Add-Finding "error" "game-settlement-cas" $rel $lineNo `
                        "Race/dice win payout must first claim the active bet row through the settlement CAS helper." $line
                }
            }
        }

        if ($line -match 'addPointsToFusionPoolInTx\(' -and $line -notmatch '^\s*func\s+addPointsToFusionPoolInTx\(') {
            $hasLockedFusionPoolTx = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'runFusionPoolLockedTransaction\('
            ) -Before 140 -After 80
            if (-not $hasLockedFusionPoolTx) {
                Add-Finding "error" "fusion-pool-lock-order" $rel $lineNo `
                    "addPointsToFusionPoolInTx must be called from runFusionPoolLockedTransaction so fusionPoolMutex is held before opening the database transaction." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match 'strings\.Contains\(.*err\.Error\(\).*"(unique|constraint failed|duplicate)"') {
            Add-Finding "error" "unique-error-helper" $rel $lineNo `
                "Database unique-constraint checks should use isUniqueConstraintError instead of ad hoc err.Error string matching." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match '(==|!=)\s*gorm\.ErrRecordNotFound') {
            Add-Finding "error" "gorm-not-found-errors-is" $rel $lineNo `
                "Use errors.Is(err, gorm.ErrRecordNotFound) so wrapped GORM not-found errors are handled correctly." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $businessErrorComparisonPattern) {
            Add-Finding "error" "business-error-sentinel" $rel $lineNo `
                "Known business errors should use package sentinel errors with errors.Is instead of direct err.Error string comparisons." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $businessErrorStringReturnPattern) {
            Add-Finding "error" "business-error-string-return" $rel $lineNo `
                "Known business errors should use package sentinel errors instead of fmt.Errorf code strings." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match 'setSystemConfigString\([^)]*(?:ErrorKey|_error|last_error)[^)]*err\.Error\(\)') {
            Add-Finding "error" "system-config-error-format" $rel $lineNo `
                "Persisted SystemConfig error fields should use setSystemConfigError so errors are redacted and length-limited before storage." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match 'strings\.Contains\(.*(?:err\.Error\(\)|msg).*"[^"]*404[^"]*"') {
            $insideAbsNotFoundHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+IsAbsNotFoundError\('
            ) -Before 20 -After 0
            if (-not $insideAbsNotFoundHelper) {
                Add-Finding "error" "abs-not-found-helper" $rel $lineNo `
                    "ABS 404/not-found checks should use IsAbsNotFoundError instead of ad hoc string matching." $line
            }
        }

        if ($line -match 'Create\(&Inventory\s*\{') {
            $usesInventoryHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'inventoryQuantityUpsertClause\('
            ) -Before 8 -After 2
            if (-not $usesInventoryHelper) {
                Add-Finding "error" "inventory-upsert-helper" $rel $lineNo `
                    "Inventory grant should use inventoryQuantityUpsertClause so user/item upsert semantics stay consistent." $line
            }
        }

        if ($line -match 'Create\(&AuditLog\s*\{') {
            $insideAuditHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+writeAuditLogInTx\('
            ) -Before 45 -After 0
            if (-not $insideAuditHelper) {
                Add-Finding "error" "audit-log-helper" $rel $lineNo `
                    "AuditLog rows should be created through writeAuditLogInTx so target/detail redaction stays consistent." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $securityAttemptDirectCreatePattern) {
            $insideSecurityAttemptHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+recordSecurityAttemptFailureInTx\('
            ) -Before 50 -After 0
            if (-not $insideSecurityAttemptHelper) {
                Add-Finding "error" "security-attempt-helper" $rel $lineNo `
                    "SecurityAttemptLock failure rows should be created through recordSecurityAttemptFailureInTx so concurrent first failures retry after unique conflicts." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $telegramErrorStringPattern) {
            $insideTelegramErrorHelper = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'func\s+isTelegramMessageNotModifiedError\(',
                'func\s+isTerminalTelegramDeleteError\('
            ) -Before 20 -After 0
            if (-not $insideTelegramErrorHelper) {
                Add-Finding "error" "telegram-error-helper" $rel $lineNo `
                    "Telegram API terminal/no-op errors should use isTelegramMessageNotModifiedError or isTerminalTelegramDeleteError instead of ad hoc err.Error string matching." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match 'Create\(&SystemConfig\s*\{') {
            $hasConfigUpsert = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'clause\.OnConflict',
                'upsertSystemConfigValue\(',
                'setSystemConfigString\('
            ) -Before 10 -After 1
            if (-not $hasConfigUpsert) {
                Add-Finding "error" "system-config-upsert" $rel $lineNo `
                    "SystemConfig writes should use an ON CONFLICT upsert helper instead of create-after-read." $line
            }
        }

        if ($line -match 'Unscoped\(\)\.Delete|DELETE FROM|DROP TABLE|DROP COLUMN') {
            Add-Finding "warn" "destructive-data-op" $rel $lineNo `
                "Destructive data operation requires explicit review before production use." $line
        }

        if ($line -match 'clause\.OnConflict\s*\{') {
            $window = Get-CodeWindow -Lines $lines -Index $i
            $usesPartialIndexModel = $window -match 'DailyListeningStat|dailyListeningStatOnConflict|SectSecretRealmParticipant|sectSecretRealmParticipantOnConflict'
            $namesConflictColumns = $window -match 'Columns:\s*\[\]clause\.Column'
            $hasPartialTarget = $window -match 'TargetWhere'
            if ($usesPartialIndexModel -and $namesConflictColumns -and -not $hasPartialTarget) {
                Add-Finding "error" "partial-index-upsert" $rel $lineNo `
                    "SQLite partial unique index upsert names conflict columns without a matching TargetWhere predicate." $line
            }
        }

        if ($line -match 'DeleteUser\(' -and $line -notmatch 'func\s+\(.+\)\s*DeleteUser\(') {
            $isRollbackDelete = $line -match 'rollbackErr\s*:=\s*absClient\.DeleteUser\('
            $hasAuditOrGuard = Has-Nearby -Lines $lines -Index $i -Patterns @(
                'requireSuperAdmin\(',
                'writeAuditLog\(',
                'AppConfig\.AccountGraceDays',
                'rollbackErr\s*:=\s*absClient\.DeleteUser\(',
                '确认删除',
                '确认注销'
            ) -Before 45 -After 45
            if ($isRollbackDelete) {
                Add-Finding "warn" "abs-delete-rollback-review" $rel $lineNo `
                    "ABS DeleteUser is used as registration rollback; keep it scoped to compensating failed local writes." $line
            } elseif (-not $hasAuditOrGuard) {
                Add-Finding "error" "abs-delete-guard" $rel $lineNo `
                    "ABS DeleteUser call lacks a nearby super-admin/audit/lifecycle guard." $line
            } else {
                Add-Finding "warn" "abs-delete-review" $rel $lineNo `
                    "ABS DeleteUser is high risk and should remain guarded, audited, and documented." $line
            }
        }

        if ($line -match 'log\.Printf|fmt\.Printf|fmt\.Println|log\.Fatalf') {
            if ($line -match 'TgToken|TELEGRAM_BOT_TOKEN|AbsKey|ABS_API_KEY|SecurityPepper|SECURITY_PEPPER|BackupEncryptKey|BACKUP_ENCRYPT_KEY|password|Password|SecurityCode') {
                if ($line -match 'AppConfig\.|,\s*(password|newPassword|text|u\.SecurityCode|AppConfig\.)' -and $line -notmatch 'secretFingerprint|absResponseSnippet|\*\*\*') {
                    Add-Finding "error" "secret-log" $rel $lineNo `
                        "Potential sensitive value in log output." $line
                }
            }

            if ($line -match 'panic.*%v' -and $line -notmatch 'formatPlainValue\(') {
                Add-Finding "error" "panic-log-format" $rel $lineNo `
                    "Recovered panic values in logs must use formatPlainValue so sensitive data is redacted and length-limited." $line
            }

            $logCallText = Get-CallExpression -Lines $lines -Index $i
            if ($logCallText -match $dynamicLogFieldRawPattern -and (Has-UnformattedDynamicLogField -CallText $logCallText)) {
                Add-Finding "error" "dynamic-log-field-format" $rel $lineNo `
                    "Dynamic item/name/reason/abs/string-user/key/value/status/event-id/startup fields in logs must use formatPlainValue so control characters and long text cannot disrupt log output." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match 'fmt\.Printf|fmt\.Println') {
            Add-Finding "error" "fmt-print-log" $rel $lineNo `
                "Production Go code should use log.Printf/log.Println instead of fmt.Print* so output is consistent and auditable." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $garbledPlaceholderTextPattern) {
            Add-Finding "error" "garbled-placeholder-text" $rel $lineNo `
                "User-visible or diagnostic text appears garbled or placeholder-like; keep source text readable UTF-8." $line
        }

        if ($rel -notmatch '_test\.go$' -and $line -match $fmtErrorfWrapPercentVPattern) {
            Add-Finding "error" "fmt-errorf-wrap" $rel $lineNo `
                "fmt.Errorf calls that wrap an error should use %w instead of %v so errors.Is/errors.As keep working." $line
        }

        if ($line -match 'notifySuperAdminsPlain\(' -and $line -notmatch '^\s*func\s+notifySuperAdminsPlain\(') {
            $window = Get-CodeWindow -Lines $lines -Index $i -Before 0 -After 8
            if ($window -match '%v' -and $window -notmatch 'formatPlainError\(|formatPlainValue\(') {
                Add-Finding "error" "plain-error-format" $rel $lineNo `
                    "Admin plain-text notifications must format raw error or panic values through formatPlainError/formatPlainValue before sending." $line
            }
            if ($window -match $plainFormattedErrorPercentVPattern) {
                Add-Finding "error" "plain-error-format" $rel $lineNo `
                    "Admin plain-text notifications should insert formatPlainError/formatPlainValue results with %s, not %v." $line
            }
        }

        if ($line -match '(sendPlainText|replyText|answerCallback)\(') {
            $callText = Get-CallExpression -Lines $lines -Index $i
            if ($callText -match '\.Error\(\)' -and $callText -notmatch 'formatPlainError\(|formatMarkdownError\(|formatPlainValue\(') {
                Add-Finding "error" "visible-error-format" $rel $lineNo `
                    "User-visible messages must not directly expose err.Error(); use formatPlainError or formatMarkdownError first." $line
            }
            if ($callText -match $visibleRawErrorFormatPattern -and $callText -notmatch 'formatPlainError\(|formatMarkdownError\(|formatPlainValue\(') {
                Add-Finding "error" "visible-error-format" $rel $lineNo `
                    "User-visible fmt.Sprintf messages must not expose raw error values with %v; use formatMarkdownError or formatPlainError first." $line
            }
            if ($callText -match $markdownSecretDeliveryPattern) {
                Add-Finding "error" "marketplace-secret-plain-delivery" $rel $lineNo `
                    "Marketplace card secrets must be delivered through the explicit plain-text sender, not generic visible message helpers." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $line -match '(replyText|sendMenuPanel|editMenuPanel|sendMenu|sendAutoDelete|sendGroupAutoDeleteMessage)\(') {
            if (Has-RawInventoryItemNameInMarkdownCall -CallText $line) {
                Add-Finding "error" "inventory-item-markdown-name" $rel $lineNo `
                    "Markdown messages that display inventory or shop item names must use inventoryItemMarkdownName." $line
            }
        }

        if ($rel -notmatch '_test\.go$' -and $rel -eq "garden.go" -and (Has-RawGardenItemNameInMarkdownLine -Line $line)) {
            Add-Finding "error" "garden-item-markdown-name" $rel $lineNo `
                "Garden Markdown text that displays seed, herb, recipe, or pill names must use inventoryItemMarkdownName or escapeMarkdown." $line
        }
    }

    $inTransaction = $false
    $transactionStartLine = 0
    $braceBalance = 0
    $transactionStartPattern = '(?:\b(?:DB|db|tx)\.Transaction\s*\(|\brunFusionPoolLockedTransaction\s*\()'
    $externalEffectPattern = '(?:\b(?:sendPlainText|replyText|answerCallback|sendAutoDelete|sendGroupAutoDeleteMessage|sendMenu|sendMenuPanel|sendLotteryPlainText|sendLotteryGroupPlainText|sendLotteryReplyPlainText|sendLotteryGroupPersistentText|sendEncryptedBackupToTelegram)\s*\(|\bbot\.(?:Send|Request)\s*\(|\babsClient\.)'

    for ($i = 0; $i -lt $lines.Length; $i++) {
        $line = $lines[$i]
        $lineNo = $i + 1

        if (-not $inTransaction -and $line -match $transactionStartPattern) {
            $inTransaction = $true
            $transactionStartLine = $lineNo
            $braceBalance = 0
        }

        if ($inTransaction) {
            if ($line -match $externalEffectPattern) {
                Add-Finding "error" "transaction-external-effect" $rel $lineNo `
                    ("Telegram or ABS external calls must not run inside database transaction callbacks; transaction starts at line {0}." -f $transactionStartLine) $line
            }

            foreach ($ch in $line.ToCharArray()) {
                if ($ch -eq '{') {
                    $braceBalance++
                } elseif ($ch -eq '}') {
                    $braceBalance--
                }
            }

            if ($lineNo -gt $transactionStartLine -and $braceBalance -le 0) {
                $inTransaction = $false
                $transactionStartLine = 0
                $braceBalance = 0
            }
        }
    }
}

$pointTypes = @{}
foreach ($file in $goFiles) {
    $content = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8
    $rel = Get-RelPath $file.FullName
    foreach ($match in [regex]::Matches($content, 'applyPointDeltaInTx\([^)]*?\,\s*"([^"]+)"')) {
        $typeName = $match.Groups[1].Value
        if (-not $pointTypes.ContainsKey($typeName)) {
            $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
            $pointTypes[$typeName] = [pscustomobject]@{
                type = $typeName
                file = $rel
                line = $lineNo
            }
        }
    }
    $fileLines = Get-Content -LiteralPath $file.FullName -Encoding UTF8
    for ($i = 0; $i -lt $fileLines.Length; $i++) {
        if ($fileLines[$i] -match '^\s*Type:\s*"([^"]+)"') {
            $typeName = $Matches[1]
            $start = [Math]::Max(0, $i - 8)
            $window = ($fileLines[$start..$i] -join "`n")
            if ($window -match 'PointTransaction\s*{' -and -not $pointTypes.ContainsKey($typeName)) {
                $pointTypes[$typeName] = [pscustomobject]@{
                    type = $typeName
                    file = $rel
                    line = $i + 1
                }
            }
        }
    }
}

$stateMachine = Join-Path $rootPath "state_machine.go"
if (Test-Path -LiteralPath $stateMachine) {
    $stateContent = Get-Content -LiteralPath $stateMachine -Raw -Encoding UTF8
    foreach ($entry in $pointTypes.GetEnumerator()) {
        $type = $entry.Key
        $source = $entry.Value
        if ($stateContent -notmatch [regex]::Escape('case "' + $type + '"')) {
            Add-Finding "warn" "point-type-display" $source.file $source.line `
                "Point transaction type is used but missing from pointTransactionTypeText." $type
        }
    }

    $highRiskSetMatch = [regex]::Match($stateContent, 'var\s+highRiskAuditActionSet\s*=\s*map\[string\]struct\{\}\s*\{(?<body>[\s\S]*?)\n\}')
    $highRiskActions = New-Object System.Collections.Generic.HashSet[string]
    if ($highRiskSetMatch.Success) {
        foreach ($match in [regex]::Matches($highRiskSetMatch.Groups['body'].Value, '"([^"]+)"\s*:')) {
            [void]$highRiskActions.Add($match.Groups[1].Value)
        }
    }
    $highRiskActionPattern = '^(ADJUST_|SET_|GENERATE_|RESERVE_INVITE_CODE|RELEASE_INVITE_CODE|USE_INVITE_CODE|USE_RENEW_CODE|BIND_USER|REBIND_USER|UNBIND_USER|MANUAL_BACKUP|SIMULATE_|SUSPEND_|UNSUSPEND_|AUTO_SUSPEND_|RENEW_REACTIVATE_|SELF_DELETE_|AUTO_DELETE_|FORCE_DELETE_|CLEAN_|CREATE_LOTTERY|FORCE_DRAW_LOTTERY|CANCEL_LOTTERY|RELOAD_CULTIVATION_|UPDATE_.*(?:CONFIG|THRESHOLD))'

    foreach ($file in $goFiles) {
        $content = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8
        $rel = Get-RelPath $file.FullName
        foreach ($match in [regex]::Matches($content, 'writeAuditLog(?:WithDelta)?\([^,\r\n]+,\s*"([^"]+)"')) {
            $action = $match.Groups[1].Value
            if ($action -match $highRiskActionPattern) {
                if (-not $highRiskActions.Contains($action)) {
                    $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                    Add-Finding "error" "high-risk-audit-action" $rel $lineNo `
                        "High-risk AuditLog action is written but missing from highRiskAuditActionSet." $action
                }
            }
        }
        foreach ($match in [regex]::Matches($content, 'writeAuditLogInTx\s*\(\s*[\w\.]+\s*,\s*[\w\.]+\s*,\s*"([^"]+)"')) {
            $action = $match.Groups[1].Value
            if ($action -match $highRiskActionPattern) {
                if (-not $highRiskActions.Contains($action)) {
                    $lineNo = (($content.Substring(0, $match.Index) -split "`n").Count)
                    Add-Finding "error" "high-risk-audit-action" $rel $lineNo `
                        "High-risk transactional AuditLog action is written but missing from highRiskAuditActionSet." $action
                }
            }
        }
    }
}

$findingArray = @($findings.ToArray())

$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root         = $rootPath
    error_count  = @($findingArray | Where-Object { $_.severity -eq "error" }).Count
    warn_count   = @($findingArray | Where-Object { $_.severity -eq "warn" }).Count
    findings     = $findingArray
}

$json = $summary | ConvertTo-Json -Depth 6
if ($Out -ne "") {
    $outPath = Join-Path $rootPath $Out
    $outDir = Split-Path -Parent $outPath
    if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
        New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    }
    Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8
}

Write-Output $json

if (-not $NoFail -and $summary.error_count -gt 0) {
    exit 1
}
