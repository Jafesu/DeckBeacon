# ADR-0009: Game artwork transport and dashboard card distribution

Status: Accepted (M8, 2026-11-17; validated on hardware, cards distributed via the Marketplace mirror)

## Context
Spec 08 asks for game artwork in the dashboard through an **authenticated proxy, with no exposed agent file URLs**. The agent never listens on a port (ADR-0001: it dials HA), so HA can't fetch images from the device. Artwork must travel over the existing Noise session, whose messages carry at most 65,519 bytes of plaintext. Steam's artwork cache (`appcache/librarycache/<appid>/`) has two layouts (G15 §6). Images can be large: heroes are 1920 px wide.

## Decision
1. **Pull on demand, chunked.** HA sends the command `get_artwork {app_id, kind}`, with `kind` one of `capsule` (portrait), `header`, `hero` or `logo`. The agent answers with `artwork` messages: `{command_id, app_id, kind, mime, width, height, seq, total, sha256, data}`, where `data` is base64 and each chunk holds at most 32 KiB of binary. Then a `command_result` follows (`confirmed {bytes, sha256}`, or `rejected/unavailable` when no artwork of that kind exists). HA reassembles the chunks and checks the sha256. One in-flight request per (device, app, kind).
2. **The agent re-encodes; it never forwards file bytes.** Only regular files with allowlisted names are read, under `librarycache/<appid>/` (one level of hashed subdirectory) and, for shortcuts, `userdata/<id>/config/grid/` (`<u32>p`, `<u32>`, `<u32>_hero`, `<u32>_logo`; confirmed by the G15 artwork survey). Symlinks and `..` are refused. Inputs are capped at 32 MiB (raised from 8 MiB after the G15 survey found a 16.2 MB logo); the pixel-count check before decoding is the real guard. Among candidate files the largest image wins, and the format is sniffed from the bytes. Each image is decoded (JPEG/PNG), downscaled (capsule ≤ 600×900, header ≤ 920×430, hero ≤ 1280 wide, logo ≤ 640 wide) and re-encoded: JPEG q82, or PNG for logos with alpha. Output is capped at 512 KiB. Re-encoding drops all metadata and means HA only ever gets freshly encoded pixels.
3. **HA caches and proxies.** HA keeps a disk cache at `<config>/.cache/deckbeacon/<device>/` (bounded at 100 MiB, LRU), so artwork still shows while the device sleeps. It refreshes when the library entry's revision changes. HA serves the images from an authenticated `HomeAssistantView`. Cards get **signed paths** (`async_sign_path`, 24 h) through the integration's WebSocket API, because `<img>` can't send a bearer token. The current game's artwork is also an `image` entity, so native dashboards show it with no custom card.
4. **Cards are a separate, optional artifact.** `apps/dashboard` builds one `deckbeacon-cards.js` (Lit, TypeScript), versioned independently and distributed like a HACS frontend plugin. The integration doesn't bundle or auto-register it. Without it, every feature remains available through native entities. During development the owner's HA gets the file in `/config/www/deckbeacon/` plus a Lovelace resource, each with explicit per-action approval.

## Consequences
- Protocol 1.4: one command, one message type, fixtures in both languages. Older agents answer `unsupported`, and the cards fall back to placeholders.
- The first view of a large library fetches artwork lazily, only for cards actually on screen (IntersectionObserver). After that it is served from cache.
- Cost on the device: one decode and encode per (app, kind, mtime), done once and cached in memory (bounded).

## Alternatives rejected
- **Agent HTTP server:** violates ADR-0001 (no inbound ports).
- **Forwarding raw file bytes:** a larger attack surface (arbitrary file content into HA), plus metadata and size issues.
- **Steam CDN URLs:** these need the Steam app id to CDN mapping and make HA, or the browser, contact Valve. That conflicts with local-first and could leak library contents to a third party.
- **Bundling cards into the integration:** spec 08 requires independent versioning and an integration that works without them.
