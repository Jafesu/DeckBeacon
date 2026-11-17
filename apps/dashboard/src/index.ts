import { DeviceCard } from "./cards/device-card";
import { DownloadsCard } from "./cards/downloads-card";
import { LibraryCard } from "./cards/library-card";
import { NowPlayingCard } from "./cards/now-playing-card";
import { PerformanceCard } from "./cards/performance-card";
import { DeckBeaconDashboardStrategy } from "./strategy";

declare const __VERSION__: string;

const CARDS: [string, CustomElementConstructor, string, string][] = [
  ["deckbeacon-device-card", DeviceCard, "DeckBeacon device", "Connection, power, session and temperatures of a SteamOS device."],
  ["deckbeacon-now-playing-card", NowPlayingCard, "DeckBeacon now playing", "Current game with Steam artwork, session time and Rich Presence activity."],
  ["deckbeacon-downloads-card", DownloadsCard, "DeckBeacon downloads", "Steam download progress, speed and time remaining."],
  ["deckbeacon-library-card", LibraryCard, "DeckBeacon game library", "Installed games with artwork; search and launch with confirmation."],
  ["deckbeacon-performance-card", PerformanceCard, "DeckBeacon performance", "CPU, GPU and memory usage with trends, temperatures and fan."],
];

const w = window as unknown as { customCards?: unknown[] };
w.customCards = w.customCards || [];
for (const [tag, ctor, name, description] of CARDS) {
  if (!customElements.get(tag)) {
    customElements.define(tag, ctor);
    w.customCards.push({ type: tag, name, description, preview: false, documentationURL: "https://github.com/Jafesu/DeckBeacon" });
  }
}
if (!customElements.get("ll-strategy-dashboard-deckbeacon")) customElements.define("ll-strategy-dashboard-deckbeacon", DeckBeaconDashboardStrategy);

console.info(`%c DECKBEACON-CARDS %c ${__VERSION__} `, "color:#101B2C;background:#24D6C5;font-weight:700", "color:#24D6C5;background:#101B2C");
