import { getDevices } from "./api";
import type { HomeAssistant } from "./types";

const slug = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "device";

/**
 * Preconfigured multi-device dashboard (spec 08), opt-in:
 *   strategy:
 *     type: custom:deckbeacon
 * One sections view per DeckBeacon device.
 */
export class DeckBeaconDashboardStrategy extends HTMLElement {
  static async generate(_config: Record<string, unknown>, hass: HomeAssistant) {
    const devices = await getDevices(hass, `strategy-${Date.now()}`);
    if (!devices.length) {
      return { title: "DeckBeacon", views: [{ title: "DeckBeacon", cards: [{ type: "markdown", content: "No DeckBeacon devices yet. Pair one from **Settings → Devices & services**." }] }] };
    }
    const used = new Set<string>();
    return {
      title: "DeckBeacon",
      views: devices.map((d) => {
        let path = slug(d.name);
        while (used.has(path)) path += "-2";
        used.add(path);
        const card = (type: string, extra: Record<string, unknown> = {}) => ({ type: `custom:deckbeacon-${type}-card`, device_id: d.device_id, ...extra });
        return {
          title: d.name,
          path,
          icon: "mdi:gamepad-variant",
          type: "sections",
          max_columns: 3,
          sections: [
            { type: "grid", cards: [card("now-playing"), card("device")] },
            { type: "grid", cards: [card("downloads"), card("performance")] },
            { type: "grid", column_span: 2, cards: [card("library")] },
          ],
        };
      }),
    };
  }
}
