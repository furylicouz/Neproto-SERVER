# NP/2 Constellation Protocol Contract

## Status

This is an additive, capability-gated contract for the implementation sequence
in `NP2-CONSTELLATION-IMPLEMENTATION-PLAN.md`. It does not change NP/2 v2.2
behavior until both peers explicitly negotiate a Constellation capability.

## Goals

- Preserve logical TCP flows across replacement authenticated carriers.
- Bound every ticket, lease, flow, replay byte, retry, and deadline.
- Keep all control messages inside the existing cell AEAD and outer carrier
  security.
- Keep carrier behavior standards-compliant and carrier-specific.
- Preserve legacy interoperability when the feature is unavailable.

## Non-goals

- No cross-user migration.
- No migration to a server that does not already share authenticated
  Constellation state.
- No infinite TCP replay window.
- No custom cryptographic primitive.
- No promise that carrier metadata is indistinguishable from a named service.

## Capability

`CapabilityConstellationContinuity` is the next additive extension bit after
`CapabilityMosaicCover`. A peer must not send a Constellation control envelope
unless this capability was selected by both peers. A legacy peer ignores the
unoffered capability and continues with NP/2 v2.2.

## Control envelope v1

The first implementation slice defines a canonical control envelope carried as
the value of an optional extension TLV inside `CellProfile`. The entire value is
therefore protected by the directional NP/2 cell AEAD and the outer carrier.

Common fields:

```text
magic             4 bytes: NPCT
version           1 byte: 1
message_type      1 byte
message_id        canonical uvarint, 1..MaxSequence
constellation_id  16 non-zero random bytes
flow_id            16 bytes; zero only for lease-level messages
send_offset       canonical uvarint
receive_offset    canonical uvarint
token_length      canonical uvarint
token             0..512 bytes
```

Initial message types:

| Type | Direction | Flow ID | Token | Meaning |
|---|---|---:|---:|---|
| `ConstellationCreate` | client to server | zero | empty | Create a new bounded logical constellation |
| `LeaseIssue` | server to client | zero | 32..512 | Issue a short-lived attach ticket |
| `LeaseAttach` | client to server | zero | 32..512 | Attach an independently authenticated carrier |
| `LeaseAccept` | server to client | zero | 32..512 | Confirm attach and rotate the ticket |
| `FlowResume` | either | non-zero | empty | Request resume at authenticated offsets |
| `FlowAck` | either | non-zero | empty | Cumulatively acknowledge offsets |
| `FlowAbort` | either | non-zero | empty | Stop continuity for one flow |

Offsets and token fields that are not meaningful for a message type must be
zero/empty. Trailing bytes, non-canonical varints, zero message IDs, zero
constellation IDs, invalid flow-ID presence, and oversized tokens are protocol
errors. Duplicate identical message IDs are idempotent; conflicting reuse is a
protocol error under the existing extension replay rules.

## Ticket security

- A ticket is random opaque data and is transmitted only inside an authenticated
  NP/2 session.
- The server binds it to the authenticated user, constellation ID, issuing
  transcript, expiry, and remaining-use count.
- A successful attach consumes and rotates it.
- Ticket lookup never authorizes a destination dial.
- Expired and consumed records are removed by bounded cleanup.

## Flow continuity invariants

- `send_offset` is the next byte the sender intends to place on the replacement
  physical stream.
- `receive_offset` cumulatively acknowledges all bytes below that value.
- Offsets never regress and never exceed bytes produced by that direction.
- An authenticated acknowledgement may race the local return from the physical
  `Write`. An implementation may stage such an acknowledgement only within the
  bounded active write range, but it must not retire replay bytes until the
  local write result confirms that offset.
- Only unacknowledged bytes may exist in a replay journal.
- A flow may have only one accepted lease epoch per direction at a time.
- After a higher authenticated lease epoch is accepted, the receiver detaches
  the superseded physical stream before publishing the replacement; losing the
  old carrier and receiving the replacement may race without aborting the
  logical flow or redialling its target.
- Resume conflicts abort the flow instead of guessing.
- Budget exhaustion disables continuity for the affected flow; it must not
  allocate unbounded memory or silently drop bytes.

## Compatibility and rollout

The capability is initially disabled in production configuration. Server-first
deployment must preserve legacy clients. Activation proceeds through a canary
credential only after codec, fuzz, replay-journal, race, migration, and rollback
gates pass.
