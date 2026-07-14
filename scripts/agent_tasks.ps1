param(
    [string]$Root = ".",
    [string]$AuditReport = "reports/agent_audit.json",
    [string]$UnattendedReport = "reports/agent_unattended.json",
    [string]$DoctorReport = "reports/agent_doctor.json",
    [string]$ToolchainReport = "reports/agent_toolchain.json",
    [string]$Baseline = "docs/agent/audit_baseline.json",
    [string]$Out = "reports/agent_tasks.json",
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$tasks = New-Object System.Collections.Generic.List[object]
$acceptedReviewIds = New-Object System.Collections.Generic.HashSet[string]
$acceptedReviewSignatureCounts = @{}

function Read-JsonFile {
    param([string]$Path)

    $fullPath = Join-Path $rootPath $Path
    if (-not (Test-Path -LiteralPath $fullPath)) {
        return $null
    }
    return Get-Content -LiteralPath $fullPath -Raw | ConvertFrom-Json
}

function Normalize-ReviewField {
    param([object]$Value)
    if ($null -eq $Value) {
        return ""
    }
    return ([string]$Value).Trim()
}

function New-ReviewSignature {
    param(
        [object]$Source,
        [object]$Kind,
        [object]$Title,
        [object]$File,
        [object]$Evidence
    )

    $parts = @(
        "v1",
        (Normalize-ReviewField $Source),
        (Normalize-ReviewField $Kind),
        (Normalize-ReviewField $Title),
        (Normalize-ReviewField $File),
        (Normalize-ReviewField $Evidence)
    )
    if ($parts[1] -eq "" -or $parts[2] -eq "" -or $parts[3] -eq "" -or $parts[4] -eq "" -or $parts[5] -eq "") {
        return ""
    }
    return ($parts -join ([string][char]31))
}

function Add-Count {
    param(
        [hashtable]$Map,
        [string]$Key
    )
    if ([string]::IsNullOrWhiteSpace($Key)) {
        return
    }
    if ($Map.ContainsKey($Key)) {
        $Map[$Key] = [int]$Map[$Key] + 1
    } else {
        $Map[$Key] = 1
    }
}

$baselineDoc = Read-JsonFile $Baseline
if ($null -ne $baselineDoc) {
    foreach ($accepted in @($baselineDoc.accepted_reviews)) {
        if ($accepted.id) {
            [void]$acceptedReviewIds.Add([string]$accepted.id)
        }
        $signature = New-ReviewSignature `
            -Source $accepted.source `
            -Kind $accepted.kind `
            -Title $accepted.title `
            -File $accepted.file `
            -Evidence $accepted.evidence
        Add-Count $acceptedReviewSignatureCounts $signature
    }
}

function Add-Task {
    param(
        [string]$Source,
        [string]$Kind,
        [string]$Severity,
        [string]$Title,
        [string]$File,
        [int]$Line,
        [string]$Evidence,
        [string]$Action,
        [string]$Status
    )

    $keyParts = @($Source, $Kind, $Title, $File, $Line, $Evidence) | ForEach-Object {
        if ($null -eq $_) { "" } else { [string]$_ }
    }
    $rawKey = ($keyParts -join "|")
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($rawKey)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $hash = [System.BitConverter]::ToString($sha.ComputeHash($bytes)).Replace("-", "").Substring(0, 16).ToLowerInvariant()

    $taskId = "agent-$hash"

    $script:tasks.Add([pscustomobject]@{
        id        = $taskId
        source    = $Source
        kind      = $Kind
        severity  = $Severity
        title     = $Title
        file      = $File
        line      = $Line
        evidence  = $Evidence
        action    = $Action
        status    = $Status
    })
}

$audit = Read-JsonFile $AuditReport
$toolchain = Read-JsonFile $ToolchainReport
if ($null -ne $audit) {
    foreach ($finding in @($audit.findings)) {
        $severity = [string]$finding.severity
        $status = "review"
        $action = "Review the finding and decide whether to fix, whitelist, or document the guard."
        if ($severity -eq "error") {
            $status = "todo"
            $action = "Fix before unattended validation can be considered healthy."
        }

        switch ([string]$finding.rule) {
            "points-ledger" {
                $action = "Route the points change through applyPointDeltaInTx so balance changes and PointTransaction rows stay atomic and consistent."
            }
            "point-transaction-helper" {
                $action = "Create PointTransaction rows through applyPointDeltaInTx so ledger writes cannot drift from the matching balance update."
            }
            "inventory-negative" {
                $action = "Add a database-level non-negative condition or an atomic upsert/update guard before decrementing quantity."
            }
            "inventory-upsert-helper" {
                $action = "Route Inventory grants through inventoryQuantityUpsertClause so all backpack item grants share the same user/item upsert semantics."
            }
            "audit-log-helper" {
                $action = "Create AuditLog rows through writeAuditLogInTx or writeAuditLog so target/detail redaction is applied consistently."
            }
            "audit-storage-display-format" {
                $action = "Route AuditLog target/detail storage through formatAuditTextForDisplay so sensitive text is redacted and disruptive control characters are normalized."
            }
            "audit-display-format" {
                $action = "Use formatAuditTextForDisplay when rendering AuditLog target/detail so historical values are redacted and normalized consistently."
            }
            "diagnostic-display-format" {
                $action = "Route formatMarkdownError and formatPlainValue through formatDiagnosticTextForDisplay so diagnostic text is redacted, length-limited, and normalized consistently."
            }
            "system-config-error-display-format" {
                $action = "Render persisted SystemConfig error fields through formatSystemConfigErrorForMarkdown so old raw values are redacted, normalized, length-limited, and Markdown-escaped."
            }
            "callback-display-format" {
                $action = "Route answerCallback text through formatCallbackAlertText so callback alerts are redacted, normalized, and kept within Telegram limits."
            }
            "telegram-send-error-format" {
                $action = "Log shared Telegram send helper failures through formatTelegramSendError so returned error text is redacted and normalized."
            }
            "destructive-data-op" {
                $action = "Confirm this destructive path is guarded, scoped, documented, and not reachable without the intended authority."
            }
            "abs-delete-review" {
                $action = "Keep ABS deletion behind lifecycle or super-admin controls, with audit logging where applicable."
            }
            "abs-delete-rollback-review" {
                $action = "Keep rollback deletion limited to compensating a failed local registration write."
            }
            "secret-log" {
                $action = "Remove or mask the sensitive value before logging."
            }
            "point-type-display" {
                $action = "Add the transaction type to pointTransactionTypeText so user-visible flow records are readable."
            }
            "security-attempt-helper" {
                $action = "Route SecurityAttemptLock failure creation through recordSecurityAttemptFailureInTx so concurrent first failures are counted correctly."
            }
            "admin-reason-control-validation" {
                $action = "Keep validateAdminReason rejecting control characters so high-risk operation reasons cannot break confirmations, audit logs, or notifications."
            }
            "admin-reason-prompt-validation" {
                $action = "Use adminReasonRequirementText or adminReasonInvalidText in high-risk operation reason prompts so admin-facing text matches validateAdminReason."
            }
            "sect-name-control-validation" {
                $action = "Keep validateSectName rejecting control characters so sect names cannot break panels, leaderboards, ledgers, or operational readability."
            }
            "sect-name-prompt-validation" {
                $action = "Use sectNameInvalidText in sect-name prompts so user-facing text matches validateSectName."
            }
            "xmly-link-control-validation" {
                $action = "Keep validateXmlyLink rejecting control characters so book request links cannot break tickets, notifications, audit logs, or operational readability."
            }
            "xmly-link-prompt-validation" {
                $action = "Use bookRequestLinkRequirementText in book request link prompts so user-facing text matches validateXmlyLink."
            }
            "book-request-note-control-validation" {
                $action = "Keep validateBookRequestNote rejecting control characters so book request notes cannot break tickets, notifications, logs, or operational readability."
            }
            "server-lines-control-validation" {
                $action = "Keep validateServerLinesContent rejecting control characters other than newlines so server line panels and Markdown output stay readable."
            }
            "server-lines-upsert-validation" {
                $action = "Validate server_lines content with validateServerLinesContent immediately before persisting the SystemConfig value."
            }
            "book-request-user-reply-cas" {
                $action = "Route need_info user replies through markBookRequestUserReplied and only write user_reply logs after the conditional update succeeds."
            }
            "book-request-finish-reload-fallback" {
                $action = "Call reloadBookRequestAfterFinish before writing finish logs or notifying users so successful finish updates keep the written status even if reload fails."
            }
            "book-request-need-info-reload-fallback" {
                $action = "Call reloadBookRequestAfterNeedInfo after need_info logs so successful need-info updates still notify users even if reload fails."
            }
            "book-request-admin-note-reload-fallback" {
                $action = "Call reloadBookRequestAfterAdminNote after admin_note logs so successful admin-note updates do not falsely report message refresh if reload fails."
            }
            "telegram-username-mention-markdown" {
                $action = "Build Telegram username mentions with telegramUsernameMentionMarkdown so Markdown metacharacters in usernames are escaped consistently."
            }
            "telegram-error-helper" {
                $action = "Use isTelegramMessageNotModifiedError or isTerminalTelegramDeleteError instead of ad hoc Telegram err.Error string matching."
            }
            "lottery-title-validation" {
                $action = "Validate lottery activity titles with validLotteryTitle so newlines, tabs, and control characters cannot break announcements, lists, point ledgers, or audit readability."
            }
            "lottery-title-prompt-validation" {
                $action = "Use lotteryTitleRequirementText in lottery title prompts so admin-facing text matches validLotteryTitle."
            }
            "lottery-claim-code-validation" {
                $action = "Validate lottery claim codes with validLotteryClaimCode so newlines, tabs, and control characters cannot break winner reminders or operational readability."
            }
            "lottery-claim-code-prompt-validation" {
                $action = "Use lotteryClaimCodeRequirementText in lottery claim-code prompts so admin-facing text matches validLotteryClaimCode."
            }
            "marketplace-secret-listing-name-prompt-validation" {
                $action = "Use marketplaceSecretListingNameRequirementText in marketplace secret listing-name prompts so user-facing text matches validMarketplaceSecretListingName."
            }
            "marketplace-inventory-item-name-prompt-validation" {
                $action = "Use marketplaceInventoryItemNameRequirementText in marketplace inventory item-name prompts so user-facing text matches validMarketplaceInventoryItemName."
            }
            "inventory-item-markdown-name" {
                $action = "Wrap inventory, shop, session, or breakthrough pill item names with inventoryItemMarkdownName before inserting them into Markdown messages; keep raw names only for buttons, callbacks, storage, and asset keys."
            }
            "garden-item-markdown-name" {
                $action = "Wrap garden seed, herb, material, pill, and recipe names with inventoryItemMarkdownName or escapeMarkdown before inserting them into Markdown panels; keep raw names for buttons, callbacks, storage, and asset keys."
            }
            "partial-index-upsert" {
                $action = "For SQLite partial unique indexes, add a matching TargetWhere predicate to the OnConflict target, or use plain ON CONFLICT DO NOTHING when no update is required."
            }
        }

        Add-Task `
            -Source "agent_audit" `
            -Kind ([string]$finding.rule) `
            -Severity $severity `
            -Title ([string]$finding.message) `
            -File ([string]$finding.file) `
            -Line ([int]$finding.line) `
            -Evidence ([string]$finding.evidence) `
            -Action $action `
            -Status $status
    }
}

$unattended = Read-JsonFile $UnattendedReport
if ($null -ne $unattended) {
    foreach ($step in @($unattended.steps)) {
        if ([string]$step.name -eq "agent_tasks") {
            continue
        }

        if ([string]$step.status -eq "skipped") {
            if ($null -ne $toolchain -and [string]$step.name -in @("gofmt", "go_test", "go_build")) {
                continue
            }
            Add-Task `
                -Source "agent_unattended" `
                -Kind "missing-tool" `
                -Severity "error" `
                -Title ("Required validation step skipped: {0}" -f $step.name) `
                -File "" `
                -Line 0 `
                -Evidence ([string]$step.reason) `
                -Action ("Install or expose the required tool, then rerun: {0}" -f $step.command) `
                -Status "todo"
        } elseif ([string]$step.status -eq "failed") {
            $kind = "validation-failed"
            $action = ("Fix the failure and rerun: {0}" -f $step.command)
            if ([string]$step.name -eq "bootstrap_go") {
                $kind = "toolchain-bootstrap-failed"
                $action = "Retry scripts\agent_bootstrap_go.ps1 with -ArchivePath for a local Go zip, -DownloadUrl plus -SHA256 for a reachable mirror, or install Go/Docker outside the project."
            } elseif ([string]$step.name -eq "agent_repair") {
                $kind = "automation-repair-failed"
                $action = "Review reports/agent_repair.json and fix failed cleanup actions."
            }
            Add-Task `
                -Source "agent_unattended" `
                -Kind $kind `
                -Severity "error" `
                -Title ("Validation step failed: {0}" -f $step.name) `
                -File "" `
                -Line 0 `
                -Evidence ([string]$step.output) `
                -Action $action `
                -Status "todo"
        }
    }
}

$doctor = Read-JsonFile $DoctorReport
if ($null -ne $doctor) {
    foreach ($finding in @($doctor.findings)) {
        $severity = [string]$finding.severity
        $status = "review"
        $action = "Review and fix the unattended automation health finding."
        if ($severity -eq "error") {
            $status = "todo"
            $action = "Fix this automation health error before unattended validation can be trusted."
        }
        Add-Task `
            -Source "agent_doctor" `
            -Kind ([string]$finding.rule) `
            -Severity $severity `
            -Title ([string]$finding.message) `
            -File ([string]$finding.path) `
            -Line 0 `
            -Evidence ([string]$finding.message) `
            -Action $action `
            -Status $status
    }
}

if ($null -ne $toolchain) {
    foreach ($issue in @($toolchain.issues)) {
        $severity = [string]$issue.severity
        $status = "review"
        if ($severity -eq "error") {
            $status = "todo"
        }
        Add-Task `
            -Source "agent_toolchain" `
            -Kind ([string]$issue.kind) `
            -Severity $severity `
            -Title ([string]$issue.message) `
            -File "" `
            -Line 0 `
            -Evidence ([string]$issue.message) `
            -Action ([string]$issue.action) `
            -Status $status
    }
}

$currentReviewSignatureCounts = @{}
foreach ($task in @($tasks.ToArray())) {
    if ([string]$task.status -ne "review") {
        continue
    }
    $signature = New-ReviewSignature `
        -Source $task.source `
        -Kind $task.kind `
        -Title $task.title `
        -File $task.file `
        -Evidence $task.evidence
    Add-Count $currentReviewSignatureCounts $signature
}

foreach ($task in @($tasks.ToArray())) {
    if ([string]$task.status -ne "review") {
        continue
    }
    if ($acceptedReviewIds.Contains([string]$task.id)) {
        $task.status = "accepted"
        continue
    }

    $signature = New-ReviewSignature `
        -Source $task.source `
        -Kind $task.kind `
        -Title $task.title `
        -File $task.file `
        -Evidence $task.evidence
    if ($signature -ne "" `
        -and $acceptedReviewSignatureCounts.ContainsKey($signature) `
        -and $currentReviewSignatureCounts.ContainsKey($signature) `
        -and [int]$acceptedReviewSignatureCounts[$signature] -eq [int]$currentReviewSignatureCounts[$signature]) {
        $task.status = "accepted"
    }
}

$taskArray = @($tasks.ToArray() | Sort-Object severity, kind, file, line, id)
$summary = [pscustomobject]@{
    generated_at   = (Get-Date).ToString("o")
    root           = $rootPath
    baseline       = $Baseline
    task_count     = $taskArray.Count
    todo_count     = @($taskArray | Where-Object { $_.status -eq "todo" }).Count
    review_count   = @($taskArray | Where-Object { $_.status -eq "review" }).Count
    accepted_count = @($taskArray | Where-Object { $_.status -eq "accepted" }).Count
    tasks          = $taskArray
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

$json = $summary | ConvertTo-Json -Depth 8
Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8
Write-Output $json

if (-not $NoFail -and $summary.todo_count -gt 0) {
    exit 1
}
