param(
    [string]$Root = ".",
    [string]$Out = "reports/agent_doctor.json",
    [switch]$SkipWatchLock,
    [switch]$NoFail
)

$ErrorActionPreference = "Stop"

$rootPath = (Resolve-Path -LiteralPath $Root).Path
$findings = New-Object System.Collections.Generic.List[object]

function Add-Finding {
    param(
        [string]$Severity,
        [string]$Rule,
        [string]$Path,
        [string]$Message
    )

    $script:findings.Add([pscustomobject]@{
        severity = $Severity
        rule     = $Rule
        path     = $Path
        message  = $Message
    })
}

function Get-RelPath {
    param([string]$Path)
    $full = (Resolve-Path -LiteralPath $Path).Path
    if ($full.StartsWith($rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $full.Substring($rootPath.Length).TrimStart('\', '/')
    }
    return $full
}

function Read-Json {
    param([string]$Path)
    $fullPath = Join-Path $rootPath $Path
    if (-not (Test-Path -LiteralPath $fullPath)) {
        Add-Finding "warn" "missing-json" $Path "JSON report does not exist yet."
        return $null
    }

    try {
        return Get-Content -LiteralPath $fullPath -Raw | ConvertFrom-Json
    } catch {
        Add-Finding "error" "invalid-json" $Path $_.Exception.Message
        return $null
    }
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

foreach ($script in @(Get-ChildItem -LiteralPath (Join-Path $rootPath "scripts") -Filter "agent_*.ps1" -File -ErrorAction SilentlyContinue)) {
    try {
        $tokens = $null
        $errors = $null
        [System.Management.Automation.Language.Parser]::ParseFile($script.FullName, [ref]$tokens, [ref]$errors) | Out-Null
        foreach ($err in @($errors)) {
            Add-Finding "error" "powershell-parse" (Get-RelPath $script.FullName) $err.Message
        }
    } catch {
        Add-Finding "error" "powershell-parse" (Get-RelPath $script.FullName) $_.Exception.Message
    }
}

$audit = Read-Json "reports/agent_audit.json"
$latest = Read-Json "reports/agent_watch_latest.json"
$effectiveTasks = "reports/agent_tasks.json"
if ($null -ne $latest -and $null -ne $latest.last_run -and $latest.last_run.task_report_path) {
    $effectiveTasks = [string]$latest.last_run.task_report_path
}
$tasks = Read-Json $effectiveTasks
$baseline = Read-Json "docs/agent/audit_baseline.json"

if ($null -ne $tasks) {
    foreach ($field in @("task_count", "todo_count", "review_count", "accepted_count", "tasks")) {
        if ($null -eq $tasks.$field) {
            Add-Finding "error" "tasks-schema" "reports/agent_tasks.json" "Missing field '$field'."
        }
    }
}

if ($null -ne $latest) {
    foreach ($field in @("status", "last_run", "cleanup")) {
        if ($null -eq $latest.$field) {
            Add-Finding "warn" "latest-schema" "reports/agent_watch_latest.json" "Missing field '$field'."
        }
    }
}

if ($null -ne $baseline) {
    $seen = @{}
    foreach ($accepted in @($baseline.accepted_reviews)) {
        $id = [string]$accepted.id
        if ($id -eq "") {
            Add-Finding "error" "baseline-id" "docs/agent/audit_baseline.json" "Accepted review is missing an id."
            continue
        }
        if ($seen.ContainsKey($id)) {
            Add-Finding "error" "baseline-duplicate" "docs/agent/audit_baseline.json" "Duplicate accepted review id '$id'."
        }
        $seen[$id] = $true

        foreach ($field in @("source", "kind", "title", "file", "evidence", "reason")) {
            $property = $accepted.PSObject.Properties[$field]
            $value = ""
            if ($null -ne $property) {
                $value = [string]$property.Value
            }
            if ([string]::IsNullOrWhiteSpace($value)) {
                Add-Finding "error" "baseline-field" "docs/agent/audit_baseline.json" "Accepted review '$id' is missing required field '$field'."
            }
        }
    }
}

if ($null -ne $tasks -and $null -ne $baseline) {
    $taskIds = @{}
    $taskSignatures = @{}
    foreach ($task in @($tasks.tasks)) {
        $taskIds[[string]$task.id] = [string]$task.status
        $signature = New-ReviewSignature `
            -Source $task.source `
            -Kind $task.kind `
            -Title $task.title `
            -File $task.file `
            -Evidence $task.evidence
        if ([string]$task.status -eq "accepted") {
            Add-Count $taskSignatures $signature
        }
    }

    $baselineSignatures = @{}
    foreach ($accepted in @($baseline.accepted_reviews)) {
        $signature = New-ReviewSignature `
            -Source $accepted.source `
            -Kind $accepted.kind `
            -Title $accepted.title `
            -File $accepted.file `
            -Evidence $accepted.evidence
        Add-Count $baselineSignatures $signature
    }

    foreach ($accepted in @($baseline.accepted_reviews)) {
        $id = [string]$accepted.id
        $matchedById = $id -ne "" -and $taskIds.ContainsKey($id)
        if ($matchedById) {
            if ($taskIds[$id] -ne "accepted") {
                Add-Finding "error" "baseline-status" "docs/agent/audit_baseline.json" "Accepted review '$id' is current task status '$($taskIds[$id])'."
            }
            continue
        }

        $signature = New-ReviewSignature `
            -Source $accepted.source `
            -Kind $accepted.kind `
            -Title $accepted.title `
            -File $accepted.file `
            -Evidence $accepted.evidence
        $matchedBySignature = $signature -ne "" `
            -and $baselineSignatures.ContainsKey($signature) `
            -and $taskSignatures.ContainsKey($signature) `
            -and [int]$baselineSignatures[$signature] -eq [int]$taskSignatures[$signature]
        if ($id -ne "" -and -not $matchedBySignature) {
            Add-Finding "warn" "baseline-stale" "docs/agent/audit_baseline.json" "Accepted review '$id' is not present in current tasks."
        }
    }
}

foreach ($tmp in @(Get-ChildItem -LiteralPath (Join-Path $rootPath "reports") -Filter "agent_step_*" -File -ErrorAction SilentlyContinue)) {
    Add-Finding "warn" "step-temp-file" (Get-RelPath $tmp.FullName) "Temporary step output file was left behind."
}

foreach ($zip in @(Get-ChildItem -LiteralPath (Join-Path $rootPath ".tools") -Filter "go*.zip" -File -ErrorAction SilentlyContinue)) {
    Add-Finding "warn" "toolchain-temp-file" (Get-RelPath $zip.FullName) "Temporary Go archive was left behind."
}
foreach ($msi in @(Get-ChildItem -LiteralPath (Join-Path $rootPath ".tools") -Filter "go*.msi" -File -ErrorAction SilentlyContinue)) {
    Add-Finding "warn" "toolchain-temp-file" (Get-RelPath $msi.FullName) "Temporary Go MSI was left behind."
}
foreach ($part in @(Get-ChildItem -LiteralPath (Join-Path $rootPath ".tools") -Filter "go*.part" -File -ErrorAction SilentlyContinue)) {
    Add-Finding "warn" "toolchain-temp-file" (Get-RelPath $part.FullName) "Temporary Go partial download was left behind."
}

$probePath = Join-Path $rootPath ".tools\probes"
if (Test-Path -LiteralPath $probePath) {
    Add-Finding "warn" "toolchain-probe-temp" ".tools/probes" "Temporary Go probe directory was left behind."
}

$offlineDir = Join-Path $rootPath ".tools\offline"
if (Test-Path -LiteralPath $offlineDir) {
    foreach ($archive in @(Get-ChildItem -LiteralPath $offlineDir -Filter "go*.windows-*.*" -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Extension -in @(".zip", ".msi") })) {
        if ($archive.Length -le 0) {
            Add-Finding "warn" "offline-toolchain-empty" (Get-RelPath $archive.FullName) "Offline Go archive is empty."
        }
    }
}

$watchLock = Join-Path $rootPath "reports\agent_watch.lock"
if (-not $SkipWatchLock -and (Test-Path -LiteralPath $watchLock)) {
    $lockPid = 0
    $lockValid = $false
    try {
        $lockRaw = Get-Content -LiteralPath $watchLock -Raw
        if ([string]::IsNullOrWhiteSpace($lockRaw)) {
            Start-Sleep -Milliseconds 200
            $lockRaw = Get-Content -LiteralPath $watchLock -Raw
        }
        $lock = $lockRaw | ConvertFrom-Json
        $lockPid = [int]$lock.pid
        $lockValid = $true
    } catch {
        Add-Finding "warn" "watch-lock-invalid" "reports/agent_watch.lock" "Watch lock file is not valid JSON."
    }

    if ($lockValid -and ($lockPid -le 0 -or $null -eq (Get-Process -Id $lockPid -ErrorAction SilentlyContinue))) {
        Add-Finding "warn" "watch-stale-lock" "reports/agent_watch.lock" "Watch lock file points to a process that is not running."
    }
}

if ($null -ne $audit -and $null -ne $tasks) {
    $auditErrorCount = [int]$audit.error_count
    if ($auditErrorCount -gt 0 -and [int]$tasks.todo_count -eq 0) {
        Add-Finding "error" "task-coverage" "reports/agent_tasks.json" "Audit has errors but task queue has no todos."
    }
}

$findingArray = @($findings.ToArray())
$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root         = $rootPath
    error_count  = @($findingArray | Where-Object { $_.severity -eq "error" }).Count
    warn_count   = @($findingArray | Where-Object { $_.severity -eq "warn" }).Count
    findings     = $findingArray
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
