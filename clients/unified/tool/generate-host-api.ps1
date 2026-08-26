[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$flutter = & (Join-Path $PSScriptRoot "resolve-flutter.ps1")
$plugin = (Resolve-Path (Join-Path $PSScriptRoot "..\plugin")).Path
$dart = Join-Path (Split-Path -Parent $flutter) "dart.bat"

Push-Location $plugin
try {
    & $dart pub get
    if ($LASTEXITCODE -ne 0) { throw "dart pub get failed" }
    & $dart run pigeon --input pigeons/client_host_api.dart `
        --dart_out lib/src/generated/client_host_api.g.dart `
        --swift_out ios/neproto_host/Sources/neproto_host/ClientHostApi.g.swift `
        --cpp_header_out windows/include/neproto_host/client_host_api.g.h `
        --cpp_source_out windows/client_host_api.g.cpp `
        --cpp_namespace neproto_host
    if ($LASTEXITCODE -ne 0) { throw "Pigeon generation failed" }
} finally {
    Pop-Location
}
