param(
    [string]$Root = ".",
    [int]$IntervalSeconds = 3600,
    [int]$Iterations = 0,
    [string]$HistoryDir = "reports/agent_runs",
    [string]$Latest = "reports/agent_watch_latest.json",
    [string]$LockFile = "reports/agent_watch.lock",
    [int]$RetainRuns = 100,
    [int]$RetainDays = 0,
    [int]$StaleLockMinutes = 720,
    [string]$StatusReport = "reports/agent_status.json",
    [string]$SummaryReport = "reports/agent_summary.md",
    [int]$BootstrapTimeoutSeconds = 300,
    [string]$BootstrapArchivePath = "",
    [string]$BootstrapOfflineDir = ".tools/offline",
    [string]$BootstrapDownloadUrl = "",
    [string]$BootstrapSHA256 = "",
    [string]$BootstrapBaseUrls = "",
    [string]$BootstrapFallbackVersions = "",
    [int]$BootstrapMaxCandidates = 6,
    [switch]$NoFormat,
    [switch]$BootstrapGo,
    [switch]$BootstrapIncludeAll,
    [switch]$BootstrapNoAutoOffline,
    [switch]$BootstrapUseBits,
    [switch]$BootstrapUseWingetMsi,
    [switch]$UseDockerGo,
    [switch]$ForceUnlock,
    [switch]$Strict
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$historyPath = Join-Path $rootPath $HistoryDir
$latestPath = Join-Path $rootPath $Latest
$lockPath = Join-Path $rootPath $LockFile
$watchStartedAt = Get-Date
$watchRunId = "{0}_{1}" -f ($watchStartedAt.ToString("yyyyMMdd_HHmmss")), $PID
$runCount = 0
$lastExit = 0
$lastCleanup = $null
$lockOwned = $false

if (-not (Test-Path -LiteralPath $historyPath)) {
    New-Item -ItemType Directory -Force -Path $historyPath | Out-Null
}

$latestDir = Split-Path -Parent $latestPath
if ($latestDir -and -not (Test-Path -LiteralPath $latestDir)) {
    New-Item -ItemType Directory -Force -Path $latestDir | Out-Null
}

function Test-ProcessAlive {
    param([int]$ProcessId)

    if ($ProcessId -le 0) {
        return $false
    }

    return $null -ne (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)
}

function Get-LockInfo {
    if (-not (Test-Path -LiteralPath $lockPath)) {
        return $null
    }

    try {
        return Get-Content -LiteralPath $lockPath -Raw | ConvertFrom-Json
    } catch {
        return [pscustomobject]@{
            pid = 0
            started_at = ""
            watch_run_id = ""
            invalid = $true
        }
    }
}

function New-LockPayload {
    return [pscustomobject]@{
        pid          = $PID
        started_at   = $watchStartedAt.ToString("o")
        root         = $rootPath
        watch_run_id = $watchRunId
    } | ConvertTo-Json -Depth 4
}

function Acquire-WatchLock {
    $lockDir = Split-Path -Parent $lockPath
    if ($lockDir -and -not (Test-Path -LiteralPath $lockDir)) {
        New-Item -ItemType Directory -Force -Path $lockDir | Out-Null
    }

    while ($true) {
        try {
            $stream = [System.IO.File]::Open($lockPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
            try {
                $payload = [System.Text.Encoding]::UTF8.GetBytes((New-LockPayload))
                $stream.Write($payload, 0, $payload.Length)
                $stream.Flush()
            } finally {
                $stream.Dispose()
            }
            $script:lockOwned = $true
            return
        } catch [System.IO.IOException] {
            $info = Get-LockInfo
            $lockedPid = 0
            if ($null -ne $info -and $null -ne $info.pid) {
                $lockedPid = [int]$info.pid
            }

            $lockAgeMinutes = 0
            if ($null -ne $info -and $info.started_at) {
                try {
                    $lockAgeMinutes = ((Get-Date) - ([datetime]$info.started_at)).TotalMinutes
                } catch {
                    $lockAgeMinutes = $StaleLockMinutes + 1
                }
            } else {
                $lockAgeMinutes = $StaleLockMinutes + 1
            }

            $isAlive = Test-ProcessAlive -ProcessId $lockedPid
            $isStale = (-not $isAlive) -or ($StaleLockMinutes -gt 0 -and $lockAgeMinutes -gt $StaleLockMinutes)
            if ($ForceUnlock -or $isStale) {
                Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
                continue
            }

            $locked = [pscustomobject]@{
                generated_at = (Get-Date).ToString("o")
                root         = $rootPath
                status       = "locked"
                lock_file    = $LockFile
                lock         = $info
                message      = "agent_watch is already running."
            }
            $locked | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $latestPath -Encoding UTF8
            Write-Output ($locked | ConvertTo-Json -Depth 8)
            exit 2
        }
    }
}

function Release-WatchLock {
    if ($script:lockOwned) {
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
        $script:lockOwned = $false
    }
}

function Get-RunSummary {
    param(
        [string]$ReportPath,
        [string]$TaskReportPath,
        [int]$RunnerExitCode,
        [int]$Iteration
    )

    $report = $null
    $actualExitCode = $RunnerExitCode
    if (Test-Path -LiteralPath $ReportPath) {
        $report = Get-Content -LiteralPath $ReportPath -Raw | ConvertFrom-Json
        if ($null -ne $report.exit_code) {
            $actualExitCode = [int]$report.exit_code
        }
    }

    $steps = @()
    $failed = 0
    $skipped = 0
    $taskCount = 0
    $todoCount = 0
    $reviewCount = 0
    $acceptedCount = 0
    if ($null -ne $report) {
        $steps = @($report.steps | ForEach-Object {
            [pscustomobject]@{
                name      = $_.name
                status    = $_.status
                exit_code = $_.exit_code
                reason    = $_.reason
            }
        })
        $failed = [int]$report.failed_count
        $skipped = [int]$report.skipped_count

        $taskStep = @($report.steps | Where-Object { $_.name -eq "agent_tasks" } | Select-Object -First 1)
        if ($taskStep.Count -gt 0 -and $taskStep[0].output) {
            try {
                $taskReport = $taskStep[0].output | ConvertFrom-Json
                $taskCount = [int]$taskReport.task_count
                $todoCount = [int]$taskReport.todo_count
                $reviewCount = [int]$taskReport.review_count
                $acceptedCount = [int]$taskReport.accepted_count
            } catch {
                $taskCount = 0
                $todoCount = 0
                $reviewCount = 0
                $acceptedCount = 0
            }
        }
    }

    return [pscustomobject]@{
        iteration         = $Iteration
        generated_at      = (Get-Date).ToString("o")
        process_exit_code = $RunnerExitCode
        exit_code         = $actualExitCode
        report_path       = $ReportPath.Substring($rootPath.Length).TrimStart('\', '/')
        task_report_path  = $TaskReportPath.Substring($rootPath.Length).TrimStart('\', '/')
        failed_count      = $failed
        skipped_count     = $skipped
        task_count        = $taskCount
        todo_count        = $todoCount
        review_count      = $reviewCount
        accepted_count    = $acceptedCount
        steps             = $steps
    }
}

function Write-Latest {
    param([object]$LastRun)

    $status = "healthy"
    if ($LastRun.exit_code -ne 0) {
        $status = "needs_attention"
    }

    $latest = [pscustomobject]@{
        generated_at     = (Get-Date).ToString("o")
        root             = $rootPath
        status           = $status
        strict           = [bool]$Strict
        bootstrap_go     = [bool]$BootstrapGo
        bootstrap_archive_path = $BootstrapArchivePath
        bootstrap_download_url = $BootstrapDownloadUrl
        bootstrap_use_bits = [bool]$BootstrapUseBits
        bootstrap_use_winget_msi = [bool]$BootstrapUseWingetMsi
        use_docker_go    = [bool]$UseDockerGo
        watch_run_id     = $watchRunId
        interval_seconds = $IntervalSeconds
        iterations       = $Iterations
        retain_runs      = $RetainRuns
        retain_days      = $RetainDays
        run_count        = $script:runCount
        started_at       = $watchStartedAt.ToString("o")
        cleanup          = $script:lastCleanup
        last_run         = $LastRun
    }

    $latest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $latestPath -Encoding UTF8
    Write-Output ($latest | ConvertTo-Json -Depth 8)
}

function Copy-LatestReport {
    param(
        [string]$SourceRel,
        [string]$DestRel
    )

    if ([string]::IsNullOrWhiteSpace($SourceRel) -or [string]::IsNullOrWhiteSpace($DestRel)) {
        return
    }

    $sourcePath = Join-Path $rootPath $SourceRel
    if (-not (Test-Path -LiteralPath $sourcePath)) {
        return
    }

    $destPath = Join-Path $rootPath $DestRel
    $destDir = Split-Path -Parent $destPath
    if ($destDir -and -not (Test-Path -LiteralPath $destDir)) {
        New-Item -ItemType Directory -Force -Path $destDir | Out-Null
    }

    Copy-Item -LiteralPath $sourcePath -Destination $destPath -Force
}

function Sync-LatestReports {
    param(
        [string]$ReportRel,
        [string]$RepairRel,
        [string]$ToolchainRel,
        [string]$AuditRel,
        [string]$DoctorRel,
        [string]$TaskRel
    )

    Copy-LatestReport -SourceRel $ReportRel -DestRel "reports/agent_unattended.json"
    Copy-LatestReport -SourceRel $RepairRel -DestRel "reports/agent_repair.json"
    Copy-LatestReport -SourceRel $ToolchainRel -DestRel "reports/agent_toolchain.json"
    Copy-LatestReport -SourceRel $AuditRel -DestRel "reports/agent_audit.json"
    Copy-LatestReport -SourceRel $DoctorRel -DestRel "reports/agent_doctor.json"
    Copy-LatestReport -SourceRel $TaskRel -DestRel "reports/agent_tasks.json"
}

function Invoke-HistoryCleanup {
    $deleted = New-Object System.Collections.Generic.List[object]
    $allReports = @(Get-ChildItem -LiteralPath $historyPath -Filter "agent_unattended_*.json" -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending)

    $deleteSet = New-Object System.Collections.Generic.HashSet[string]

    if ($RetainRuns -gt 0 -and $allReports.Count -gt $RetainRuns) {
        foreach ($report in @($allReports | Select-Object -Skip $RetainRuns)) {
            [void]$deleteSet.Add($report.FullName)
        }
    }

    if ($RetainDays -gt 0) {
        $cutoff = (Get-Date).AddDays(-1 * $RetainDays)
        foreach ($report in @($allReports | Where-Object { $_.LastWriteTime -lt $cutoff })) {
            [void]$deleteSet.Add($report.FullName)
        }
    }

    foreach ($path in @($deleteSet)) {
        $unattended = Get-Item -LiteralPath $path -ErrorAction SilentlyContinue
        if ($null -eq $unattended) {
            continue
        }

        $suffix = $unattended.BaseName.Substring("agent_unattended_".Length)
        $runFiles = @(Get-ChildItem -LiteralPath $historyPath -Filter ("agent_*_{0}.json" -f $suffix) -File -ErrorAction SilentlyContinue)
        foreach ($item in $runFiles) {
            $rel = $item.FullName.Substring($rootPath.Length).TrimStart('\', '/')
            $lastWrite = $item.LastWriteTime.ToString("o")
            $length = $item.Length
            Remove-Item -LiteralPath $item.FullName -Force
            $deleted.Add([pscustomobject]@{
                file = $rel
                last_write_time = $lastWrite
                length = $length
            })
        }
    }

    $remaining = @(Get-ChildItem -LiteralPath $historyPath -Filter "agent_unattended_*.json" -File -ErrorAction SilentlyContinue)
    return [pscustomobject]@{
        generated_at = (Get-Date).ToString("o")
        deleted_count = $deleted.Count
        remaining_count = $remaining.Count
        deleted = @($deleted.ToArray())
    }
}

Acquire-WatchLock

try {
    do {
        $runCount++
        $stamp = Get-Date -Format "yyyyMMdd_HHmmss"
        $runPrefix = "{0}_{1}_{2}" -f $stamp, $PID, $runCount
        $reportRel = Join-Path $HistoryDir ("agent_unattended_{0}.json" -f $runPrefix)
        $repairRel = Join-Path $HistoryDir ("agent_repair_{0}.json" -f $runPrefix)
        $toolchainRel = Join-Path $HistoryDir ("agent_toolchain_{0}.json" -f $runPrefix)
        $auditRel = Join-Path $HistoryDir ("agent_audit_{0}.json" -f $runPrefix)
        $doctorRel = Join-Path $HistoryDir ("agent_doctor_{0}.json" -f $runPrefix)
        $taskRel = Join-Path $HistoryDir ("agent_tasks_{0}.json" -f $runPrefix)
        $command = @(
            "-NoProfile",
            "-ExecutionPolicy", "Bypass",
            "-File", "scripts\agent_unattended.ps1",
            "-Root", ".",
            "-Report", $reportRel,
            "-RepairReport", $repairRel,
            "-ToolchainReport", $toolchainRel,
            "-AuditReport", $auditRel,
            "-DoctorReport", $doctorRel,
            "-TaskReport", $taskRel,
            "-SkipWatchLockRepair",
            "-SkipWatchLockDoctor",
            "-ContinueOnFailure"
        )

        if ($NoFormat) {
            $command += "-NoFormat"
        }
        if ($BootstrapGo) {
            $command += "-BootstrapGo"
            $command += "-BootstrapTimeoutSeconds"
            $command += $BootstrapTimeoutSeconds
            $command += "-BootstrapMaxCandidates"
            $command += $BootstrapMaxCandidates
            if ($BootstrapIncludeAll) {
                $command += "-BootstrapIncludeAll"
            }
            if ($BootstrapArchivePath -ne "") {
                $command += "-BootstrapArchivePath"
                $command += $BootstrapArchivePath
            }
            if ($BootstrapOfflineDir -ne "") {
                $command += "-BootstrapOfflineDir"
                $command += $BootstrapOfflineDir
            }
            if ($BootstrapDownloadUrl -ne "") {
                $command += "-BootstrapDownloadUrl"
                $command += $BootstrapDownloadUrl
            }
            if ($BootstrapSHA256 -ne "") {
                $command += "-BootstrapSHA256"
                $command += $BootstrapSHA256
            }
            if ($BootstrapBaseUrls -ne "") {
                $command += "-BootstrapBaseUrls"
                $command += $BootstrapBaseUrls
            }
            if ($BootstrapFallbackVersions -ne "") {
                $command += "-BootstrapFallbackVersions"
                $command += $BootstrapFallbackVersions
            }
            if ($BootstrapUseBits) {
                $command += "-BootstrapUseBits"
            }
            if ($BootstrapUseWingetMsi) {
                $command += "-BootstrapUseWingetMsi"
            }
            if ($BootstrapNoAutoOffline) {
                $command += "-BootstrapNoAutoOffline"
            }
        }
        if ($UseDockerGo) {
            $command += "-UseDockerGo"
        }

        Push-Location $rootPath
        try {
            & powershell @command | Out-Null
            $lastExit = $LASTEXITCODE
            if ($null -eq $lastExit) {
                $lastExit = 0
            }
        } finally {
            Pop-Location
        }

        $reportPath = Join-Path $rootPath $reportRel
        $taskPath = Join-Path $rootPath $taskRel
        $lastRun = Get-RunSummary -ReportPath $reportPath -TaskReportPath $taskPath -RunnerExitCode $lastExit -Iteration $runCount
        $lastCleanup = Invoke-HistoryCleanup
        Write-Latest -LastRun $lastRun
        Sync-LatestReports -ReportRel $reportRel -RepairRel $repairRel -ToolchainRel $toolchainRel -AuditRel $auditRel -DoctorRel $doctorRel -TaskRel $taskRel
        & powershell -NoProfile -ExecutionPolicy Bypass -File "scripts\agent_status.ps1" -Latest $Latest -Tasks $taskRel -Out $StatusReport -NoFail | Out-Null
        & powershell -NoProfile -ExecutionPolicy Bypass -File "scripts\agent_report.ps1" -Status $StatusReport -Out $SummaryReport | Out-Null

        if ($Strict -and $lastRun.exit_code -ne 0) {
            exit $lastRun.exit_code
        }

        if ($Iterations -gt 0 -and $runCount -ge $Iterations) {
            break
        }

        Start-Sleep -Seconds $IntervalSeconds
    } while ($true)
} finally {
    Release-WatchLock
}

if ($Strict -and $lastExit -ne 0) {
    exit $lastExit
}

exit 0
