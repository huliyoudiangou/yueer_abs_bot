param(
    [string]$Root = ".",
    [string]$Out = "reports/agent_gate.json",
    [string]$Latest = "reports/agent_watch_latest.json",
    [string]$StatusReport = "reports/agent_status.json",
    [string]$SummaryReport = "reports/agent_summary.md",
    [int]$WatchTimeoutSeconds = 900,
    [int]$MaxReportAgeMinutes = 1440,
    [switch]$RunWatch,
    [switch]$BootstrapGo,
    [switch]$BootstrapUseWingetMsi,
    [switch]$UseDockerGo,
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$requiredSteps = @("agent_repair", "agent_toolchain", "gofmt", "agent_audit", "go_test", "go_build", "agent_doctor", "agent_tasks")
$issues = New-Object System.Collections.Generic.List[object]
$watchExitCode = $null

function Add-Issue {
    param(
        [string]$Severity,
        [string]$Kind,
        [string]$Message,
        [string]$Action
    )

    $script:issues.Add([pscustomobject]@{
        severity = $Severity
        kind     = $Kind
        message  = $Message
        action   = $Action
    })
}

function Read-JsonFile {
    param([string]$Path)

    $fullPath = Join-Path $rootPath $Path
    if (-not (Test-Path -LiteralPath $fullPath)) {
        return $null
    }

    return Get-Content -LiteralPath $fullPath -Raw | ConvertFrom-Json
}

function Invoke-WatchOnce {
    $watchScript = Join-Path $rootPath "scripts\agent_watch.ps1"
    if (-not (Test-Path -LiteralPath $watchScript)) {
        Add-Issue "error" "missing-watch-script" "scripts/agent_watch.ps1 is missing." "Restore the automation scripts before running the gate."
        return 127
    }

    $args = @(
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-File", $watchScript,
        "-Root", $rootPath,
        "-Iterations", "1",
        "-IntervalSeconds", "1",
        "-Latest", $Latest,
        "-StatusReport", $StatusReport
    )
    if ($BootstrapGo) {
        $args += "-BootstrapGo"
    }
    if ($BootstrapUseWingetMsi) {
        $args += "-BootstrapUseWingetMsi"
    }
    if ($UseDockerGo) {
        $args += "-UseDockerGo"
    }

    $started = Get-Date
    $proc = Start-Process -FilePath "powershell" `
        -ArgumentList $args `
        -WorkingDirectory $rootPath `
        -WindowStyle Hidden `
        -PassThru
    $finished = $proc.WaitForExit($WatchTimeoutSeconds * 1000)
    if (-not $finished) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Add-Issue "error" "watch-timeout" "agent_watch did not finish within $WatchTimeoutSeconds seconds." "Inspect reports/agent_watch.lock and rerun with a larger -WatchTimeoutSeconds if needed."
        return 124
    }

    $elapsed = [int64]((Get-Date) - $started).TotalMilliseconds
    if ($proc.ExitCode -ne 0) {
        Add-Issue "error" "watch-exit-code" "agent_watch exited with code $($proc.ExitCode) after $elapsed ms." "Inspect reports/agent_watch_latest.json and reports/agent_status.json."
    }
    return $proc.ExitCode
}

if ($RunWatch) {
    $watchExitCode = Invoke-WatchOnce
}

$statusScript = Join-Path $rootPath "scripts\agent_status.ps1"
if (Test-Path -LiteralPath $statusScript) {
    & powershell -NoProfile -ExecutionPolicy Bypass -File $statusScript -Root $rootPath -Latest $Latest -Out $StatusReport -NoFail | Out-Null
} else {
    Add-Issue "error" "missing-status-script" "scripts/agent_status.ps1 is missing." "Restore the automation scripts before running the gate."
}

$statusData = Read-JsonFile $StatusReport
$latestData = Read-JsonFile $Latest

