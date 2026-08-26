#Requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$VerifyOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$candidateRoot = $PSScriptRoot
$verifier = Join-Path $candidateRoot "Verify-Candidate.exe"
$manifestPath = Join-Path $candidateRoot "candidate-manifest.json"
$requiredPayload = @(
    "app\neproto_client.exe",
    "app\flutter_windows.dll",
    "app\data\icudtl.dat",
    "app\data\app.so",
    "service\NeProto.Service.exe",
    "service\wintun.dll",
    "Rollback-Candidate.ps1"
)

function Assert-CandidatePayload {
    if (-not (Test-Path -LiteralPath $verifier -PathType Leaf)) {
        throw "Verify-Candidate.exe is missing"
    }
    $verificationOutput = & $verifier -mode verify -root $candidateRoot 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Candidate manifest verification failed: $($verificationOutput -join ' ')"
    }
    foreach ($relativePath in $requiredPayload) {
        if (-not (Test-Path -LiteralPath (Join-Path $candidateRoot $relativePath) -PathType Leaf)) {
            throw "Candidate payload is incomplete: $relativePath"
        }
    }
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    if ($manifest.schema -ne 1 -or $manifest.platform -ne "windows-x64" -or $manifest.carrier_policy -ne "http3-only") {
        throw "Candidate manifest policy is invalid"
    }
    return $manifest
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Assert-ServiceVersion([string]$Path, [string]$ExpectedVersion) {
    $output = & $Path --version 2>&1
    if ($LASTEXITCODE -ne 0 -or ($output -join "`n").Trim() -ne "NeProto Windows Service $ExpectedVersion") {
        throw "Windows service version mismatch at $Path"
    }
}

function Write-StateAtomic([object]$State, [string]$Path) {
    $temporary = "$Path.tmp"
    $State | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $temporary -Encoding UTF8
    Move-Item -LiteralPath $temporary -Destination $Path -Force
}

function Wait-ServiceRunning([System.ServiceProcess.ServiceController]$Service) {
    $Service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(20))
    $Service.Refresh()
    if ($Service.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Running) {
        throw "NeProtoService did not reach Running state"
    }
}

function Stop-ServiceBounded([System.ServiceProcess.ServiceController]$Service) {
    $Service.Refresh()
    if ($Service.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
        Stop-Service -InputObject $Service -Force
        $Service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(20))
    }
}

function Replace-FileAtomically([string]$Source, [string]$Destination) {
    $temporary = "$Destination.candidate-new"
    Copy-Item -LiteralPath $Source -Destination $temporary -Force
    try {
        [System.IO.File]::Replace($temporary, $Destination, $null, $true)
    } finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
}

$manifest = Assert-CandidatePayload
if ($VerifyOnly) {
    Write-Host "Candidate payload verified: $($manifest.version) $($manifest.commit) http3-only"
    exit 0
}

if (-not (Test-IsAdministrator)) {
    throw "Run Install-Candidate.ps1 from an elevated PowerShell window"
}

$stableRoot = Join-Path $env:ProgramFiles "NeProto"
$stableService = Join-Path $stableRoot "NeProto.Service.exe"
$stableWintun = Join-Path $stableRoot "wintun.dll"
$candidateApp = Join-Path $env:ProgramFiles "NeProto Candidate"
$candidateStage = "$candidateApp.new"
$dataRoot = Join-Path $env:ProgramData "NeProto"
$backupRoot = Join-Path $dataRoot "candidate-backups"
$statePath = Join-Path $dataRoot "candidate-overlay.json"
$serviceSource = Join-Path $candidateRoot "service\NeProto.Service.exe"
$wintunSource = Join-Path $candidateRoot "service\wintun.dll"

