# NeProto Windows Client

## Status

This document defines the production Windows adapter for the NP/2 wire and
behaviour contract in `docs/SPEC.md`. It does not change the NP/2 wire format.

## Supported platform

- Windows 10 22H2 and Windows 11, x64.
- Installation and removal use one signed-ready setup executable.
- The interactive application never requires administrator privileges after
  installation.

## Components

| Component | Responsibility |
| --- | --- |
| `NeProto.exe` | Native WPF UI: connection, server catalogue, routes, diagnostics and profile import. |
| `NeProto.Service.exe` | LocalSystem Windows service that owns NP/2 sessions, Wintun, DNS and route changes. |
| `NeProto.Setup.exe` | Elevating installer/uninstaller that deploys both components and registers the service. |

The GUI and service communicate only over a local Windows named pipe. The pipe
has an explicit security descriptor and length-bounded request/response frames.
The service never returns profile root secrets through the pipe or logs.

## Profile storage

- The service accepts only validated `np2://import/v1/` and
  `np2://import/v2/` onboarding URIs.
- Public profile metadata is stored under `%ProgramData%\NeProto`.
- Root secrets are encrypted with Windows DPAPI in local-machine scope and the
  containing directory is restricted to SYSTEM and Administrators.
- A random per-installation NP/2 device identifier is generated once. Hardware
  identifiers, MAC addresses and Windows account names are not used.

## Connection lifecycle

1. Validate and load the selected profile.
2. Establish and authenticate an NP/2 carrier on the physical network.
3. Capture the original IPv4/IPv6 route for every numeric carrier endpoint.
4. Create the `NeProto` Wintun adapter and configure tunnel addresses and DNS.
5. Add endpoint host-route exclusions through the original interfaces.
6. Add two half-default routes per address family through Wintun.
7. Attach the existing NP/2 userspace TCP/IP stack to Wintun.
8. Report live carrier, traffic and catalogue state to the GUI.

On stop, failure, service shutdown or next service start, active-store routes
recorded in the recovery journal are removed before the Wintun device is
closed. Failure to install endpoint exclusions aborts connection before a
default route is installed.

## DNS and leak behaviour

The Wintun interface uses explicit DNS servers and receives the preferred route
metric while connected. IPv4 and IPv6 are configured together. If the platform
cannot configure either family safely, connection fails closed instead of
leaving that family outside the tunnel.

## Cluster catalogue and routes

The service fetches the catalogue only through an authenticated NP/2 session.
It verifies the Ed25519 signature against the public key pinned by onboarding,
enforces cluster identity, expiry and non-decreasing revision, then publishes
the verified server and route snapshot to the GUI. Client routes are accepted
only when the verified catalogue permission allows them.

## IPC bounds

- UTF-8 JSON, one request and one response per pipe connection.
- Maximum request and response size: 256 KiB.
- Unknown fields and trailing JSON are rejected.
- At most 16 concurrent pipe clients.
- Mutating calls are serialized by the service controller.

## Installer guarantees

The setup executable is safe to rerun. It stops the old service, atomically
replaces versioned application files, registers an automatic delayed-start
service, creates Start Menu entries and starts the service. Uninstall first
disconnects and removes all active NP/2 routes, then deletes the service and
application files. User profiles are retained unless explicit data removal is
selected.
