# DeckBeacon brand assets

Generated for the owner (M8, 2026-11-17). Palette, font and usage rules: [BRAND_GUIDE.md](BRAND_GUIDE.md).

| Folder | Contents | Used by |
|---|---|---|
| `masters/` | Vector icon (color, monochrome, light/dark UI) and horizontal logos (light, dark). The logos use live text in the Manrope font: convert to paths before print or production use. | Source for everything else |
| `extras/` | README banner, social previews (main repo, cards repo), avatar, "no artwork" placeholders | GitHub, the dashboard cards |
| `png/` | 1024 px icons, rendered logos, banner, social previews (1280×640), avatar, favicon, placeholders | README, GitHub settings, docs |

Copies that ship elsewhere:
- `apps/homeassistant/custom_components/deckbeacon/brand/`: the eight HA brand files (`icon`, `logo`, `@2x`, `dark_`). HA's brands component serves a custom integration's own `brand/` folder (2026.5+), so the icon shows in Settings → Devices & services without a brands-repo submission. Covered by `tests/test_artwork.py::test_integration_brand_icon_served_locally`.
- `apps/dashboard/src/assets/`: the portrait and wide placeholders, inlined into `deckbeacon-cards.js` for games without artwork.
- Decky (M4): the monochrome mark becomes the sidebar icon, and a social-preview-sized image the store listing. Exact Decky store dimensions are checked at M4.

Rules: no Steam, Valve, Steam Deck or Home Assistant marks. Keep the aspect ratio. Check legibility at 16–24 px.
