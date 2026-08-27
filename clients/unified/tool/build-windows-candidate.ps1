#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Commit = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = (Get-Content -Raw -LiteralPath (Join-Path $root "VERSION")).Trim()
}
if ($Version -notmatch '^np2-[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "Invalid NP/2 version: $Version"
}
if ([string]::IsNullOrWhiteSpace($Commit)) {
    $Commit = (& git -C $root rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Cannot resolve candidate Git commit" }
}
$Commit = $Commit.ToLowerInvariant()
if ($Commit -notmatch '^[0-9a-f]{40}$') {
    throw "Invalid candidate Git commit: $Commit"
}

function Invoke-Checked([scriptblock]$Command, [string]$Failure) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw $Failure }
}

function Assert-PowerShellSyntax([string]$Path) {
    $tokens = $null
    $errors = $null
    [void][Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) {
        throw "PowerShell syntax validation failed for $Path`: $($errors.Message -join '; ')"
    }
}

function Find-VCRuntimeDirectory {
    $candidates = @()
    if (-not [string]::IsNullOrWhiteSpace($env:VCToolsRedistDir)) {
        $candidates += Join-Path $env:VCToolsRedistDir "x64\Microsoft.VC143.CRT"
    }
    $vswhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
    if (Test-Path -LiteralPath $vswhere -PathType Leaf) {
        $installation = (& $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath).Trim()
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($installation)) {
            $redistRoot = Join-Path $installation "VC\Redist\MSVC"
            if (Test-Path -LiteralPath $redistRoot -PathType Container) {
                $candidates += Get-ChildItem -LiteralPath $redistRoot -Directory |
                    Sort-Object Name -Descending |
                    ForEach-Object { Join-Path $_.FullName "x64\Microsoft.VC143.CRT" }
            }
        }
    }
    $directory = $candidates |
        Where-Object { Test-Path -LiteralPath $_ -PathType Container } |
        Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($directory)) {
        throw "Microsoft VC143 x64 runtime directory was not found"
    }
    return $directory
}

$appRoot = Join-Path $root "clients\unified\app"
$windowsBuild = Join-Path $appRoot "build\windows\x64\runner\Release"
$legacyWindowsBuild = Join-Path $appRoot "build\windows\runner\Release"
$work = Join-Path $root ".tmp\windows-unified-candidate"
$candidateName = "NeProto-Windows-Candidate-$Version-x64"
$candidateRoot = Join-Path $work $candidateName
$appDestination = Join-Path $candidateRoot "app"
$serviceDestination = Join-Path $candidateRoot "service"
$output = Join-Path $root "dist\windows-unified"
$archive = Join-Path $output "$candidateName.zip"
$checksum = "$archive.sha256"

Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $checksum -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $appDestination, $serviceDestination, $output | Out-Null

Assert-PowerShellSyntax (Join-Path $PSScriptRoot "verify-windows.ps1")
Assert-PowerShellSyntax (Join-Path $PSScriptRoot "Install-Candidate.ps1")
Assert-PowerShellSyntax (Join-Path $PSScriptRoot "Rollback-Candidate.ps1")
Assert-PowerShellSyntax (Join-Path $PSScriptRoot "flutter-version-output.ps1")
Assert-PowerShellSyntax (Join-Path $PSScriptRoot "verify-flutter-version-output.ps1")
& (Join-Path $PSScriptRoot "verify-flutter-version-output.ps1")
Invoke-Checked { & (Join-Path $PSScriptRoot "verify-windows.ps1") -BuildOnly } "Unified Windows build verification failed"

if (-not (Test-Path -LiteralPath $windowsBuild -PathType Container)) {
    $windowsBuild = $legacyWindowsBuild
}
if (-not (Test-Path -LiteralPath $windowsBuild -PathType Container)) {
    throw "Flutter Windows release directory was not produced"
}
$requiredFlutterFiles = @(
    "neproto_client.exe",
    "flutter_windows.dll",
    "data\icudtl.dat",
    "data\app.so",
    "data\flutter_assets\AssetManifest.bin"
)
foreach ($relativePath in $requiredFlutterFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $windowsBuild $relativePath) -PathType Leaf)) {
        throw "Flutter Windows release is incomplete: $relativePath"
    }
}
Copy-Item -Path (Join-Path $windowsBuild "*") -Destination $appDestination -Recurse -Force

$vcRuntime = Find-VCRuntimeDirectory
foreach ($runtimeName in @("msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll")) {
    $runtimeSource = Join-Path $vcRuntime $runtimeName
    if (-not (Test-Path -LiteralPath $runtimeSource -PathType Leaf)) {
        throw "Required Microsoft runtime is missing: $runtimeName"
    }
    Copy-Item -LiteralPath $runtimeSource -Destination (Join-Path $appDestination $runtimeName) -Force
}

$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path -LiteralPath $go -PathType Leaf)) { $go = "go" }
$serviceBinary = Join-Path $serviceDestination "NeProto.Service.exe"
$verifierBinary = Join-Path $candidateRoot "Verify-Candidate.exe"
Push-Location $root
try {
    Invoke-Checked {
        & $go build -trimpath -ldflags "-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$Version" -o $serviceBinary ./cmd/neproto-windows-service
    } "NeProto.Service.exe build failed"
    Invoke-Checked {
        & $go build -trimpath -ldflags "-s -w" -o $verifierBinary ./cmd/neproto-windows-candidate
    } "Verify-Candidate.exe build failed"
} finally {
    Pop-Location
}

Copy-Item -LiteralPath (Join-Path $root "clients\windows\ThirdParty\wintun\wintun.dll") -Destination (Join-Path $serviceDestination "wintun.dll")
Copy-Item -LiteralPath (Join-Path $root "clients\windows\ThirdParty\wintun\LICENSE.txt") -Destination (Join-Path $serviceDestination "WINTUN-LICENSE.txt")
Copy-Item -LiteralPath (Join-Path $PSScriptRoot "Install-Candidate.ps1") -Destination $candidateRoot
Copy-Item -LiteralPath (Join-Path $PSScriptRoot "Rollback-Candidate.ps1") -Destination $candidateRoot
Copy-Item -LiteralPath (Join-Path $root "clients\unified\WINDOWS-CANDIDATE.md") -Destination $candidateRoot

Invoke-Checked {
    & $verifierBinary -mode create -root $candidateRoot -version $Version -commit $Commit
} "Candidate manifest creation failed"
Invoke-Checked {
    & $verifierBinary -mode verify -root $candidateRoot
} "Candidate manifest verification failed"

$windowsPowerShell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
Invoke-Checked {
    & $windowsPowerShell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $candidateRoot "Install-Candidate.ps1") -VerifyOnly
} "Candidate PowerShell 5.1 verification failed"

Compress-Archive -Path (Join-Path $candidateRoot "*") -DestinationPath $archive -CompressionLevel Optimal
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
Set-Content -LiteralPath $checksum -Value "$hash  $([System.IO.Path]::GetFileName($archive))" -Encoding ascii

Write-Host "Built $archive"
Write-Host "SHA256 $hash"