if ($null -eq $statusData) {
    Add-Issue "error" "missing-status-report" "Status report is missing: $StatusReport." "Run scripts/agent_watch.ps1 or scripts/agent_status.ps1."
} else {
    if ([int]$statusData.exit_code -ne 0) {
        Add-Issue "error" "status-exit-code" "agent_status reports exit_code=$($statusData.exit_code)." "Resolve status issues before treating the repo as unattended healthy."
    }
    if ($statusData.status -ne "healthy") {
        Add-Issue "error" "watch-status" "Latest watch status is '$($statusData.status)'." "Inspect $Latest and the latest task report."
    }
    if ($null -ne $statusData.todo_count -and [int]$statusData.todo_count -gt 0) {
        Add-Issue "error" "open-todos" "Task queue has $($statusData.todo_count) todo item(s)." "Resolve todo tasks before release or autonomous continuation."
    }
    if ($null -ne $statusData.review_count -and [int]$statusData.review_count -gt 0) {
        Add-Issue "warn" "open-reviews" "Task queue has $($statusData.review_count) review item(s)." "Review, fix, or baseline accepted review findings."
    }
    if ($null -ne $statusData.toolchain_ready -and -not [bool]$statusData.toolchain_ready) {
        Add-Issue "error" "toolchain-not-ready" "Go toolchain is not ready." "Provide Go through .tools/offline, -BootstrapGo, Docker, or approved system install."
    }

    foreach ($stepName in $requiredSteps) {
        $step = @($statusData.required_steps | Where-Object { $_.name -eq $stepName } | Select-Object -First 1)
        if ($step.Count -eq 0) {
            Add-Issue "error" "missing-step" "Required step '$stepName' is missing." "Rerun scripts/agent_watch.ps1 and inspect the unattended report."
            continue
        }
        if ($step[0].status -ne "passed") {
            Add-Issue "error" "step-not-passed" "Required step '$stepName' status is '$($step[0].status)'." $step[0].reason
        }
    }
}

if ($null -eq $latestData) {
    Add-Issue "error" "missing-latest-report" "Latest watch report is missing: $Latest." "Run scripts/agent_watch.ps1 -Iterations 1."
} elseif ($MaxReportAgeMinutes -gt 0) {
    try {
        $latestGeneratedAt = [datetime]$latestData.generated_at
        $ageMinutes = ((Get-Date) - $latestGeneratedAt).TotalMinutes
        if ($ageMinutes -gt $MaxReportAgeMinutes) {
            Add-Issue "error" "stale-latest-report" "Latest watch report is $([math]::Round($ageMinutes, 1)) minute(s) old." "Run scripts/agent_gate.ps1 -RunWatch or scripts/agent_watch.ps1 -Iterations 1."
        }
    } catch {
        Add-Issue "error" "invalid-latest-timestamp" "Latest watch report has an invalid generated_at value." "Rerun scripts/agent_watch.ps1 -Iterations 1."
    }
}

$issueArray = @($issues.ToArray())
$errorCount = @($issueArray | Where-Object { $_.severity -eq "error" }).Count
$warnCount = @($issueArray | Where-Object { $_.severity -eq "warn" }).Count
$gateStatus = if ($errorCount -eq 0) { "pass" } else { "fail" }
$exitCode = if ($errorCount -eq 0) { 0 } else { 1 }

$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root         = $rootPath
    status       = $gateStatus
    exit_code    = $exitCode
    run_watch    = [bool]$RunWatch
    watch_exit_code = $watchExitCode
    max_report_age_minutes = $MaxReportAgeMinutes
    latest       = $Latest
    status_report = $StatusReport
    toolchain_ready = if ($null -eq $statusData) { $null } else { $statusData.toolchain_ready }
    todo_count   = if ($null -eq $statusData) { $null } else { $statusData.todo_count }
    review_count = if ($null -eq $statusData) { $null } else { $statusData.review_count }
    accepted_count = if ($null -eq $statusData) { $null } else { $statusData.accepted_count }
    required_steps = if ($null -eq $statusData) { @() } else { $statusData.required_steps }
    error_count  = $errorCount
    warn_count   = $warnCount
    issues       = $issueArray
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

$json = $summary | ConvertTo-Json -Depth 8
Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8

$reportScript = Join-Path $rootPath "scripts\agent_report.ps1"
if (Test-Path -LiteralPath $reportScript) {
    & powershell -NoProfile -ExecutionPolicy Bypass -File $reportScript -Root $rootPath -Status $StatusReport -Gate $Out -Out $SummaryReport | Out-Null
}

Write-Output $json

if (-not $NoFail -and $exitCode -ne 0) {
    exit $exitCode
}
