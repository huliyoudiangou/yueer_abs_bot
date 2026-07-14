param(
    [string]$Root = ".",
    [string]$Version = "latest",
    [string]$Arch = "amd64",
    [string]$ArchivePath = "",
    [string]$OfflineDir = ".tools/offline",
    [string]$DownloadUrl = "",
    [string]$SHA256 = "",
    [string]$BaseUrls = "https://go.dev/dl",
    [string]$FallbackVersions = "",
    [int]$MaxCandidates = 6,
    [int]$TimeoutSeconds = 900,
    [int]$Retries = 5,
    [switch]$IncludeAll,
    [switch]$NoAutoOffline,
    [switch]$UseBits,
    [switch]$UseWingetMsi,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$toolsPath = Join-Path $rootPath ".tools"
$goRoot = Join-Path $toolsPath "go"
$goExe = Join-Path $goRoot "bin\go.exe"
$metadataUrl = "https://go.dev/dl/?mode=json"
if ($IncludeAll) {
    $metadataUrl = "https://go.dev/dl/?mode=json&include=all"
}

if ((Test-Path -LiteralPath $goExe) -and -not $Force) {
    $versionOutput = & $goExe version
    [pscustomobject]@{
        status = "already_installed"
        goroot = $goRoot
        go     = $goExe
        version = $versionOutput
    } | ConvertTo-Json -Depth 4
    exit 0
}

if (-not (Test-Path -LiteralPath $toolsPath)) {
    New-Item -ItemType Directory -Force -Path $toolsPath | Out-Null
}

$archive = ""
$downloadTarget = ""
$url = $DownloadUrl
$expectedSHA256 = $SHA256
$usingLocalArchive = $false
$sourceKind = "download"
$archiveKind = "zip"
$baseUrlList = @($BaseUrls -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" })
if ($baseUrlList.Count -eq 0) {
    $baseUrlList = @("https://go.dev/dl")
}
$fallbackVersionList = @($FallbackVersions -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" })

function Test-DownloadUrl {
    param([string]$Url)

    $probeDir = Join-Path $toolsPath "probes"
    if (-not (Test-Path -LiteralPath $probeDir)) {
        New-Item -ItemType Directory -Force -Path $probeDir | Out-Null
    }
    $probeFile = Join-Path $probeDir ("go-probe-{0}.bin" -f ([guid]::NewGuid().ToString()))

    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
        try {
            & curl.exe --fail --location --range 0-1023 --connect-timeout 10 --max-time 30 --silent --show-error --output $probeFile $Url
            return ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $probeFile) -and (Get-Item -LiteralPath $probeFile).Length -gt 0)
        } finally {
            if (Test-Path -LiteralPath $probeFile) {
                Remove-Item -LiteralPath $probeFile -Force -ErrorAction SilentlyContinue
            }
            if (Test-Path -LiteralPath $probeDir) {
                $probeChildren = @(Get-ChildItem -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue)
                if ($probeChildren.Count -eq 0) {
                    Remove-Item -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue
                }
            }
        }
    }

    try {
        Invoke-WebRequest -Uri $Url -Headers @{ Range = "bytes=0-1023" } -OutFile $probeFile -UseBasicParsing -TimeoutSec 30 | Out-Null
        return ((Test-Path -LiteralPath $probeFile) -and (Get-Item -LiteralPath $probeFile).Length -gt 0)
    } catch {
        return $false
    } finally {
        if (Test-Path -LiteralPath $probeFile) {
            Remove-Item -LiteralPath $probeFile -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $probeDir) {
            $probeChildren = @(Get-ChildItem -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue)
            if ($probeChildren.Count -eq 0) {
                Remove-Item -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

function Get-WindowsArchiveFile {
    param(
        [object]$Release,
        [string]$Arch
    )

    return @($Release.files | Where-Object {
            $_.os -eq "windows" -and $_.arch -eq $Arch -and $_.kind -eq "archive" -and $_.filename -like "*.zip"
        } | Select-Object -First 1)[0]
}

function Find-OfflineArchive {
    param(
        [string]$Dir,
        [string]$Arch
    )

    $offlinePath = Join-Path $rootPath $Dir
    if (-not (Test-Path -LiteralPath $offlinePath)) {
        return $null
    }

    $matches = @()
    foreach ($pattern in @("go*.windows-$Arch.zip", "go*.windows-$Arch.msi", "Go Programming Language*_X64_wix*.msi")) {
        $matches += @(Get-ChildItem -LiteralPath $offlinePath -Filter $pattern -File -ErrorAction SilentlyContinue)
    }

    return @($matches |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1)[0]
}

$offlineArchive = $null
if ($ArchivePath -eq "" -and $DownloadUrl -eq "" -and -not $NoAutoOffline) {
    $offlineArchive = Find-OfflineArchive -Dir $OfflineDir -Arch $Arch
}

if ($ArchivePath -ne "") {
    $archive = (Resolve-Path -LiteralPath $ArchivePath).Path
    $usingLocalArchive = $true
    $sourceKind = "archive_path"
    $archiveKind = if ($archive.EndsWith(".msi", [System.StringComparison]::OrdinalIgnoreCase)) { "msi" } else { "zip" }
} elseif ($null -ne $offlineArchive) {
    $archive = $offlineArchive.FullName
    $usingLocalArchive = $true
    $sourceKind = "offline_dir"
    $archiveKind = if ($archive.EndsWith(".msi", [System.StringComparison]::OrdinalIgnoreCase)) { "msi" } else { "zip" }
} else {
    if ($DownloadUrl -eq "" -and $UseWingetMsi) {
        if (-not (Get-Command winget.exe -ErrorAction SilentlyContinue)) {
            throw "winget.exe was not found; cannot use -UseWingetMsi."
        }

        $offlinePath = Join-Path $rootPath $OfflineDir
        if (-not (Test-Path -LiteralPath $offlinePath)) {
            New-Item -ItemType Directory -Force -Path $offlinePath | Out-Null
        }

        & winget.exe download --id GoLang.Go --exact --architecture x64 --installer-type wix --download-directory $offlinePath --accept-package-agreements --accept-source-agreements --disable-interactivity
        if ($LASTEXITCODE -ne 0) {
            throw "winget download failed with exit code $LASTEXITCODE."
        }

        $offlineArchive = Find-OfflineArchive -Dir $OfflineDir -Arch $Arch
        if ($null -eq $offlineArchive) {
            throw "winget download did not create a Go Windows $Arch MSI or zip in $OfflineDir."
        }

        $archive = $offlineArchive.FullName
        $usingLocalArchive = $true
        $sourceKind = "winget_download"
        $archiveKind = if ($archive.EndsWith(".msi", [System.StringComparison]::OrdinalIgnoreCase)) { "msi" } else { "zip" }
    } elseif ($DownloadUrl -eq "") {
        $releases = Invoke-RestMethod -Uri $metadataUrl -UseBasicParsing -TimeoutSec 60
        $selectedRelease = $null
        $downloadFile = $null
        $candidateReleases = @()
        if ($Version -eq "latest") {
            foreach ($fallback in $fallbackVersionList) {
                $fallbackName = $fallback
                if (-not $fallbackName.StartsWith("go")) {
                    $fallbackName = "go$fallbackName"
                }
                $candidateReleases += @($releases | Where-Object { $_.version -eq $fallbackName })
            }
            $candidateReleases += @($releases | Where-Object { $_.stable })
            $dedupedCandidates = New-Object System.Collections.Generic.List[object]
            $seenVersions = New-Object System.Collections.Generic.HashSet[string]
            foreach ($candidateRelease in $candidateReleases) {
                if ($null -eq $candidateRelease -or -not $candidateRelease.version) {
                    continue
                }
                if ($seenVersions.Add([string]$candidateRelease.version)) {
                    $dedupedCandidates.Add($candidateRelease)
                }
                if ($MaxCandidates -gt 0 -and $dedupedCandidates.Count -ge $MaxCandidates) {
                    break
                }
            }
            $candidateReleases = @($dedupedCandidates.ToArray())

            foreach ($candidate in $candidateReleases) {
                $candidateFile = Get-WindowsArchiveFile -Release $candidate -Arch $Arch
                if ($null -eq $candidateFile) {
                    continue
                }

                foreach ($baseUrl in $baseUrlList) {
                    $trimmedBase = $baseUrl.TrimEnd("/")
                    $candidateUrl = "$trimmedBase/$($candidateFile.filename)"
                    if (Test-DownloadUrl -Url $candidateUrl) {
                        $selectedRelease = $candidate
                        $downloadFile = $candidateFile
                        $url = $candidateUrl
                        break
                    }

                    Write-Warning "Skipping unavailable Go archive: $candidateUrl"
                }

                if ($null -ne $downloadFile) {
                    break
                }
            }
        } else {
            $versionName = $Version
            if (-not $versionName.StartsWith("go")) {
                $versionName = "go$versionName"
            }
            $selectedRelease = @($releases | Where-Object { $_.version -eq $versionName } | Select-Object -First 1)[0]
            if ($null -ne $selectedRelease) {
                $downloadFile = Get-WindowsArchiveFile -Release $selectedRelease -Arch $Arch
            }
        }

        if ($null -eq $selectedRelease) {
            throw "Could not resolve Go release '$Version' from $metadataUrl"
        }

        if ($null -eq $downloadFile) {
            throw "Could not find Windows $Arch Go zip for $($selectedRelease.version)"
        }

        if ($url -eq "") {
            $url = ($baseUrlList[0].TrimEnd("/") + "/" + $downloadFile.filename)
        }
        if ($expectedSHA256 -eq "") {
            $expectedSHA256 = [string]$downloadFile.sha256
        }
    }

    if (-not $usingLocalArchive) {
        $fileName = Split-Path -Leaf $url
        if ($fileName.EndsWith(".msi", [System.StringComparison]::OrdinalIgnoreCase)) {
            $archiveKind = "msi"
            $fileName = $fileName -replace '\.msi$', ".$PID.msi"
        } elseif ($fileName.EndsWith(".zip", [System.StringComparison]::OrdinalIgnoreCase)) {
            $archiveKind = "zip"
            $fileName = $fileName -replace '\.zip$', ".$PID.zip"
        } else {
            $archiveKind = "zip"
            $fileName = "go.windows-$Arch.$PID.zip"
        }
        $archive = Join-Path $toolsPath $fileName
        $downloadTarget = "$archive.part"
    }
}

Get-ChildItem -LiteralPath $toolsPath -Filter ("go*.windows-{0}*.zip" -f $Arch) -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -ne $archive } |
    ForEach-Object {
        try {
            Remove-Item -LiteralPath $_.FullName -Force -ErrorAction Stop
        } catch {
            Write-Warning ("Could not remove stale archive {0}: {1}" -f $_.FullName, $_.Exception.Message)
        }
    }

Get-ChildItem -LiteralPath $toolsPath -Filter ("go*.windows-{0}*.msi" -f $Arch) -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -ne $archive } |
    ForEach-Object {
        try {
            Remove-Item -LiteralPath $_.FullName -Force -ErrorAction Stop
        } catch {
            Write-Warning ("Could not remove stale archive {0}: {1}" -f $_.FullName, $_.Exception.Message)
        }
    }

function Find-ExtractedGoRoot {
    param([string]$ExtractPath)

    $direct = Join-Path $ExtractPath "go"
    if (Test-Path -LiteralPath (Join-Path $direct "bin\go.exe")) {
        return $direct
    }

    $candidates = @(Get-ChildItem -LiteralPath $ExtractPath -Directory -Recurse -ErrorAction SilentlyContinue |
        Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "bin\go.exe") } |
        Select-Object -First 1)
    if ($candidates.Count -gt 0) {
        return $candidates[0].FullName
    }

    return ""
}

