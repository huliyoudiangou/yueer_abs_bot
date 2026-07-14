param(
    [string]$Root = ".",
    [string]$Out = "reports/agent_repair.json",
    [switch]$SkipWatchLock,
    [switch]$WhatIf
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$actions = New-Object System.Collections.Generic.List[object]

function Get-RelPath {
    param([string]$Path)
    $full = (Resolve-Path -LiteralPath $Path).Path
    if ($full.StartsWith($rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $full.Substring($rootPath.Length).TrimStart('\', '/')
    }
    return $full
}

function Add-Action {
    param(
        [string]$Kind,
        [string]$Path,
        [string]$Status,
        [string]$Message
    )

    $script:actions.Add([pscustomobject]@{
        kind    = $Kind
        path    = $Path
        status  = $Status
        message = $Message
    })
}

function Remove-AgentPath {
    param(
        [string]$Kind,
        [string]$Path,
        [switch]$Recurse
    )

    $full = (Resolve-Path -LiteralPath $Path).Path
    if (-not $full.StartsWith($rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        Add-Action $Kind $full "skipped" "Path is outside root."
        return
    }

    $rel = Get-RelPath $full
    if ($WhatIf) {
        Add-Action $Kind $rel "planned" "Would remove."
        return
    }

    try {
        if ($Recurse) {
            Remove-Item -LiteralPath $full -Recurse -Force
        } else {
            Remove-Item -LiteralPath $full -Force
        }
        Add-Action $Kind $rel "removed" "Removed."
    } catch {
        Add-Action $Kind $rel "failed" $_.Exception.Message
    }
}

$reportsPath = Join-Path $rootPath "reports"
if (Test-Path -LiteralPath $reportsPath) {
    foreach ($file in @(Get-ChildItem -LiteralPath $reportsPath -Filter "agent_step_*" -File -ErrorAction SilentlyContinue)) {
        Remove-AgentPath -Kind "step-temp-file" -Path $file.FullName
    }
}

$toolsPath = Join-Path $rootPath ".tools"
if (Test-Path -LiteralPath $toolsPath) {
    foreach ($zip in @(Get-ChildItem -LiteralPath $toolsPath -Filter "go*.zip" -File -ErrorAction SilentlyContinue)) {
        Remove-AgentPath -Kind "toolchain-temp-file" -Path $zip.FullName
    }
    foreach ($msi in @(Get-ChildItem -LiteralPath $toolsPath -Filter "go*.msi" -File -ErrorAction SilentlyContinue)) {
        Remove-AgentPath -Kind "toolchain-temp-file" -Path $msi.FullName
    }
    foreach ($part in @(Get-ChildItem -LiteralPath $toolsPath -Filter "go*.part" -File -ErrorAction SilentlyContinue)) {
        Remove-AgentPath -Kind "toolchain-temp-file" -Path $part.FullName
    }

    $extractPath = Join-Path $toolsPath "go_extract"
    if (Test-Path -LiteralPath $extractPath) {
        Remove-AgentPath -Kind "toolchain-extract-temp" -Path $extractPath -Recurse
    }

    $probePath = Join-Path $toolsPath "probes"
    if (Test-Path -LiteralPath $probePath) {
        Remove-AgentPath -Kind "toolchain-probe-temp" -Path $probePath -Recurse
    }
}

$watchLock = Join-Path $rootPath "reports\agent_watch.lock"
if (-not $SkipWatchLock -and (Test-Path -LiteralPath $watchLock)) {
    $removeLock = $false
    try {
        $lockRaw = Get-Content -LiteralPath $watchLock -Raw
        if ([string]::IsNullOrWhiteSpace($lockRaw)) {
            Start-Sleep -Milliseconds 200
            $lockRaw = Get-Content -LiteralPath $watchLock -Raw
        }
        $lock = $lockRaw | ConvertFrom-Json
        $lockPid = [int]$lock.pid
        if ($lockPid -gt 0 -and $null -ne (Get-Process -Id $lockPid -ErrorAction SilentlyContinue)) {
            Add-Action "watch-active-lock" "reports\agent_watch.lock" "skipped" "Watch lock is held by a running process."
        } elseif ($lockPid -le 0 -or $null -eq (Get-Process -Id $lockPid -ErrorAction SilentlyContinue)) {
            $removeLock = $true
        }
    } catch {
        $removeLock = $true
    }

    if ($removeLock) {
        Remove-AgentPath -Kind "watch-stale-lock" -Path $watchLock
    }
}

$actionArray = @($actions.ToArray())
$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root         = $rootPath
    what_if      = [bool]$WhatIf
    action_count = $actionArray.Count
    failed_count = @($actionArray | Where-Object { $_.status -eq "failed" }).Count
    actions      = $actionArray
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

$json = $summary | ConvertTo-Json -Depth 6
Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8
Write-Output $json

if ($summary.failed_count -gt 0) {
    exit 1
}
