# NP/2 Constellation Console UX Plan

## Goal

Replace the current launcher-style TUI with a persistent cinematic server
console. Opening an item must switch the central workspace in place; it must
never suspend tcell or drop the operator into the ordinary terminal.

## UX contract

- The header, system monitor, network map, filesystem rail, and footer remain
  visible while the central workspace changes.
- `Enter` opens a workspace. `Esc` returns one level; only `q` from the
  dashboard exits the application.
- Read-only pages refresh in place. Mutations use an in-TUI form or confirmation
  overlay and show their result in an internal output panel.
- The map uses a native offline Braille renderer inspired by MapSCII. No public
  telnet session, remote JavaScript, Node runtime, or unauthenticated tile
  service is required on the VPN server.
- Credentials, private routes, certificate contents, and traffic destinations
  are never displayed in dashboard telemetry.

## Vertical slices

### Slice 1: Persistent navigation

- [ ] State machine covers dashboard and every management workspace.
- [ ] Enter changes the central workspace without `Screen.Suspend`.
- [ ] Esc returns to the dashboard and preserves the live shell frame.
- [ ] Simulation-screen tests cover navigation and responsive rendering.

### Slice 2: Operational pages

- [ ] Status, users, service, domain, backups, storage, and events render inside
  the central workspace.
- [ ] Lists scroll and retain selection independently.
- [ ] Service logs and command results are bounded and sanitized.

### Slice 3: Safe actions

- [ ] Add/rotate/revoke/export-user workflows stay in TUI overlays.
- [ ] Service start/stop/restart and configuration validation stay in TUI.
- [ ] Domain, feature, backup, and restore mutations retain confirmations,
  validation, rollback, and readiness checks.

### Slice 4: MapSCII-style network map

- [ ] Braille world map renders offline at arbitrary panel sizes.
- [ ] Public server coordinates are marked without exposing client locations.
- [ ] Map workspace supports pan/zoom/reset with keyboard and mouse.
- [ ] MIT MapSCII inspiration/attribution and map-data provenance are recorded.

### Slice 5: Release gates

- [ ] `go test ./...`, `go vet ./...`, and Linux race tests pass.
- [ ] Bare-metal and Docker release-bundle smoke tests pass.
- [ ] A real SSH pseudo-TTY confirms persistent in-place navigation.
- [ ] VPS update is atomic and keeps the VPN data plane online.

## Risks

| Risk | Mitigation |
|---|---|
| Terminal smaller than cinematic layout | Responsive compact workspace, no broken controls |
| Long service action freezes the UI | Bounded background jobs with visible progress |
| Accidental destructive action | Explicit in-TUI confirmation phrase |
| Secret leakage in logs or exports | Bounded sanitized buffers and opt-in credential reveal |
| External MapSCII availability/security | Native offline renderer; no telnet or HTTP dependency |

## References

- eDEX-UI visual references supplied by the user.
- MapSCII: <https://github.com/rastapasta/mapscii>
- tcell: <https://pkg.go.dev/github.com/gdamore/tcell/v2>
