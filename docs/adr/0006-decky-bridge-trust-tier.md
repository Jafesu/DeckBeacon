# ADR-0006: Decky bridge as a low-trust local client

Status: Accepted (approved by project owner 2026-11-16; see docs/research/M0-decisions.md)

## Context
- Decky Loader runs as root. Plugin frontends share Steam's single CEF JS context, so any installed plugin can reach any other plugin's backend callables. Plugin backends run as `deck` unless they request `root`.
- `@decky/api` toasts carry only a single `onClick`, with no action buttons.

## Decision
- The DeckBeacon Decky plugin does **not** request the `root` flag. Its backend talks to the agent over a Unix socket at `$XDG_RUNTIME_DIR/deckbeacon/decky.sock` (0600, dir 0700, peer UID checked with `SO_PEERCRED`), using a local token minted by the agent and stored in the plugin's settings dir.
- Privilege tier `local-ui-low`, the only things the bridge can do:
  - read status, capabilities, and the notification inbox
  - acknowledge or answer notifications delivered to this device
  - confirm a pairing SAS that the user also confirms in HA
  - relay Tier C Steam adapter results and requests, where the agent originates the request and validates the response
- It **cannot** change permissions, approve agent updates on its own (HA-side approval is still required), run power or admin commands, or unpair.
- Notification UX: a toast with `onClick` opens the plugin's notification center route, where the action buttons live. An action only counts as "displayed" once Decky reports the toast as rendered.

## Consequences
- A compromised plugin can at worst answer DeckBeacon notifications and see status. HA-side validation of notification responses (spec 07) limits the impact.
- Accepted residual risk: anything running as root (Decky Loader, root plugins) can read agent key files. Documented in the threat model.

## Validation
HV-D1, HV-D4. M4 tests: socket permission tests and a forged-request test.
