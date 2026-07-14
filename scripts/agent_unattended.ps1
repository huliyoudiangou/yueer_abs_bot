param(
    [string]$Root = ".",
    [string]$Report = "reports/agent_unattended.json",
    [string]$RepairReport = "reports/agent_repair.json",
    [string]$ToolchainReport = "reports/agent_toolchain.json",
    [string]$AuditReport = "reports/agent_audit.json",
    [string]$DoctorReport = "reports/agent_doctor.json",
    [string]$TaskReport = "reports/agent_tasks.json",
    [int]$MaxOutputChars = 20000,
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
    [switch]$SkipWatchLockRepair,
    [switch]$SkipWatchLockDoctor,
    [switch]$ContinueOnFailure
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$steps = New-Object System.Collections.Generic.List[object]
$localGoBin = Join-Path $rootPath ".tools\go\bin"
$localGo = Join-Path $localGoBin "go.exe"
$localGofmt = Join-Path $localGoBin "gofmt.exe"

function Get-ToolPath {
    param(
        [string]$Name,
        [string]$LocalPath
    )

    if ($LocalPath -and (Test-Path -LiteralPath $LocalPath)) {
        return $LocalPath
    }

    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if ($cmd) {
        return $cmd.Source
    }

    return ""
}

function Stop-ProcessTree {
    param([int]$ProcessId)

    $children = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object { $_.ParentProcessId -eq $ProcessId })
    foreach ($child in $children) {
        Stop-ProcessTree -ProcessId ([int]$child.ProcessId)
    }
    Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
}

function Add-Step {
    param(
        [string]$Name,
        [string]$Status,
        [Nullable[int]]$ExitCode,
        [int64]$DurationMs,
        [string]$Command,
        [string]$Output,
        [string]$Reason
    )

    $trimmedOutput = $Output.Trim()
    if ($MaxOutputChars -gt 0 -and $trimmedOutput.Length -gt $MaxOutputChars) {
        $trimmedOutput = $trimmedOutput.Substring(0, $MaxOutputChars) + "`n...[truncated]"
    }

    $script:steps.Add([pscustomobject]@{
        name        = $Name
        status      = $Status
        exit_code   = $ExitCode
        duration_ms = $DurationMs
        command     = $Command
        output      = $trimmedOutput
        reason      = $Reason
    })
}

function Invoke-Captured {
    param(
        [string]$Name,
        [string]$Command,
        [string]$ToolName,
        [string]$ToolPath = "",
        [int]$TimeoutSeconds = 0
    )

    if ($ToolName -and -not $ToolPath -and -not (Get-Command $ToolName -ErrorAction SilentlyContinue)) {
        Add-Step $Name "skipped" $null 0 $Command "" "Required tool '$ToolName' was not found."
        return 127
    }

    $started = Get-Date
    $output = ""
    $exitCode = 0

    try {
        Push-Location $rootPath
        if ($TimeoutSeconds -gt 0) {
            $outFile = Join-Path $rootPath ("reports\agent_step_{0}_{1}.out" -f $Name, $PID)
            $errFile = Join-Path $rootPath ("reports\agent_step_{0}_{1}.err" -f $Name, $PID)
            $proc = Start-Process -FilePath "powershell" `
                -ArgumentList @("-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", $Command) `
                -WorkingDirectory $rootPath `
                -RedirectStandardOutput $outFile `
                -RedirectStandardError $errFile `
                -WindowStyle Hidden `
                -PassThru
            $finished = $proc.WaitForExit($TimeoutSeconds * 1000)
            if (-not $finished) {
                Stop-ProcessTree -ProcessId $proc.Id
                $exitCode = 124
                $output = "Step timed out after $TimeoutSeconds seconds.`n" +
                    (Get-Content -LiteralPath $outFile -Raw -ErrorAction SilentlyContinue) + "`n" +
                    (Get-Content -LiteralPath $errFile -Raw -ErrorAction SilentlyContinue)
            } else {
                $exitCode = $proc.ExitCode
                $output = ((Get-Content -LiteralPath $outFile -Raw -ErrorAction SilentlyContinue) + "`n" + (Get-Content -LiteralPath $errFile -Raw -ErrorAction SilentlyContinue))
            }
            Remove-Item -LiteralPath $outFile, $errFile -Force -ErrorAction SilentlyContinue
        } else {
            $output = & powershell -NoProfile -ExecutionPolicy Bypass -Command $Command 2>&1 | Out-String
            $exitCode = $LASTEXITCODE
            if ($null -eq $exitCode) {
                $exitCode = 0
            }
        }
    } finally {
        Pop-Location
    }

    $elapsed = [int64]((Get-Date) - $started).TotalMilliseconds
    $status = "passed"
    if ($exitCode -ne 0) {
        $status = "failed"
    }

    Add-Step $Name $status $exitCode $elapsed $Command $output ""
    return $exitCode
}

