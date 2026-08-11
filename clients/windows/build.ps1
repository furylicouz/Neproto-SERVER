param(
    [string]$Version = "",
    [string]$Configuration = "Release"
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = (Get-Content -Raw (Join-Path $root "VERSION")).Trim()
}
if ($Version -notmatch '^np2-[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "Invalid NP/2 version: $Version"
}

$versionNumber = $Version.Substring(4)
$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }
$env:GOPATH = Join-Path $root ".tools\gopath"
$env:GOCACHE = Join-Path $root ".tools\gocache"
$env:GOMODCACHE = Join-Path $root ".tools\gomodcache"

$work = Join-Path $root ".tmp\windows-package"
$payload = Join-Path $work "payload"
$publish = Join-Path $work "publish"
$output = Join-Path $root "dist\windows"
Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $payload, $publish, $output | Out-Null

& $go build -trimpath -ldflags "-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$Version" -o (Join-Path $payload "NeProto.Service.exe") ./cmd/neproto-windows-service
if ($LASTEXITCODE -ne 0) { throw "NeProto.Service.exe build failed" }

dotnet test (Join-Path $PSScriptRoot "NeProto.App.Tests\NeProto.App.Tests.csproj") `
    -c $Configuration --verbosity minimal
if ($LASTEXITCODE -ne 0) { throw "NeProto.exe tests failed" }

dotnet restore (Join-Path $PSScriptRoot "NeProto.App\NeProto.App.csproj") -r win-x64
if ($LASTEXITCODE -ne 0) { throw "NeProto.exe restore failed" }

dotnet publish (Join-Path $PSScriptRoot "NeProto.App\NeProto.App.csproj") `
    -c $Configuration -r win-x64 --self-contained true --no-restore `
    -p:Version=$versionNumber -p:PublishSingleFile=true -p:DebugType=None `
    -o $publish
if ($LASTEXITCODE -ne 0) { throw "NeProto.exe publish failed" }
Copy-Item -LiteralPath (Join-Path $publish "NeProto.exe") -Destination (Join-Path $payload "NeProto.exe")

$client = Join-Path $payload "NeProto.exe"
$smoke = Start-Process -FilePath $client -ArgumentList "--smoke-test" -PassThru
if (-not $smoke.WaitForExit(15000)) {
    Stop-Process -Id $smoke.Id -Force -ErrorAction SilentlyContinue
    throw "NeProto.exe isolated payload smoke test timed out"
}
if ($smoke.ExitCode -ne 0) {
    throw "NeProto.exe isolated payload smoke test failed with exit code $($smoke.ExitCode)"
}