if (-not (Test-Path -LiteralPath $stableService -PathType Leaf) -or -not (Test-Path -LiteralPath $stableWintun -PathType Leaf)) {
    throw "Install the matching stable NeProto Windows package before applying this candidate"
}
if (Get-Process -Name "NeProto", "neproto_client" -ErrorAction SilentlyContinue) {
    throw "Disconnect and close every NeProto UI before applying the candidate"
}
if (Test-Path -LiteralPath $statePath) {
    throw "A candidate overlay is already active; run Rollback-Candidate.ps1 first"
}
if ((Test-Path -LiteralPath $candidateApp) -or (Test-Path -LiteralPath $candidateStage)) {
    throw "NeProto Candidate directory already exists; remove it only after checking prior rollback state"
}

$serviceRegistry = Get-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\NeProtoService" -ErrorAction Stop
if ([string]$serviceRegistry.ImagePath -notmatch [regex]::Escape($stableService)) {
    throw "NeProtoService does not point to the stable NeProto installation"
}
Assert-ServiceVersion $stableService ([string]$manifest.version)
Assert-ServiceVersion $serviceSource ([string]$manifest.version)
$service = Get-Service -Name "NeProtoService" -ErrorAction Stop

$timestamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$backupDirectory = Join-Path $backupRoot ("{0}-{1}" -f $manifest.version, $timestamp)
New-Item -ItemType Directory -Path $backupDirectory -Force | Out-Null
$backupService = Join-Path $backupDirectory "NeProto.Service.exe"
$backupWintun = Join-Path $backupDirectory "wintun.dll"
Copy-Item -LiteralPath $stableService -Destination $backupService
Copy-Item -LiteralPath $stableWintun -Destination $backupWintun

$state = [ordered]@{
    schema = 1
    phase = "prepared"
    version = [string]$manifest.version
    commit = [string]$manifest.commit
    carrier_policy = "http3-only"
    installed_at = [DateTime]::UtcNow.ToString("o")
    stable_service_path = $stableService
    stable_wintun_path = $stableWintun
    candidate_app_path = $candidateApp
    backup_directory = $backupDirectory
    backup_service_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $backupService).Hash.ToLowerInvariant()
    backup_wintun_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $backupWintun).Hash.ToLowerInvariant()
}
Write-StateAtomic $state $statePath

$rollbackRequired = $true
try {
    New-Item -ItemType Directory -Path $candidateStage | Out-Null
    Copy-Item -Path (Join-Path $candidateRoot "app\*") -Destination $candidateStage -Recurse -Force
    if (-not (Test-Path -LiteralPath (Join-Path $candidateStage "neproto_client.exe") -PathType Leaf)) {
        throw "Candidate Flutter application copy is incomplete"
    }
    Move-Item -LiteralPath $candidateStage -Destination $candidateApp

    Stop-ServiceBounded $service
    Replace-FileAtomically $serviceSource $stableService
    Replace-FileAtomically $wintunSource $stableWintun
    Start-Service -InputObject $service
    Wait-ServiceRunning $service
    & $stableService --probe *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Candidate service probe failed"
    }

    $state.phase = "active"
    Write-StateAtomic $state $statePath
    $rollbackRequired = $false
} finally {
    if ($rollbackRequired) {
        try {
            Stop-ServiceBounded $service
            Replace-FileAtomically $backupService $stableService
            Replace-FileAtomically $backupWintun $stableWintun
            Start-Service -InputObject $service
            Wait-ServiceRunning $service
            Remove-Item -LiteralPath $candidateStage -Recurse -Force -ErrorAction SilentlyContinue
            Remove-Item -LiteralPath $candidateApp -Recurse -Force -ErrorAction SilentlyContinue
            Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue
        } catch {
            Write-Error "Automatic candidate rollback failed; backup is retained at $backupDirectory"
        }
    }
}

Write-Host "NeProto Windows candidate installed without launching the UI."
Write-Host "Launch: $candidateApp\neproto_client.exe"
Write-Host "Rollback: $candidateRoot\Rollback-Candidate.ps1"
