# ADR-003: Continuity-first multi-carrier sessions

- Status: Accepted for incremental implementation
- Date: 2026-07-18

## Context

NP/2 v2.2 authenticates and encrypts an original multiplexed session over real
HTTPS/WebSocket, HTTP/3/WebTransport, or WebRTC carriers. Carrier fallback and
the adaptive HTTPS pool improve availability and throughput, but an individual
logical TCP flow is still owned by one authenticated carrier session. Closing
that carrier closes the flow. Several long-lived carriers can also become an
observable connection-lifecycle pattern.

## Decision

Introduce an optional NP/2 Constellation layer above authenticated carrier
sessions:

1. A logical constellation owns bounded, independently authenticated carrier
   leases.
2. Logical flow IDs and byte offsets are independent of session-local stream
   IDs.
3. A bounded replay journal retains only bytes not cumulatively acknowledged by
   the peer.
4. A new lease attaches only with a short-lived ticket issued inside an already
   authenticated and cell-encrypted NP/2 session.
5. Capability negotiation is additive. Without explicit mutual support, both
   peers retain exact NP/2 v2.2 behavior.
6. Carrier-native grammar controls the lifecycle of leases; it never emits
   arbitrary out-of-protocol bytes.

## Consequences

### Positive

- Wi-Fi/LTE and carrier migration can preserve active logical flows.
- Carrier selection becomes an implementation detail instead of session
  identity.
- Bulk and latency-sensitive traffic can use different healthy leases.
- Future carrier behavior can evolve without changing the core flow API.

### Negative

- Correct byte replay and acknowledgement semantics are substantially more
  complex than reconnecting a proxy stream.
- Server state must survive physical carrier loss for a short bounded window.
- Multiple leases can create a new statistical fingerprint if lifecycle rules
  are not validated against matched control traffic.
- Memory accounting becomes part of the security boundary.

## Rejected alternatives

- **Add more padding to the existing tunnel:** does not solve carrier-bound flow
  lifetime and can reduce both performance and camouflage.
- **Change the existing cell-kind table:** breaks the authenticated type-map
  contract before capabilities can be negotiated.
- **Resume by redialing the destination:** may duplicate non-idempotent
  application operations and is not true flow continuity.
- **Execute server-provided scripts:** creates an unacceptable remote-code and
  validation boundary; manifests remain constrained data.
