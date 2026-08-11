# Windows client implementation plan

| Phase | Acceptance gate |
| --- | --- |
| Core contracts | Onboarding conversion, state machine, IPC bounds and route plans have focused tests. |
| Windows data plane | Wintun opens, endpoint exclusions precede half-default routes, and rollback is idempotent. |
| Service | Connect/disconnect/import/list/remove/status/log operations work through an ACL-restricted named pipe. |
| Catalogue | Signed catalogues are verified and persisted; server selection and route snapshots survive restart. |
| UI | WPF home, server picker, routes, diagnostics and import flows match the iOS information architecture. |
| Packaging | One `NeProto-Setup-<version>-x64.exe` installs, upgrades and uninstalls on a clean Windows VM. |
| Release | Go race tests, vet, Windows builds, .NET tests/build and installer smoke checks pass. |

The implementation proceeds risk-first: route planning and cleanup, then the
service contract, then UI and packaging. No phase may silently fall back to a
system-wide SOCKS proxy.