try {
    if (-not $usingLocalArchive) {
        if (Test-Path -LiteralPath $downloadTarget) {
            Remove-Item -LiteralPath $downloadTarget -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $archive) {
            Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
        }

        if ($UseBits) {
            $jobName = "agent-go-bootstrap-$PID"
            $job = Start-BitsTransfer -Source $url -Destination $downloadTarget -TransferType Download -DisplayName $jobName -Asynchronous -ErrorAction Stop
            $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
            while ($job.JobState -in @("Connecting", "Transferring", "Queued", "Suspended")) {
                if ((Get-Date) -gt $deadline) {
                    Remove-BitsTransfer -BitsJob $job -ErrorAction SilentlyContinue
                    throw "BITS download timed out after $TimeoutSeconds seconds: $url"
                }
                Start-Sleep -Seconds 2
                $job = Get-BitsTransfer -JobId $job.JobId -ErrorAction Stop
            }
            if ($job.JobState -eq "Transferred") {
                Complete-BitsTransfer -BitsJob $job
            } else {
                $state = $job.JobState
                Remove-BitsTransfer -BitsJob $job -ErrorAction SilentlyContinue
                throw "BITS download failed with state $state`: $url"
            }
        } elseif (Get-Command curl.exe -ErrorAction SilentlyContinue) {
            $curlArgs = @(
                "--fail",
                "--location",
                "--retry", $Retries,
                "--retry-delay", "5",
                "--connect-timeout", "30",
                "--max-time", $TimeoutSeconds,
                "--output", $downloadTarget,
                $url
            )
            $proc = Start-Process -FilePath "curl.exe" -ArgumentList $curlArgs -NoNewWindow -PassThru -Wait
            if ($proc.ExitCode -ne 0) {
                if (Test-Path -LiteralPath $downloadTarget) {
                    Remove-Item -LiteralPath $downloadTarget -Force -ErrorAction SilentlyContinue
                }
                throw "curl failed to download Go archive with exit code $($proc.ExitCode): $url"
            }
        } else {
            Invoke-WebRequest -Uri $url -OutFile $downloadTarget -UseBasicParsing -TimeoutSec $TimeoutSeconds
        }

        if (-not (Test-Path -LiteralPath $downloadTarget)) {
            throw "Go archive download did not create a file: $downloadTarget"
        }
        Move-Item -LiteralPath $downloadTarget -Destination $archive -Force
    }

    if ((Get-Item -LiteralPath $archive).Length -le 0) {
        throw "Downloaded Go archive is empty: $archive"
    }

    $actualSHA256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expectedSHA256 -and $actualSHA256 -ne $expectedSHA256.ToLowerInvariant()) {
        throw "Downloaded Go archive checksum mismatch. expected=$expectedSHA256 actual=$actualSHA256"
    }

    $extractPath = Join-Path $toolsPath "go_extract"
    if (Test-Path -LiteralPath $extractPath) {
        Remove-Item -LiteralPath $extractPath -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $extractPath | Out-Null

    if ($archiveKind -eq "msi") {
        if (-not (Get-Command msiexec.exe -ErrorAction SilentlyContinue)) {
            throw "msiexec.exe was not found; cannot extract MSI archive."
        }

        $msiArgs = "/a `"$archive`" TARGETDIR=`"$extractPath`" /qn /norestart"
        $msiProc = Start-Process -FilePath "msiexec.exe" -ArgumentList $msiArgs -WindowStyle Hidden -PassThru -Wait
        if ($msiProc.ExitCode -ne 0) {
            throw "msiexec administrative extract failed with exit code $($msiProc.ExitCode): $archive"
        }
    } else {
        Expand-Archive -LiteralPath $archive -DestinationPath $extractPath -Force
    }

    $extractedGo = Find-ExtractedGoRoot -ExtractPath $extractPath
    if ($extractedGo -eq "") {
        throw "Downloaded Go archive did not contain bin\go.exe"
    }

    if (Test-Path -LiteralPath $goRoot) {
        Remove-Item -LiteralPath $goRoot -Recurse -Force
    }
    Move-Item -LiteralPath $extractedGo -Destination $goRoot
    Remove-Item -LiteralPath $extractPath -Recurse -Force

    $versionOutput = & $goExe version
    [pscustomobject]@{
        status  = "installed"
        source  = $(if ($usingLocalArchive) { $archive } else { $url })
        source_kind = $sourceKind
        archive_kind = $archiveKind
        archive = $archive
        sha256  = $actualSHA256
        goroot  = $goRoot
        go      = $goExe
        version = $versionOutput
    } | ConvertTo-Json -Depth 4
} finally {
    if (-not $usingLocalArchive -and (Test-Path -LiteralPath $downloadTarget)) {
        Remove-Item -LiteralPath $downloadTarget -Force -ErrorAction SilentlyContinue
    }
    if (-not $usingLocalArchive -and (Test-Path -LiteralPath $archive)) {
        Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    }
    $probeDir = Join-Path $toolsPath "probes"
    if (Test-Path -LiteralPath $probeDir) {
        $probeChildren = @(Get-ChildItem -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue)
        if ($probeChildren.Count -eq 0) {
            Remove-Item -LiteralPath $probeDir -Force -ErrorAction SilentlyContinue
        }
    }
}
