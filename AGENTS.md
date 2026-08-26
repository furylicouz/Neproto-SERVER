# Neproto Engineering Rules

## Stack

- Go 1.26.7
- Pion WebRTC v4.2.16
- Coder WebSocket v1.8.15
- uTLS v1.8.0, optional and isolated
- Caddy v2.11.4 for production ingress

## Commands

- Format: `gofmt -w ./cmd ./internal ./tests`
- Test: `go test -race -coverprofile=coverage.out ./...`
- Vet: `go vet ./...`
- Build: `go build ./cmd/...`
- Vulnerabilities: `govulncheck ./...`
- Fuzz smoke: targeted `go test -run=^$ -fuzz=<name> -fuzztime=30s`

## Conventions

- Start each behavior with a failing test.
- Keep core protocol packages independent of carrier libraries.
- Validate and cap every network/config input before allocation or side effects.
- Use `context.Context` for blocking work and make cancellation ownership explicit.
- Prefer sentinel errors and stable categories; never return raw internal errors over the wire.
- No global mutable session state. Inject clocks, randomness, and dialers for deterministic tests.
- Every cache, queue, goroutine, message, deadline, and retry count is bounded.
- Benchmark before performance optimization.

## Security Boundaries

- Never invent cryptographic primitives or disable TLS/DTLS verification.
- Never put secrets in Git, logs, process arguments, generated URLs, or test fixtures.
- Never append arbitrary bytes outside a standards-compliant carrier.
- Never bind the NP/2 backend publicly by default.
- Never dial a target before session authentication and destination-policy validation.
- Never modify SSH, firewall, Zabbix, or public VPS state without the requested deployment phase.

## Protocol Sources

- `docs/SPEC.md` is the wire and behavior contract.
- `docs/PLAN.md` defines implementation order and checkpoints.
- Protocol behavior changes require updating the spec first.
