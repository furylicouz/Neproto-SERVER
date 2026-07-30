[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Xray,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory,

    [string]$ServerAddress = '127.0.0.1',
    [string]$ServerListen = '127.0.0.1',
    [ValidateRange(1024, 65535)]
    [int]$VisionPort = 18443,
    [ValidateRange(1024, 65535)]
    [int]$XHTTPPort = 18444,
    [ValidateRange(1024, 65535)]
    [int]$VisionSOCKSPort = 1081,
    [ValidateRange(1024, 65535)]
    [int]$XHTTPSOCKSPort = 1082,
    [string]$RealityTarget = 'neproto.lyntragram.ru:443',
    [string]$RealityServerName = 'neproto.lyntragram.ru'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$xrayPath = (Resolve-Path -LiteralPath $Xray).Path
$outputPath = [System.IO.Path]::GetFullPath($OutputDirectory)
[System.IO.Directory]::CreateDirectory($outputPath) | Out-Null

function New-Hex([int]$Bytes) {
    $buffer = New-Object byte[] $Bytes
    $random = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($buffer)
    }
    finally {
        $random.Dispose()
    }
    return -join ($buffer | ForEach-Object { $_.ToString('x2') })
}

function Get-XrayKeyMaterial {
    $lines = @(& $xrayPath x25519)
    if ($LASTEXITCODE -ne 0) {
        throw 'xray x25519 failed'
    }
    $private = ''
    $password = ''
    foreach ($line in $lines) {
        if ($line -match '^PrivateKey:\s*(\S+)$') {
            $private = $Matches[1]
        }
        elseif ($line -match '^Password \(PublicKey\):\s*(\S+)$') {
            $password = $Matches[1]
        }
    }
    if (-not $private -or -not $password) {
        throw 'unexpected xray x25519 output'
    }
    return @{ Private = $private; Password = $password }
}

function Get-VLESSEncryption {
    $lines = @(& $xrayPath vlessenc)
    if ($LASTEXITCODE -ne 0) {
        throw 'xray vlessenc failed'
    }
    $decryption = ''
    $encryption = ''
    foreach ($line in $lines) {
        if ($line -match '^"decryption":\s*"([^"]+)"$' -and -not $decryption) {
            $decryption = $Matches[1]
        }
        elseif ($line -match '^"encryption":\s*"([^"]+)"$' -and -not $encryption) {
            $encryption = $Matches[1]
        }
    }
    if (-not $decryption -or -not $encryption) {
        throw 'unexpected xray vlessenc output'
    }
    return @{ Decryption = $decryption; Encryption = $encryption }
}

