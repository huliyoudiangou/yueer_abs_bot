param(
    [string]$Root = ".",
    [string]$Mode = "auto",
    [string]$ArchivePath = "",
    [string]$OfflineDir = ".tools/offline",
    [string]$DownloadUrl = "",
    [string]$SHA256 = "",
    [string]$BaseUrls = "https://go.dev/dl",
    [string]$FallbackVersions = "1.23.12,1.22.12",
    [int]$MaxCandidates = 2,
    [int]$TimeoutSeconds = 300,
    [switch]$IncludeAll,
    [switch]$UseBits,
    [switch]$UseWingetMsi,
    [switch]$Install,
    [switch]$AllowSystemInstall,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$bootstrapScript = Join-Path $rootPath "scripts\agent_bootstrap_go.ps1"
$toolchainScript = Join-Path $rootPath "scripts\agent_toolchain.ps1"

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

function Find-OfflineArchive {
    param([string]$Dir)

    $offlinePath = Join-Path $rootPath $Dir
    if (-not (Test-Path -LiteralPath $offlinePath)) {
        return $null
    }

    return @((Get-ChildItem -LiteralPath $offlinePath -Filter "go*.windows-amd64.*" -File -ErrorAction SilentlyContinue) +
        (Get-ChildItem -LiteralPath $offlinePath -Filter "Go Programming Language*_X64_wix*.msi" -File -ErrorAction SilentlyContinue) |
        Where-Object { $_.Extension -in @(".zip", ".msi") } |
        Where-Object { $_.Length -gt 0 } |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1)[0]
}

if (-not (Test-Path -LiteralPath $bootstrapScript)) {
    throw "Missing bootstrap script: $bootstrapScript"
}

$toolchain = $null
if (Test-Path -LiteralPath $toolchainScript) {
    $toolchain = & powershell -NoProfile -ExecutionPolicy Bypass -File $toolchainScript -Root $rootPath -NoFail | ConvertFrom-Json
    if ([bool]$toolchain.ready -and -not $Force) {
        [pscustomobject]@{
            mode = "already_ready"
            install = [bool]$Install
            action = "none"
            toolchain = $toolchain
        } | ConvertTo-Json -Depth 8
        exit 0
    }
}

$selectedMode = $Mode.ToLowerInvariant()
if ($selectedMode -eq "auto") {
    if ($ArchivePath -ne "" -or $null -ne (Find-OfflineArchive -Dir $OfflineDir)) {
        $selectedMode = "bootstrap"
    } elseif ($DownloadUrl -ne "") {
        $selectedMode = "bootstrap"
    } elseif ($AllowSystemInstall -and (Get-Command winget -ErrorAction SilentlyContinue)) {
        $selectedMode = "winget"
    } else {
        $selectedMode = "bootstrap"
    }
}

$bootstrapArgs = @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", (Quote-Arg $bootstrapScript),
    "-Root", (Quote-Arg $rootPath),
    "-OfflineDir", (Quote-Arg $OfflineDir),
    "-BaseUrls", (Quote-Arg $BaseUrls),
    "-FallbackVersions", (Quote-Arg $FallbackVersions),
    "-MaxCandidates", $MaxCandidates,
    "-TimeoutSeconds", $TimeoutSeconds
)
if ($ArchivePath -ne "") {
    $bootstrapArgs += "-ArchivePath"
    $bootstrapArgs += (Quote-Arg $ArchivePath)
}
if ($DownloadUrl -ne "") {
    $bootstrapArgs += "-DownloadUrl"
    $bootstrapArgs += (Quote-Arg $DownloadUrl)
}
if ($SHA256 -ne "") {
    $bootstrapArgs += "-SHA256"
    $bootstrapArgs += (Quote-Arg $SHA256)
}
if ($IncludeAll) {
    $bootstrapArgs += "-IncludeAll"
}
if ($UseBits) {
    $bootstrapArgs += "-UseBits"
}
if ($UseWingetMsi) {
    $bootstrapArgs += "-UseWingetMsi"
}
if ($Force) {
    $bootstrapArgs += "-Force"
}

$bootstrapCommand = "powershell.exe " + ($bootstrapArgs -join " ")
$wingetArgs = @("install", "--id", "GoLang.Go", "--exact", "--silent", "--accept-package-agreements", "--accept-source-agreements")
$wingetCommand = "winget.exe " + ($wingetArgs -join " ")

if (-not $Install) {
    [pscustomobject]@{
        mode = "what_if"
        selected_mode = $selectedMode
        bootstrap_command = $bootstrapCommand
        winget_command = $wingetCommand
        allow_system_install = [bool]$AllowSystemInstall
        offline_archive = $(
            $offline = Find-OfflineArchive -Dir $OfflineDir
            if ($null -eq $offline) { "" } else { $offline.FullName.Substring($rootPath.Length).TrimStart('\', '/') }
        )
        toolchain = $toolchain
    } | ConvertTo-Json -Depth 8
    exit 0
}

if ($selectedMode -eq "winget") {
    if (-not $AllowSystemInstall) {
        throw "System install requires -AllowSystemInstall."
    }
    & winget.exe @wingetArgs
    exit $LASTEXITCODE
}

if ($selectedMode -ne "bootstrap") {
    throw "Unsupported acquisition mode '$Mode'. Use auto, bootstrap, or winget."
}

& powershell @bootstrapArgs
exit $LASTEXITCODE
