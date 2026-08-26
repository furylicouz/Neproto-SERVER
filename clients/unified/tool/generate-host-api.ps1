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
    & $dart run pigeon --input pigeons/client_host_api.dart
    if ($LASTEXITCODE -ne 0) { throw "Pigeon generation failed" }

    $generatedFiles = @(
        "lib\src\generated\client_host_api.g.dart",
        "ios\neproto_host\Sources\neproto_host\ClientHostApi.g.swift",
        "windows\include\neproto_host\client_host_api.g.h",
        "windows\client_host_api.g.cpp"
    )
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    foreach ($generatedFile in $generatedFiles) {
        $path = Join-Path $plugin $generatedFile
        $contents = [System.IO.File]::ReadAllText($path)
        $normalized = [regex]::Replace(
            $contents,
            '[\t ]+(?=\r?$)',
            '',
            [System.Text.RegularExpressions.RegexOptions]::Multiline
        )
        if ($normalized -ne $contents) {
            [System.IO.File]::WriteAllText($path, $normalized, $utf8NoBom)
        }
    }
} finally {
    Pop-Location
}