function Invoke-GoStep {
    param(
        [string]$Name,
        [string]$LocalCommand,
        [string]$DockerCommand
    )

    $goPath = Get-ToolPath "go" $localGo
    if ($goPath) {
        $command = $LocalCommand -replace '^go\b', ('"' + $goPath + '"')
        return Invoke-Captured $Name $command "go" $goPath
    }

    if ($UseDockerGo) {
        if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
            Add-Step $Name "skipped" $null 0 $DockerCommand "" "Required tool 'go' was not found and Docker fallback is unavailable."
            return 127
        }
        return Invoke-Captured $Name $DockerCommand "docker"
    }

    Add-Step $Name "skipped" $null 0 $LocalCommand "" "Required tool 'go' was not found."
    return 127
}

function Write-Report {
    param(
        [int]$ExitCode,
        [switch]$Silent
    )

    $stepArray = @($steps.ToArray())
    $failed = @($stepArray | Where-Object { $_.status -eq "failed" }).Count
    $skipped = @($stepArray | Where-Object { $_.status -eq "skipped" }).Count

    $summary = [pscustomobject]@{
        generated_at = (Get-Date).ToString("o")
        root         = $rootPath
        exit_code    = $ExitCode
        failed_count = $failed
        skipped_count = $skipped
        steps        = $stepArray
    }

    $reportPath = Join-Path $rootPath $Report
    $reportDir = Split-Path -Parent $reportPath
    if ($reportDir -and -not (Test-Path -LiteralPath $reportDir)) {
        New-Item -ItemType Directory -Force -Path $reportDir | Out-Null
    }

    $json = $summary | ConvertTo-Json -Depth 8
    Set-Content -LiteralPath $reportPath -Value $json -Encoding UTF8
    if (-not $Silent) {
        Write-Output $json
    }
}

$overallExit = 0

$repairCommand = "powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_repair.ps1 -Out `"$RepairReport`""
if ($SkipWatchLockRepair) {
    $repairCommand += " -SkipWatchLock"
}
$repairExit = Invoke-Captured "agent_repair" $repairCommand "powershell"
if ($repairExit -ne 0) {
    $overallExit = $repairExit
}

$toolchainExit = Invoke-Captured "agent_toolchain" ("powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_toolchain.ps1 -Out `"" + $ToolchainReport + "`" -NoFail") "powershell"
if ($toolchainExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $toolchainExit
}

if ($BootstrapGo -and -not (Get-ToolPath "go" $localGo)) {
    $bootstrapCommand = "powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_bootstrap_go.ps1 -TimeoutSeconds " + $BootstrapTimeoutSeconds
    if ($BootstrapArchivePath -ne "") {
        $bootstrapCommand += " -ArchivePath `"$BootstrapArchivePath`""
    }
    if ($BootstrapOfflineDir -ne "") {
        $bootstrapCommand += " -OfflineDir `"$BootstrapOfflineDir`""
    }
    if ($BootstrapDownloadUrl -ne "") {
        $bootstrapCommand += " -DownloadUrl `"$BootstrapDownloadUrl`""
    }
    if ($BootstrapSHA256 -ne "") {
        $bootstrapCommand += " -SHA256 `"$BootstrapSHA256`""
    }
    if ($BootstrapBaseUrls -ne "") {
        $bootstrapCommand += " -BaseUrls `"$BootstrapBaseUrls`""
    }
    if ($BootstrapFallbackVersions -ne "") {
        $bootstrapCommand += " -FallbackVersions `"$BootstrapFallbackVersions`""
    }
    $bootstrapCommand += " -MaxCandidates $BootstrapMaxCandidates"
    if ($BootstrapIncludeAll) {
        $bootstrapCommand += " -IncludeAll"
    }
    if ($BootstrapNoAutoOffline) {
        $bootstrapCommand += " -NoAutoOffline"
    }
    if ($BootstrapUseBits) {
        $bootstrapCommand += " -UseBits"
    }
    if ($BootstrapUseWingetMsi) {
        $bootstrapCommand += " -UseWingetMsi"
    }
    $bootstrapExit = Invoke-Captured "bootstrap_go" $bootstrapCommand "powershell" "" ($BootstrapTimeoutSeconds + 15)
    if ($bootstrapExit -ne 0) {
        $overallExit = $bootstrapExit
    }
}

