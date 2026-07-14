param(
    [string]$Root = ".",
    [string]$Status = "reports/agent_status.json",
    [string]$Gate = "reports/agent_gate.json",
    [string]$Tasks = "reports/agent_tasks.json",
    [string]$Toolchain = "reports/agent_toolchain.json",
    [string]$Out = "reports/agent_summary.md",
    [int]$MaxTasks = 20
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

function Format-MdCell {
    param([object]$Value)

    if ($null -eq $Value) {
        return ""
    }
    $text = [string]$Value
    $text = $text -replace "`r", " "
    $text = $text -replace "`n", " "
    $text = $text -replace "\|", "\\|"
    return $text.Trim()
}

$statusData = Read-JsonFile $Status
$gateData = Read-JsonFile $Gate

if ($null -ne $statusData -and $statusData.tasks) {
    $Tasks = [string]$statusData.tasks
}
if ($null -ne $statusData -and $statusData.toolchain) {
    $Toolchain = [string]$statusData.toolchain
}

$taskData = Read-JsonFile $Tasks
$toolchainData = Read-JsonFile $Toolchain

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add("# Agent Summary")
$lines.Add("")
$lines.Add(("Generated: {0}" -f (Get-Date).ToString("o")))
$lines.Add(("Root: ``{0}``" -f $rootPath))
$lines.Add("")

$lines.Add("## Health")
$lines.Add("")
$lines.Add("| Field | Value |")
$lines.Add("| --- | --- |")
$lines.Add(("| Gate | {0} |" -f (Format-MdCell $(if ($null -eq $gateData) { "missing" } else { $gateData.status }))))
$lines.Add(("| Status | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "missing" } else { $statusData.status }))))
$lines.Add(("| Exit code | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "" } else { $statusData.exit_code }))))
$lines.Add(("| Toolchain ready | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "" } else { $statusData.toolchain_ready }))))
$lines.Add(("| Todos | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "" } else { $statusData.todo_count }))))
$lines.Add(("| Reviews | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "" } else { $statusData.review_count }))))
$lines.Add(("| Accepted | {0} |" -f (Format-MdCell $(if ($null -eq $statusData) { "" } else { $statusData.accepted_count }))))
$lines.Add("")

if ($null -ne $statusData -and $null -ne $statusData.required_steps) {
    $lines.Add("## Required Steps")
    $lines.Add("")
    $lines.Add("| Step | Status | Reason |")
    $lines.Add("| --- | --- | --- |")
    foreach ($step in @($statusData.required_steps)) {
        $lines.Add(("| {0} | {1} | {2} |" -f (Format-MdCell $step.name), (Format-MdCell $step.status), (Format-MdCell $step.reason)))
    }
    $lines.Add("")
}

if ($null -ne $gateData -and $null -ne $gateData.issues -and @($gateData.issues).Count -gt 0) {
    $lines.Add("## Gate Issues")
    $lines.Add("")
    $lines.Add("| Severity | Kind | Message | Action |")
    $lines.Add("| --- | --- | --- | --- |")
    foreach ($issue in @($gateData.issues)) {
        $lines.Add(("| {0} | {1} | {2} | {3} |" -f (Format-MdCell $issue.severity), (Format-MdCell $issue.kind), (Format-MdCell $issue.message), (Format-MdCell $issue.action)))
    }
    $lines.Add("")
}

if ($null -ne $taskData -and $null -ne $taskData.tasks) {
    $openTasks = @($taskData.tasks | Where-Object { $_.status -in @("todo", "review") } | Select-Object -First $MaxTasks)
    $lines.Add("## Open Tasks")
    $lines.Add("")
    if ($openTasks.Count -eq 0) {
        $lines.Add("No todo or review tasks.")
    } else {
        $lines.Add("| Status | Severity | Kind | Title | Action |")
        $lines.Add("| --- | --- | --- | --- | --- |")
        foreach ($task in $openTasks) {
            $lines.Add(("| {0} | {1} | {2} | {3} | {4} |" -f (Format-MdCell $task.status), (Format-MdCell $task.severity), (Format-MdCell $task.kind), (Format-MdCell $task.title), (Format-MdCell $task.action)))
        }
    }
    $lines.Add("")
}

if ($null -ne $toolchainData) {
    $lines.Add("## Toolchain")
    $lines.Add("")
    $lines.Add("| Field | Value |")
    $lines.Add("| --- | --- |")
    $lines.Add(("| Status | {0} |" -f (Format-MdCell $toolchainData.status)))
    $lines.Add(("| Go | {0} |" -f (Format-MdCell $(if ($toolchainData.go.found) { $toolchainData.go.source } else { "missing" }))))
    $lines.Add(("| gofmt | {0} |" -f (Format-MdCell $(if ($toolchainData.gofmt.found) { $toolchainData.gofmt.source } else { "missing" }))))
    $lines.Add(("| Docker | {0} |" -f (Format-MdCell $(if ($toolchainData.docker.found) { $toolchainData.docker.source } else { "missing" }))))
    $lines.Add(("| winget | {0} |" -f (Format-MdCell $(if ($toolchainData.winget.found) { $toolchainData.winget.source } else { "missing" }))))
    $lines.Add(("| Offline archives | {0} |" -f (Format-MdCell @($toolchainData.offline_archives).Count)))
    $lines.Add("")
}

$outPath = Join-Path $rootPath $Out
$outDir = Split-Path -Parent $outPath
if ($outDir -and -not (Test-Path -LiteralPath $outDir)) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}

Set-Content -LiteralPath $outPath -Value ($lines -join "`n") -Encoding UTF8

[pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    root         = $rootPath
    out          = $Out
    status       = if ($null -eq $statusData) { "missing" } else { $statusData.status }
    gate         = if ($null -eq $gateData) { "missing" } else { $gateData.status }
    todo_count   = if ($null -eq $statusData) { $null } else { $statusData.todo_count }
    review_count = if ($null -eq $statusData) { $null } else { $statusData.review_count }
} | ConvertTo-Json -Depth 4
