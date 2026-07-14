param(
    [string]$Root = ".",
    [string]$TaskName = "abs-bot-agent-watch",
    [string]$Interval = "HOURLY",
    [int]$Modifier = 1,
    [int]$RetainRuns = 100,
    [int]$RetainDays = 0,
    [int]$BootstrapTimeoutSeconds = 300,
    [string]$BootstrapArchivePath = "",
    [string]$BootstrapOfflineDir = ".tools/offline",
    [string]$BootstrapDownloadUrl = "",
    [string]$BootstrapSHA256 = "",
    [string]$BootstrapBaseUrls = "",
    [string]$BootstrapFallbackVersions = "",
    [int]$BootstrapMaxCandidates = 6,
    [switch]$BootstrapGo,
    [switch]$BootstrapIncludeAll,
    [switch]$BootstrapNoAutoOffline,
    [switch]$BootstrapUseBits,
    [switch]$UseDockerGo,
    [switch]$Install,
    [switch]$Uninstall,
    [switch]$Status
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$watchScript = Join-Path $rootPath "scripts\agent_watch.ps1"
if (-not (Test-Path -LiteralPath $watchScript)) {
    throw "agent_watch.ps1 was not found at $watchScript"
}

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

$watchArgs = @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", (Quote-Arg $watchScript),
    "-Root", (Quote-Arg $rootPath),
    "-Iterations", "1",
    "-RetainRuns", $RetainRuns,
    "-RetainDays", $RetainDays,
    "-BootstrapTimeoutSeconds", $BootstrapTimeoutSeconds
)

if ($BootstrapGo) {
    $watchArgs += "-BootstrapGo"
}
if ($BootstrapIncludeAll) {
    $watchArgs += "-BootstrapIncludeAll"
}
$watchArgs += "-BootstrapMaxCandidates"
$watchArgs += $BootstrapMaxCandidates
if ($BootstrapArchivePath -ne "") {
    $watchArgs += "-BootstrapArchivePath"
    $watchArgs += (Quote-Arg $BootstrapArchivePath)
}
if ($BootstrapOfflineDir -ne "") {
    $watchArgs += "-BootstrapOfflineDir"
    $watchArgs += (Quote-Arg $BootstrapOfflineDir)
}
if ($BootstrapDownloadUrl -ne "") {
    $watchArgs += "-BootstrapDownloadUrl"
    $watchArgs += (Quote-Arg $BootstrapDownloadUrl)
}
if ($BootstrapSHA256 -ne "") {
    $watchArgs += "-BootstrapSHA256"
    $watchArgs += (Quote-Arg $BootstrapSHA256)
}
if ($BootstrapBaseUrls -ne "") {
    $watchArgs += "-BootstrapBaseUrls"
    $watchArgs += (Quote-Arg $BootstrapBaseUrls)
}
if ($BootstrapFallbackVersions -ne "") {
    $watchArgs += "-BootstrapFallbackVersions"
    $watchArgs += (Quote-Arg $BootstrapFallbackVersions)
}
if ($BootstrapUseBits) {
    $watchArgs += "-BootstrapUseBits"
}
if ($BootstrapNoAutoOffline) {
    $watchArgs += "-BootstrapNoAutoOffline"
}
if ($UseDockerGo) {
    $watchArgs += "-UseDockerGo"
}

$taskRun = "powershell.exe " + ($watchArgs -join " ")
$createArgs = @(
    "/Create",
    "/TN", $TaskName,
    "/SC", $Interval,
    "/MO", $Modifier,
    "/TR", $taskRun,
    "/F"
)
$deleteArgs = @("/Delete", "/TN", $TaskName, "/F")
$queryArgs = @("/Query", "/TN", $TaskName)

if ($Status) {
    & schtasks.exe @queryArgs
    exit $LASTEXITCODE
}

if ($Uninstall) {
    if ($Install) {
        throw "Use either -Install or -Uninstall, not both."
    }
    & schtasks.exe @deleteArgs
    exit $LASTEXITCODE
}

if ($Install) {
    & schtasks.exe @createArgs
    exit $LASTEXITCODE
}

[pscustomobject]@{
    mode = "what_if"
    action = "install"
    task_name = $TaskName
    schedule = $Interval
    modifier = $Modifier
    command = "schtasks.exe " + ($createArgs -join " ")
    task_run = $taskRun
} | ConvertTo-Json -Depth 5
