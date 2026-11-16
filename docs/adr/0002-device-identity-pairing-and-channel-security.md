# ADR-0002: Device identity, SAS pairing and end-to-end channel security

Status: Accepted (approved by project owner 2026-11-16; see docs/research/M0-decisions.md)

## Context
- Home Assistant has no scoped tokens, so putting any HA token on the device breaks the spec's "no unrestricted HA token" rule.
- Local HA is commonly plain HTTP. HA Cloud gives TLS to the HA host, but network proximity must not grant trust (spec 10).
- The spike used a bare challenge/response. That authenticates the connection but gives no confidentiality or integrity afterwards, so it is not acceptable for production.

## Decision
- **Identity:** at install the agent generates a static X25519 key (channel) and an Ed25519 key (signing: diagnostics, audit export). `device_id` = base32(SHA-256(ed25519_pub))[:26], stable for the life of the install. Keys live in `$XDG_STATE_HOME/deckbeacon/identity/` (0600). The integration generates its own static key per config entry.
- **Pairing:**
  1. The user opens a pairing window in HA (Add Integration → DeckBeacon, or the "Discovered" card). It lasts at most 10 minutes. Outside a window `/pair` returns 404.
  2. The agent connects to `/pair` and runs a Noise `XX` handshake, exchanging static keys.
  3. Both sides derive a 6-digit SAS from the handshake hash. HA shows it, and the user confirms the same SAS on the device (Decky, Desktop loopback UI, or CLI). Both confirmations are required.
  4. HA stores the device's static public key in the config entry. The agent stores HA's key plus the connection profile (internal URL, external/cloud URL).
- **Channel:** each connection runs Noise `IK` (agent knows HA's static key), giving mutual authentication, forward secrecy, and AEAD framing inside the WebSocket. HA's TLS (where present) is defence in depth only.
- **Implementation:** Go uses `github.com/flynn/noise`. Python uses a small Noise implementation on top of `cryptography` (X25519, ChaCha20-Poly1305, BLAKE2s/SHA-256, HKDF), pinned to shared cross-language test vectors in `packages/protocol/fixtures`. Library choice is validated in M1.
- **Anti-replay:** Noise nonces cover each session. Each command carries an ID, TTL, and sequence number, and the agent keeps a durable dedupe table. Events carry an ID, sequence, a live/backfill flag, and timestamps (spec 07).
- **Revocation and rotation:** deleting the device in HA revokes it, and the next connect is refused with `revoked`. Rotation is an authenticated in-channel key-change message plus user confirmation.
- **Clones:** a second live session for the same device ID from a different connection raises an HA repair issue. The newest session wins only after the old one stops responding to a liveness ping. Otherwise both are dropped (clone suspected).

## Consequences
- No HA credentials ever reach the device. The device can only use the narrow DeckBeacon protocol.
- The integration carries crypto code, so it needs cross-language test vectors and fuzzing of the handshake parser.
- Pairing needs a human on both sides, which matches spec 10.

## Validation
M1: protocol fixtures and test vectors. M2: pairing integration tests, a replay test, a revocation test, and a MITM test (a proxy swaps keys, so the SAS mismatches and pairing aborts).