$serviceSmokeData = Join-Path $work "service-smoke"
New-Item -ItemType Directory -Force -Path $serviceSmokeData | Out-Null
$serviceBinary = Join-Path $payload "NeProto.Service.exe"
$serviceProcess = $null
$installedService = Get-Service -Name "NeProtoService" -ErrorAction SilentlyContinue
$restartInstalledService = $false
$skipServiceWorkflowSmoke = $false
try {
    if ($installedService -and $installedService.Status -eq "Running") {
		try {
			Stop-Service -Name "NeProtoService" -Force -ErrorAction Stop
			$installedService.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(20))
			$restartInstalledService = $true
		} catch {
			Write-Warning "Installed NeProtoService cannot be stopped; running non-IPC service smoke only. CI still runs the isolated IPC workflow."
			$skipServiceWorkflowSmoke = $true
		}
    }
	if ($skipServiceWorkflowSmoke) {
		$validStaleJournal = '{"apply":[],"rollback":[{"kind":"tunnel-route","add":false,"family":4,"destination":"0.0.0.0/1","interface_index":9999,"next_hop":"0.0.0.0"},{"kind":"tunnel-route","add":false,"family":4,"destination":"128.0.0.0/1","interface_index":9999,"next_hop":"0.0.0.0"},{"kind":"tunnel-route","add":false,"family":6,"destination":"::/1","interface_index":9999,"next_hop":"::"},{"kind":"tunnel-route","add":false,"family":6,"destination":"8000::/1","interface_index":9999,"next_hop":"::"},{"kind":"endpoint-exclusion","add":false,"family":4,"destination":"203.0.113.1/32","interface_index":9998,"next_hop":"192.0.2.1"},{"kind":"configure-adapter","add":false,"family":0,"interface_index":9999,"adapter_name":"NeProto"}]}'
		$journal = Join-Path $serviceSmokeData "active-routes.json"
		Set-Content -LiteralPath $journal -Value $validStaleJournal -Encoding ascii
		& $serviceBinary --cleanup --data-dir $serviceSmokeData
		if ($LASTEXITCODE -ne 0 -or (Test-Path -LiteralPath $journal)) {
			throw "NeProto.Service.exe stale route recovery smoke test failed"
		}
	} else {
		Set-Content -LiteralPath (Join-Path $serviceSmokeData "active-routes.json") -Value '{"invalid":true}' -Encoding ascii
		$serviceOut = Join-Path $work "service-smoke-console.out.log"
		$serviceErr = Join-Path $work "service-smoke-console.err.log"
		$serviceProcess = Start-Process -FilePath $serviceBinary `
			-ArgumentList "--console", "--data-dir", $serviceSmokeData -WindowStyle Hidden -PassThru `
			-RedirectStandardOutput $serviceOut -RedirectStandardError $serviceErr
		$deadline = (Get-Date).AddSeconds(20)
		do {
			Start-Sleep -Milliseconds 250
			if ($serviceProcess.HasExited) { throw "NeProto.Service.exe recovery smoke test exited early" }
			& $serviceBinary --probe *> $null
			$probeExitCode = $LASTEXITCODE
		} while ($probeExitCode -ne 0 -and (Get-Date) -lt $deadline)
		if ($probeExitCode -ne 0) { throw "NeProto.Service.exe recovery smoke test did not expose IPC" }

		$serviceClientSmoke = Start-Process -FilePath $client -ArgumentList "--service-smoke-test" -PassThru
		if (-not $serviceClientSmoke.WaitForExit(20000)) {
			Stop-Process -Id $serviceClientSmoke.Id -Force -ErrorAction SilentlyContinue
			throw "NeProto.exe service workflow smoke test timed out"
		}
		if ($serviceClientSmoke.ExitCode -ne 0) {
			throw "NeProto.exe service workflow smoke test failed with exit code $($serviceClientSmoke.ExitCode)"
		}
	}
} finally {
    if ($serviceProcess -and -not $serviceProcess.HasExited) {
        Stop-Process -Id $serviceProcess.Id -Force -ErrorAction SilentlyContinue
    }
	if ($restartInstalledService) {
		Start-Service -Name "NeProtoService"
		(Get-Service -Name "NeProtoService").WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(20))
	}
}

Copy-Item -LiteralPath (Join-Path $PSScriptRoot "ThirdParty\wintun\wintun.dll") -Destination (Join-Path $payload "wintun.dll")
Copy-Item -LiteralPath (Join-Path $PSScriptRoot "ThirdParty\wintun\LICENSE.txt") -Destination (Join-Path $payload "WINTUN-LICENSE.txt")

$setupBase = Join-Path $work "NeProto.Setup.base.exe"
& $go build -trimpath -ldflags "-s -w -H windowsgui -X neproto.local/chameleon/internal/buildinfo.Version=$Version" -o $setupBase ./cmd/neproto-windows-setup
if ($LASTEXITCODE -ne 0) { throw "NeProto setup bootstrap build failed" }
Copy-Item -LiteralPath $setupBase -Destination (Join-Path $payload "NeProto.Uninstall.exe")

$archive = Join-Path $work "payload.zip"
Compress-Archive -Path (Join-Path $payload "*") -DestinationPath $archive -CompressionLevel Optimal
$destination = Join-Path $output "NeProto-Setup-$Version-x64.exe"
Copy-Item -LiteralPath $setupBase -Destination $destination -Force

$archiveBytes = [System.IO.File]::ReadAllBytes($archive)
$stream = [System.IO.File]::Open($destination, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
try {
    $stream.Write($archiveBytes, 0, $archiveBytes.Length)
    $length = [System.BitConverter]::GetBytes([UInt64]$archiveBytes.Length)
    $stream.Write($length, 0, $length.Length)
    $magic = [byte[]](0x4e,0x50,0x32,0x57,0x49,0x4e,0x53,0x45,0x54,0x55,0x50,0x56,0x31,0,0,0)
    $stream.Write($magic, 0, $magic.Length)
    $stream.Flush($true)
} finally {
    $stream.Dispose()
}

$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $destination).Hash.ToLowerInvariant()
Set-Content -LiteralPath "$destination.sha256" -Value "$hash  $([System.IO.Path]::GetFileName($destination))" -Encoding ascii
Write-Host "Built $destination"
Write-Host "SHA256 $hash"
