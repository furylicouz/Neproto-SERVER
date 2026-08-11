# NeProto for Windows

NeProto for Windows is a native desktop client for the same direct NP/2 data
plane used by the iOS Packet Tunnel. It is not a browser SOCKS wrapper.

## Build

Requirements:

- Windows 10/11 x64
- Go toolchain declared by the repository
- .NET 8 SDK with Windows Desktop support

From the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File .\clients\windows\build.ps1
```

The output is `dist\windows\NeProto-Setup-<version>-x64.exe` plus its SHA-256
file. The setup executable contains the self-contained WPF application, the Go
Windows service and the official Wintun x64 runtime.

## Runtime layout

- `%ProgramFiles%\NeProto\NeProto.exe` — desktop UI.
- `%ProgramFiles%\NeProto\NeProto.Service.exe` — LocalSystem tunnel service.
- `%ProgramData%\NeProto` — DPAPI-protected profiles, route recovery journal
  and bounded service log.
- `\\.\pipe\NeProto.Service.v1` — local-only, length-bounded IPC.

The installer must be code-signed before public distribution. Wintun is
redistributed under its included license.

## Diagnostic commands

```powershell
& "$env:ProgramFiles\NeProto\NeProto.Service.exe" --probe
& "$env:ProgramFiles\NeProto\NeProto.Service.exe" --check
```

Uninstall from Windows Installed apps, or run:

```powershell
& "$env:ProgramFiles\NeProto\NeProto.Uninstall.exe" /uninstall
```
