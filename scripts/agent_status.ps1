param(
    [string]$Root = ".",
    [string]$Latest = "reports/agent_watch_latest.json",
    [string]$Tasks = "reports/agent_tasks.json",
    [string]$Toolchain = "reports/agent_toolchain.json",
    [string]$Out = "reports/agent_status.json",
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path

function Read-JsonFile {
    param([string]$Path)

    $fullPath = Join-Path $rootPath $Path
    if (-not (Test-Path -LiteralPath $fullPath)) {
        return $null
    }
    return Get-Content -LiteralPath $fullPath -Raw | ConvertFrom-Json
}

function Get-Step {
    param(
        [object]$LatestReport,
        [string]$Name
    )

    if ($null -eq $LatestReport -or $null -eq $LatestReport.last_run -or $null -eq $LatestReport.last_run.steps) {
        return $null
    }

    return @($LatestReport.last_run.steps | Where-Object { $_.name -eq $Name } | Select-Object -First 1)
}

$latestReport = Read-JsonFile $Latest
$effectiveTasks = $Tasks
if ($null -ne $latestReport -and $null -ne $latestReport.last_run -and $latestReport.last_run.task_report_path) {
    $effectiveTasks = [string]$latestReport.last_run.task_report_path
}
$taskReport = Read-JsonFile $effectiveTasks
$effectiveToolchain = $Toolchain
if ($null -ne $latestReport -and $null -ne $latestReport.last_run -and $null -ne $latestReport.last_run.report_path) {
    $runReportPath = [string]$latestReport.last_run.report_path
    $runToolchainPath = $runReportPath -replace 'agent_unattended_', 'agent_toolchain_'
    if ($runToolchainPath -ne $runReportPath) {
        $effectiveToolchain = $runToolchainPath
    }
}
$toolchainReport = Read-JsonFile $effectiveToolchain

$issues = New-Object System.Collections.Generic.List[object]
$exitCode = 0
$status = "unknown"

if ($null -eq $latestReport) {
    $issues.Add([pscustomobject]@{
        severity = "error"
        kind     = "missing-latest"
        message  = "Latest watch report is missing."
        action   = "Run scripts\agent_watch.ps1 or scripts\agent_unattended.ps1."
    })
    $exitCode = 1
} else {
    $status = [string]$latestReport.status
    if ($status -ne "healthy") {
        $issues.Add([pscustomobject]@{
            severity = "error"
            kind     = "watch-status"
            message  = "Latest watch status is '$status'."
            action   = "Inspect reports/agent_watch_latest.json and reports/agent_tasks.json."
        })
        $exitCode = 1
    }
}

if ($null -eq $taskReport) {
    $issues.Add([pscustomobject]@{
        severity = "warn"
        kind     = "missing-tasks"
        message  = "Task report is missing."
        action   = "Run scripts\agent_tasks.ps1 -NoFail."
    })
} else {
    if ([int]$taskReport.todo_count -gt 0) {
        $issues.Add([pscustomobject]@{
            severity = "error"
            kind     = "open-todos"
            message  = ("Task queue has {0} todo item(s)." -f [int]$taskReport.todo_count)
            action   = "Resolve todo tasks before treating unattended validation as healthy."
        })
        $exitCode = 1
    }
    if ([int]$taskReport.review_count -gt 0) {
        $issues.Add([pscustomobject]@{
            severity = "warn"
            kind     = "open-reviews"
            message  = ("Task queue has {0} review item(s)." -f [int]$taskReport.review_count)
            action   = "Review, fix, or explicitly baseline accepted review findings."
        })
    }
}

if ($null -ne $toolchainReport -and -not [bool]$toolchainReport.ready) {
    $issues.Add([pscustomobject]@{
        severity = "error"
        kind     = "toolchain-not-ready"
        message  = "Go toolchain is not ready."
        action   = "Inspect $effectiveToolchain and provide Go via .tools/offline, -BootstrapArchivePath, -BootstrapDownloadUrl, Docker, or system install."
    })
    $exitCode = 1
}

$requiredSteps = @("agent_repair", "agent_toolchain", "gofmt", "agent_audit", "go_test", "go_build", "agent_doctor", "agent_tasks")
$stepStatus = @()
foreach ($name in $requiredSteps) {
    $step = Get-Step -LatestReport $latestReport -Name $name
    if ($null -eq $step -or $step.Count -eq 0) {
        $stepStatus += [pscustomobject]@{
            name = $name
            status = "missing"
            reason = "Step is absent from latest report."
        }
        $issues.Add([pscustomobject]@{
            severity = "error"
            kind     = "missing-step"
            message  = "Required step '$name' is missing from latest report."
            action   = "Rerun scripts\agent_watch.ps1 and inspect the unattended report."
        })
        $exitCode = 1
        continue
    }

    $stepStatus += [pscustomobject]@{
        name = $name
        status = [string]$step[0].status
        reason = [string]$step[0].reason
    }
}

$summary = [pscustomobject]@{
    generated_at   = (Get-Date).ToString("o")
    root           = $rootPath
    status         = $status
    exit_code      = $exitCode
    latest         = $Latest
    tasks          = $effectiveTasks
    toolchain      = $effectiveToolchain
    toolchain_status = if ($null -eq $toolchainReport) { $null } else { [string]$toolchainReport.status }
    toolchain_ready = if ($null -eq $toolchainReport) { $null } else { [bool]$toolchainReport.ready }
    todo_count     = if ($null -eq $taskReport) { $null } else { [int]$taskReport.todo_count }
    review_count   = if ($null -eq $taskReport) { $null } else { [int]$taskReport.review_count }
    accepted_count = if ($null -eq $taskReport) { $null } else { [int]$taskReport.accepted_count }
    last_run       = if ($null -eq $latestReport) { $null } else { $latestReport.last_run }
    required_steps = $stepStatus
    issues         = @($issues.ToArray())
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

$json = $summary | ConvertTo-Json -Depth 8
Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8
Write-Output $json

if (-not $NoFail -and $exitCode -ne 0) {
    exit $exitCode
}