function Write-PrivateJSON([string]$Name, [object]$Value) {
    $path = Join-Path $outputPath $Name
    $json = $Value | ConvertTo-Json -Depth 20
    [System.IO.File]::WriteAllText($path, $json + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
    return $path
}

$versionLine = (& $xrayPath version | Select-Object -First 1)
if ($LASTEXITCODE -ne 0 -or $versionLine -notmatch '^Xray\s+([0-9.]+)') {
    throw 'unable to read Xray version'
}
$version = $Matches[1]
$uuid = (& $xrayPath uuid | Select-Object -First 1).Trim()
if ($LASTEXITCODE -ne 0 -or $uuid -notmatch '^[0-9a-f-]{36}$') {
    throw 'xray uuid failed'
}
$keys = Get-XrayKeyMaterial
$vlessEncryption = Get-VLESSEncryption
$shortID = New-Hex 8
$xhttpPath = '/' + (New-Hex 16)

$visionServer = @{
    log = @{ loglevel = 'warning' }
    inbounds = @(@{
        listen = $ServerListen; port = $VisionPort; protocol = 'vless'
        settings = @{ clients = @(@{ id = $uuid; flow = 'xtls-rprx-vision' }); decryption = 'none' }
        streamSettings = @{
            network = 'raw'; security = 'reality'
            realitySettings = @{
                show = $false; target = $RealityTarget; xver = 0
                serverNames = @($RealityServerName); privateKey = $keys.Private
                shortIds = @($shortID)
            }
        }
    })
    outbounds = @(@{ protocol = 'freedom'; tag = 'direct' })
}

$visionClient = @{
    log = @{ loglevel = 'warning' }
    inbounds = @(@{
        listen = '127.0.0.1'; port = $VisionSOCKSPort; protocol = 'socks'
        settings = @{ udp = $true }
    })
    outbounds = @(@{
        protocol = 'vless'; tag = 'proxy'
        settings = @{
            address = $ServerAddress; port = $VisionPort; id = $uuid
            encryption = 'none'; flow = 'xtls-rprx-vision'
        }
        streamSettings = @{
            network = 'raw'; security = 'reality'
            realitySettings = @{
                serverName = $RealityServerName; fingerprint = 'chrome'
                password = $keys.Password; shortId = $shortID; spiderX = '/'
            }
        }
    })
}

$xhttpServer = @{
    log = @{ loglevel = 'warning' }
    inbounds = @(@{
        listen = $ServerListen; port = $XHTTPPort; protocol = 'vless'
        settings = @{
            clients = @(@{ id = $uuid; flow = '' })
            decryption = $vlessEncryption.Decryption
        }
        streamSettings = @{
            network = 'xhttp'; security = 'reality'
            xhttpSettings = @{ path = $xhttpPath; mode = 'auto' }
            realitySettings = @{
                show = $false; target = $RealityTarget; xver = 0
                serverNames = @($RealityServerName); privateKey = $keys.Private
                shortIds = @($shortID)
            }
        }
    })
    outbounds = @(@{ protocol = 'freedom'; tag = 'direct' })
}

$xhttpClient = @{
    log = @{ loglevel = 'warning' }
    inbounds = @(@{
        listen = '127.0.0.1'; port = $XHTTPSOCKSPort; protocol = 'socks'
        settings = @{ udp = $true }
    })
    outbounds = @(@{
        protocol = 'vless'; tag = 'proxy'
        settings = @{
            address = $ServerAddress; port = $XHTTPPort; id = $uuid
            encryption = $vlessEncryption.Encryption; flow = ''
        }
        streamSettings = @{
            network = 'xhttp'; security = 'reality'
            xhttpSettings = @{ path = $xhttpPath; mode = 'auto' }
            realitySettings = @{
                serverName = $RealityServerName; fingerprint = 'chrome'
                password = $keys.Password; shortId = $shortID; spiderX = '/'
            }
        }
    })
}

$manifest = @{
    schema = 'np2-xray-lab-manifest/v1'
    xray_version = $version
    server_address = $ServerAddress
    server_listen = $ServerListen
    reality_target = $RealityTarget
    reality_server_name = $RealityServerName
    profiles = @(
        @{ name = 'vless-reality-vision'; server_port = $VisionPort; socks_port = $VisionSOCKSPort },
        @{ name = 'vless-xhttp-reality-vlessenc'; server_port = $XHTTPPort; socks_port = $XHTTPSOCKSPort }
    )
}

$written = @(
    (Write-PrivateJSON 'vision-server.json' $visionServer),
    (Write-PrivateJSON 'vision-client.json' $visionClient),
    (Write-PrivateJSON 'xhttp-server.json' $xhttpServer),
    (Write-PrivateJSON 'xhttp-client.json' $xhttpClient),
    (Write-PrivateJSON 'manifest.json' $manifest)
)

foreach ($path in $written[0..3]) {
    & $xrayPath run -test -c $path
    if ($LASTEXITCODE -ne 0) {
        throw "Xray rejected generated configuration: $([System.IO.Path]::GetFileName($path))"
    }
}

Write-Output "Prepared Xray $version lab configs in $outputPath"
