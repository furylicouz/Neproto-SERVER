#Requires -Version 5.1
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "candidate-service-version.ps1")

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Stop-ServiceBounded([System.ServiceProcess.ServiceController]$Service) {
    $Service.Refresh()
    if ($Service.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
        Stop-Service -InputObject $Service -Force
        $Service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(20))
    }
}

function Wait-ServiceRunning([System.ServiceProcess.ServiceController]$Service) {
    $Service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(20))
    $Service.Refresh()
    if ($Service.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Running) {
        throw "NeProtoService did not reach Running state"
    }
}

function Replace-FileAtomically([string]$Source, [string]$Destination) {
    $temporary = "$Destination.rollback-new"
    Copy-Item -LiteralPath $Source -Destination $temporary -Force
    try {
        [System.IO.File]::Replace($temporary, $Destination, $null, $true)
    } finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
}

if (-not (Test-IsAdministrator)) {
    throw "Run Rollback-Candidate.ps1 from an elevated PowerShell window"
}

$dataRoot = Join-Path $env:ProgramData "NeProto"
$statePath = Join-Path $dataRoot "candidate-overlay.json"
if (-not (Test-Path -LiteralPath $statePath -PathType Leaf)) {
    throw "No active NeProto candidate rollback state was found"
}
$state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
$baseVersion = Get-NeProtoRollbackBaseVersion $state

$stableRoot = Join-Path $env:ProgramFiles "NeProto"
$expectedService = Join-Path $stableRoot "NeProto.Service.exe"
$expectedWintun = Join-Path $stableRoot "wintun.dll"
$expectedCandidateApp = Join-Path $env:ProgramFiles "NeProto Candidate"
$expectedBackupRoot = [System.IO.Path]::GetFullPath((Join-Path $dataRoot "candidate-backups"))
$backupDirectory = [System.IO.Path]::GetFullPath([string]$state.backup_directory)

if ($state.schema -ne 1 -or $state.phase -notin @("prepared", "active") -or
    [string]$state.version -notmatch '^np2-[0-9]+\.[0-9]+\.[0-9]+$' -or
    $baseVersion -notmatch '^np2-[0-9]+\.[0-9]+\.[0-9]+$' -or
    [string]$state.commit -notmatch '^[0-9a-f]{40}$' -or
    $state.carrier_policy -ne "http3-only" -or
    [string]$state.stable_service_path -ne $expectedService -or
    [string]$state.stable_wintun_path -ne $expectedWintun -or
    [string]$state.candidate_app_path -ne $expectedCandidateApp -or
    -not $backupDirectory.StartsWith($expectedBackupRoot + [System.IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Candidate rollback state is invalid"
}

$backupService = Join-Path $backupDirectory "NeProto.Service.exe"
$backupWintun = Join-Path $backupDirectory "wintun.dll"
if (-not (Test-Path -LiteralPath $backupService -PathType Leaf) -or -not (Test-Path -LiteralPath $backupWintun -PathType Leaf)) {
    throw "Candidate rollback backup is incomplete"
}
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $backupService).Hash.ToLowerInvariant() -ne [string]$state.backup_service_sha256 -or
    (Get-FileHash -Algorithm SHA256 -LiteralPath $backupWintun).Hash.ToLowerInvariant() -ne [string]$state.backup_wintun_sha256) {
    throw "Candidate rollback backup checksum mismatch"
}
if (Get-Process -Name "NeProto", "neproto_client" -ErrorAction SilentlyContinue) {
    throw "Disconnect and close every NeProto UI before rollback"
}
Assert-NeProtoServiceVersion -Path $backupService -ExpectedVersion $baseVersion

$service = Get-Service -Name "NeProtoService" -ErrorAction Stop
Stop-ServiceBounded $service
Replace-FileAtomically $backupService $expectedService
Replace-FileAtomically $backupWintun $expectedWintun
Start-Service -InputObject $service
Wait-ServiceRunning $service
& $expectedService --probe *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Restored stable service probe failed; rollback state was retained"
}

Remove-Item -LiteralPath $expectedCandidateApp -Recurse -Force -ErrorAction SilentlyContinue
$completedState = Join-Path $backupDirectory "candidate-overlay.rolled-back.json"
Move-Item -LiteralPath $statePath -Destination $completedState -Force
Write-Host "NeProto stable Windows service restored. Profiles and DPAPI records were preserved."
