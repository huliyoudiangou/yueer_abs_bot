param(
    [string]$Root = ".",
    [string]$Out = "reports/agent_toolchain.json",
    [string]$OfflineDir = ".tools/offline",
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$localGo = Join-Path $rootPath ".tools\go\bin\go.exe"
$localGofmt = Join-Path $rootPath ".tools\go\bin\gofmt.exe"
$offlinePath = Join-Path $rootPath $OfflineDir
$issues = New-Object System.Collections.Generic.List[object]

function Get-Tool {
    param(
        [string]$Name,
        [string]$LocalPath = ""
    )

    if ($LocalPath -and (Test-Path -LiteralPath $LocalPath)) {
        return [pscustomobject]@{
            found = $true
            source = $LocalPath
            local = $true
            version = ""
        }
    }

    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if ($cmd) {
        return [pscustomobject]@{
            found = $true
            source = $cmd.Source
            local = $false
            version = ""
        }
    }

    return [pscustomobject]@{
        found = $false
        source = ""
        local = $false
        version = ""
    }
}

function Add-Issue {
    param(
        [string]$Severity,
        [string]$Kind,
        [string]$Message,
        [string]$Action
    )

    $script:issues.Add([pscustomobject]@{
        severity = $Severity
        kind = $Kind
        message = $Message
        action = $Action
    })
}

$go = Get-Tool -Name "go" -LocalPath $localGo
if ($go.found) {
    try {
        $go.version = (& $go.source version 2>&1 | Out-String).Trim()
    } catch {
        Add-Issue "error" "go-version-failed" "Go was found but 'go version' failed." "Inspect $($go.source)."
    }
} else {
    Add-Issue "error" "go-missing" "Go executable was not found." "Run scripts\agent_acquire_toolchain.ps1 to preview options, place go*.windows-amd64.zip in .tools/offline, or run it with -Install when an approved source is available."
}

$gofmt = Get-Tool -Name "gofmt" -LocalPath $localGofmt
if (-not $gofmt.found) {
    Add-Issue "error" "gofmt-missing" "gofmt executable was not found." "Acquire Go through scripts\agent_acquire_toolchain.ps1 or provide an offline Go zip."
}

$docker = Get-Tool -Name "docker"
if ($docker.found) {
    try {
        $docker.version = (& $docker.source --version 2>&1 | Out-String).Trim()
    } catch {
        Add-Issue "warn" "docker-version-failed" "Docker was found but version check failed." "Inspect Docker installation before using -UseDockerGo."
    }
}

$winget = Get-Tool -Name "winget"
if ($winget.found) {
    try {
        $winget.version = (& $winget.source --version 2>&1 | Out-String).Trim()
    } catch {
        $winget.version = ""
    }
}

$offlineArchives = @()
if (Test-Path -LiteralPath $offlinePath) {
    $offlineArchives = @((Get-ChildItem -LiteralPath $offlinePath -Filter "go*.windows-amd64.*" -File -ErrorAction SilentlyContinue) +
        (Get-ChildItem -LiteralPath $offlinePath -Filter "Go Programming Language*_X64_wix*.msi" -File -ErrorAction SilentlyContinue) |
        Where-Object { $_.Extension -in @(".zip", ".msi") } |
        Sort-Object LastWriteTime -Descending |
        ForEach-Object {
            [pscustomobject]@{
                path = $_.FullName.Substring($rootPath.Length).TrimStart('\', '/')
                length = $_.Length
                last_write_time = $_.LastWriteTime.ToString("o")
                sha256 = if ($_.Length -gt 0) { (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant() } else { "" }
            }
        })
}

if (-not $go.found -and @($offlineArchives).Count -eq 0) {
    Add-Issue "error" "offline-archive-missing" "No offline Go archive was found in $OfflineDir." "Put go*.windows-amd64.zip in $OfflineDir or preview other acquisition paths with scripts\agent_acquire_toolchain.ps1."
}

foreach ($archive in @($offlineArchives)) {
    if ([int64]$archive.length -le 0) {
        Add-Issue "error" "offline-archive-empty" "Offline Go archive is empty: $($archive.path)." "Replace it with a complete go*.windows-amd64.zip."
    }
}

$ready = $go.found -and $gofmt.found
$status = if ($ready) { "ready" } else { "needs_toolchain" }
$issueArray = @($issues.ToArray())
$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root = $rootPath
    status = $status
    ready = [bool]$ready
    error_count = @($issueArray | Where-Object { $_.severity -eq "error" }).Count
    warn_count = @($issueArray | Where-Object { $_.severity -eq "warn" }).Count
    go = $go
    gofmt = $gofmt
    docker = $docker
    winget = $winget
    offline_dir = $OfflineDir
    offline_archives = @($offlineArchives)
    issues = $issueArray
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

$json = $summary | ConvertTo-Json -Depth 8
Set-Content -LiteralPath $outPath -Value $json -Encoding UTF8
Write-Output $json

if (-not $NoFail -and $summary.error_count -gt 0) {
    exit 1
}
