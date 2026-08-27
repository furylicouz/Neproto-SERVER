[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "candidate-service-version.ps1")

$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("neproto-version-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $temporaryRoot | Out-Null

function New-FakeService([string]$Name, [string]$Output, [int]$ExitCode = 0) {
    $path = Join-Path $temporaryRoot "$Name.cmd"
    @(
        "@echo off"
        "echo $Output"
        "exit /b $ExitCode"
    ) | Set-Content -LiteralPath $path -Encoding ascii
    return $path
}

try {
    $oldService = New-FakeService "old-service" "NeProto Windows Service np2-0.5.17"
    $candidateService = New-FakeService "candidate-service" "NeProto Windows Service np2-0.5.21"
    $malformedService = New-FakeService "malformed-service" "NeProto service development"

    if ((Get-NeProtoServiceVersion $oldService) -ne "np2-0.5.17") {
        throw "An older stable service version was not parsed"
    }
    Assert-NeProtoStableBaseCompatible -StableVersion "np2-0.5.17" -CandidateVersion "np2-0.5.21"
    Assert-NeProtoServiceVersion -Path $candidateService -ExpectedVersion "np2-0.5.21"
    if ((Get-NeProtoRollbackBaseVersion ([pscustomobject]@{ version = "np2-0.5.21"; base_version = "np2-0.5.17" })) -ne "np2-0.5.17") {
        throw "Rollback did not preserve the installed stable service version"
    }
    if ((Get-NeProtoRollbackBaseVersion ([pscustomobject]@{ version = "np2-0.5.21" })) -ne "np2-0.5.21") {
        throw "Legacy rollback state compatibility was lost"
    }

    $newerRejected = $false
    try {
        Assert-NeProtoStableBaseCompatible -StableVersion "np2-0.5.22" -CandidateVersion "np2-0.5.21"
    } catch {
        $newerRejected = $true
    }
    if (-not $newerRejected) {
        throw "A candidate downgrade over a newer stable service was accepted"
    }

    $differentReleaseLineRejected = $false
    try {
        Assert-NeProtoStableBaseCompatible -StableVersion "np2-0.4.99" -CandidateVersion "np2-0.5.21"
    } catch {
        $differentReleaseLineRejected = $true
    }
    if (-not $differentReleaseLineRejected) {
        throw "A stable service from a different release line was accepted"
    }

    $malformedRejected = $false
    try {
        [void](Get-NeProtoServiceVersion $malformedService)
    } catch {
        $malformedRejected = $true
    }
    if (-not $malformedRejected) {
        throw "Malformed service version output was accepted"
    }
} finally {
    Remove-Item -LiteralPath $temporaryRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "PASS: candidate service version policy accepts safe upgrades and rejects invalid versions."
