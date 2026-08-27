[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "flutter-version-output.ps1")

$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$versionConfig = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot "versions.json") |
    ConvertFrom-Json

$candidates = @()
if (-not [string]::IsNullOrWhiteSpace($env:NEPROTO_FLUTTER_ROOT)) {
    $candidates += Join-Path $env:NEPROTO_FLUTTER_ROOT "bin\flutter.bat"
}
$candidates += Join-Path $root ".tools\flutter\bin\flutter.bat"
$candidates += "D:\NeprotoToolchains\flutter-$($versionConfig.flutter)\flutter\bin\flutter.bat"

$pathFlutter = Get-Command flutter -ErrorAction SilentlyContinue
if ($null -ne $pathFlutter) {
    $candidates += $pathFlutter.Source
}

$flutter = $candidates |
    Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and (Test-Path -LiteralPath $_) } |
    Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($flutter)) {
    throw "Flutter $($versionConfig.flutter) was not found. Set NEPROTO_FLUTTER_ROOT."
}

$versionOutput = @(& $flutter --version --machine 2>&1)
if ($LASTEXITCODE -ne 0) {
    throw "Flutter --version --machine failed"
}
$version = ConvertFrom-FlutterMachineVersionOutput -Output $versionOutput
if ($version.frameworkVersion -ne $versionConfig.flutter) {
    throw "Expected Flutter $($versionConfig.flutter), got $($version.frameworkVersion)."
}
if ($version.dartSdkVersion -notlike "$($versionConfig.dart)*") {
    throw "Expected Dart $($versionConfig.dart), got $($version.dartSdkVersion)."
}

$flutter
