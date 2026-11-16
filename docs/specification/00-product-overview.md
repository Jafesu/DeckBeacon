# Product overview
DeckBeacon is a SteamOS-only, local-first monitoring and management companion for Home Assistant. It supports Steam Deck and other genuine SteamOS devices. No mandatory third-party relay or cloud account. Required: standalone Go agent and Python HA custom integration. Optional: Decky Loader plugin and Lovelace frontend. All sources in one monorepo. Implementation by Claude Code after planning approval.

## Scope
System telemetry, Steam activity, installed library, downloads and updates, launch/exit, sessions, artwork, Proton/compatibility, game storage, power/brightness/volume, managed updates, local/remote connectivity, bidirectional notifications and automations, diagnostics and privacy.

## Principles
Native HA functionality must not depend on Decky or custom cards. Unsupported operations must be visible as unsupported, not simulated. Security and game-session safety take precedence over convenience. No direct modification of Steam internal state to perform management operations. User approval for agent updates. Multi-device identity must be stable.
