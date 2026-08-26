# NeProto Windows HTTP/3-only candidate

This unsigned QA overlay is for the separate Windows test PC only. It does not
modify the server and it is not installed by NeProto Web.

## Prerequisite

Install the matching stable `NeProto-Setup-np2-0.5.19-x64.exe` first. Keep the
candidate ZIP extracted on disk until testing and rollback are complete.

## Install

1. Disconnect and close the stable NeProto UI.
2. Open PowerShell **as Administrator** in the extracted candidate directory.
3. Run `powershell -ExecutionPolicy Bypass -File .\Install-Candidate.ps1`.
4. Start `C:\Program Files\NeProto Candidate\neproto_client.exe` manually.

The script verifies every candidate file, preserves `%ProgramData%\NeProto`,
backs up the stable service/Wintun, replaces only those two stable runtime
files, starts and probes the candidate service, and never launches the UI.

## Rollback

Disconnect and close every NeProto UI. From the extracted candidate directory,
run as Administrator:

```powershell
powershell -ExecutionPolicy Bypass -File .\Rollback-Candidate.ps1
```

Rollback verifies the saved stable-file hashes, restores the stable service,
probes it, removes only `C:\Program Files\NeProto Candidate`, and retains the
completed rollback record below `%ProgramData%\NeProto\candidate-backups`.

## HTTP/3-only acceptance

- Import or reuse the profile, connect, and confirm carrier
  `http3_webtransport`.
- Test YouTube seek/play for 10 minutes, Telegram media upload/download, and
  Instagram video/feed for 10 minutes.
- Restart only the Flutter UI and confirm native tunnel status resynchronizes.
- Block outbound UDP/443: connection must fail in the HTTP/3 stage with no
  WebRTC or HTTPS/WebSocket attempt.
- Unblock UDP/443, reconnect, then disconnect and confirm routes/tunnel clean up.
- Preserve the UI diagnostics and matching server log interval as evidence.
