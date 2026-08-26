[CmdletBinding()]
param(
    [switch]$BuildOnly
)

$ErrorActionPreference = "Stop"

if (-not $BuildOnly) {
    throw "Unified Windows verification is build-only on this machine. Pass -BuildOnly."
}

$flutter = & (Join-Path $PSScriptRoot "resolve-flutter.ps1")
$plugin = (Resolve-Path (Join-Path $PSScriptRoot "..\plugin")).Path
$dart = Join-Path (Split-Path -Parent $flutter) "dart.bat"

Push-Location $plugin
try {
    & $dart pub get
    if ($LASTEXITCODE -ne 0) { throw "dart pub get failed" }
    & $flutter analyze
    if ($LASTEXITCODE -ne 0) { throw "flutter analyze failed" }
    & $flutter test
    if ($LASTEXITCODE -ne 0) { throw "flutter test failed" }
} finally {
    Pop-Location
}

$app = Join-Path (Split-Path -Parent $plugin) "app"
if (Test-Path -LiteralPath (Join-Path $app "pubspec.yaml")) {
    Push-Location $app
    try {
        & $dart pub get
        if ($LASTEXITCODE -ne 0) { throw "app dart pub get failed" }
        & $flutter analyze
        if ($LASTEXITCODE -ne 0) { throw "app flutter analyze failed" }
        & $flutter test
        if ($LASTEXITCODE -ne 0) { throw "app flutter test failed" }
        & $flutter build windows --release
        if ($LASTEXITCODE -ne 0) { throw "Flutter Windows release build failed" }
    } finally {
        Pop-Location
    }
}