if (-not $NoFormat) {
    $gofmtPath = Get-ToolPath "gofmt" $localGofmt
    if ($gofmtPath) {
        $formatExit = Invoke-Captured "gofmt" ('Get-ChildItem -LiteralPath . -Filter "*.go" -File | ForEach-Object { & "' + $gofmtPath + '" -w $_.FullName; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE } }') "gofmt" $gofmtPath
    } elseif ($UseDockerGo -and (Get-Command docker -ErrorAction SilentlyContinue)) {
        $formatExit = Invoke-Captured "gofmt" "docker run --rm -v `"${rootPath}:/app`" -w /app golang:1.22-alpine3.20 gofmt -w ." "docker"
    } elseif ($UseDockerGo) {
        Add-Step "gofmt" "skipped" $null 0 "docker run --rm -v `"${rootPath}:/app`" -w /app golang:1.22-alpine3.20 gofmt -w ." "" "Required tool 'gofmt' was not found and Docker fallback is unavailable."
        $formatExit = 127
    } else {
        Add-Step "gofmt" "skipped" $null 0 "gofmt -w <top-level go files>" "" "Required tool 'gofmt' was not found."
        $formatExit = 127
    }
    if ($formatExit -ne 0) {
        $overallExit = $formatExit
    }
}

$auditExit = Invoke-Captured "agent_audit" ("powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_audit.ps1 -Out `"" + $AuditReport + "`"") "powershell"
if ($auditExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $auditExit
}

$testExit = Invoke-GoStep "go_test" `
    "go test ./..." `
    "docker run --rm -v `"${rootPath}:/app`" -w /app golang:1.22-alpine3.20 sh -lc `"apk add --no-cache gcc musl-dev && go test ./...`""
if ($testExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $testExit
}

$buildExit = Invoke-GoStep "go_build" `
    "go build ./..." `
    "docker run --rm -v `"${rootPath}:/app`" -w /app golang:1.22-alpine3.20 sh -lc `"apk add --no-cache gcc musl-dev && CGO_ENABLED=1 go build ./...`""
if ($buildExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $buildExit
}

$doctorCommand = "powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_doctor.ps1 -Out `"$DoctorReport`""
if ($SkipWatchLockDoctor) {
    $doctorCommand += " -SkipWatchLock"
}
$doctorExit = Invoke-Captured "agent_doctor" $doctorCommand "powershell"
if ($doctorExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $doctorExit
}

Write-Report $overallExit -Silent

$taskExit = Invoke-Captured "agent_tasks" ("powershell -NoProfile -ExecutionPolicy Bypass -File scripts\agent_tasks.ps1 -AuditReport `"" + $AuditReport + "`" -UnattendedReport `"" + $Report + "`" -DoctorReport `"" + $DoctorReport + "`" -ToolchainReport `"" + $ToolchainReport + "`" -Out `"" + $TaskReport + "`" -NoFail") "powershell"
if ($taskExit -ne 0 -and $overallExit -eq 0) {
    $overallExit = $taskExit
}

if ($ContinueOnFailure) {
    Write-Report $overallExit
    exit 0
}

Write-Report $overallExit
exit $overallExit
