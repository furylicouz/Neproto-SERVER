[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "flutter-version-output.ps1")

$bootstrapOutput = @(
    "Building flutter tool..."
    "Running pub upgrade..."
    '{"frameworkVersion":"3.44.7","dartSdkVersion":"3.12.2"}'
)
$version = ConvertFrom-FlutterMachineVersionOutput -Output $bootstrapOutput
if ($version.frameworkVersion -ne "3.44.7" -or $version.dartSdkVersion -ne "3.12.2") {
    throw "Flutter machine version parser returned the wrong version"
}

$rejected = $false
try {
    [void](ConvertFrom-FlutterMachineVersionOutput -Output @("Building flutter tool..."))
} catch {
    $rejected = $true
}
if (-not $rejected) {
    throw "Flutter machine version parser accepted output without JSON"
}

Write-Host "PASS: Flutter bootstrap output is parsed safely."
