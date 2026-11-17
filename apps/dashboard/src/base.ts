import { LitElement, html, nothing, type PropertyValues, type TemplateResult } from "lit";
import { getDevices } from "./api";
import { GLYPH } from "./assets";
import { relative } from "./format";
import type { CardConfig, DeviceInfo, HassEntity, HomeAssistant } from "./types";
import { unsafeSVG } from "lit/directives/unsafe-svg.js";

/** Entity keys: unique_id suffixes the integration uses ("<device>_<key>"). */
export const K = {
  connected: "connected",
  connection: "connection",
  power: "power_state",
  lastSeen: "last_seen",
  game: "game_current_name",
  appId: "game_current_app_id",
  sessionStarted: "game_session_started_at",
  sessionDuration: "game_session_duration_s",
  activityDetails: "game_activity_details",
  activityState: "game_activity_state",
  partySize: "game_activity_party_size",
  partyMax: "game_activity_party_max",
  uiMode: "steam_ui_mode",
  sessionType: "session_type",
  battery: "battery_level",
  batteryStatus: "battery_status",
  cpu: "cpu_usage",
  gpu: "gpu_busy",
  memory: "memory_used_percent",
  cpuTemp: "thermal_cpu_c",
  gpuTemp: "thermal_gpu_c",
  fan: "fan_rpm",
  storageFree: "storage_home_free_bytes",
  dlStatus: "downloads_status",
  dlName: "downloads_name",
  dlProgress: "downloads_progress",
  dlRate: "downloads_rate_bps",
  dlEta: "downloads_eta_s",
  updates: "library_updates",
  games: "library_games",
  shortcuts: "library_shortcuts",
  picker: "game_to_launch",
  launch: "launch_game",
} as const;

export interface FormField {
  name: string;
  selector: Record<string, unknown>;
}

/** Capability that governs an entity key (by key prefix). */
const CAPABILITY_PREFIX: [string, string][] = [
  ["game_activity_", "game.activity"],
  ["downloads_", "downloads.status"],
  ["steam_ui_mode", "steam.ui_mode"],
  ["session_type", "session.type"],
  ["game_", "game.current"],
  ["library_", "game.library"],
];

/** Base class: device resolution, entity lookup, state explanations, editor. */
export abstract class DeckBeaconCard extends LitElement {
  static override properties = {
    hass: { attribute: false },
    _config: { state: true },
    _device: { state: true },
    _error: { state: true },
  };

  hass?: HomeAssistant;
  protected _config?: CardConfig;
  protected _device?: DeviceInfo;
  protected _error?: string;
  private _deviceKey = "";

  setConfig(config: CardConfig): void {
    if (!config || typeof config !== "object") throw new Error("Invalid configuration");
    if (config.device_id !== undefined && typeof config.device_id !== "string") throw new Error("device_id must be a string");
    this._config = config;
    this._deviceKey = "";
  }

  /** The visual editor (HA renders it from this schema). */
  static getConfigForm(): { schema: FormField[]; computeLabel: (s: { name: string }) => string } {
    return {
      schema: [
        { name: "device_id", selector: { device: { integration: "deckbeacon" } } },
        { name: "title", selector: { text: {} } },
      ],
      computeLabel: (s: { name: string }) => ({ device_id: "DeckBeacon device", title: "Title (optional)" })[s.name] ?? s.name,
    };
  }

  getCardSize(): number {
    return 3;
  }

  protected override willUpdate(changed: PropertyValues): void {
    if (!changed.has("hass") || !this.hass) return;
    // Re-describe the device only when something the description depends on
    // changed (connection, library revision, game, download): no polling.
    const key = this._refreshKey();
    if (key !== this._deviceKey) {
      this._deviceKey = key;
      getDevices(this.hass, key)
        .then((devices) => {
          const want = this._config?.device_id;
          this._device = want ? devices.find((d) => d.device_id === want) : devices[0];
          this._error = this._device ? undefined : devices.length ? "Device not found" : "No DeckBeacon devices";
          // The key now includes the device's entities: re-baseline it so the
          // next ordinary state update doesn't trigger a redundant refetch.
          this._deviceKey = this._refreshKey();
        })
        .catch(() => {
          this._error = "DeckBeacon integration not loaded";
        });
    }
  }

  private _refreshKey(): string {
    const ids = Object.values(this._device?.entities ?? {});
    const watch = [K.connection, K.power, K.appId, K.dlStatus, K.games, K.shortcuts, K.uiMode, K.sessionType].map((k) => this._device?.entities[k]);
    return [this._config?.device_id ?? "", ids.length, ...watch.map((id) => (id ? this.hass?.states[id]?.state : ""))].join("|");
  }

  protected entity(key: string): HassEntity | undefined {
    const id = this._device?.entities[key];
    return id ? this.hass?.states[id] : undefined;
  }

  protected has(key: string): boolean {
    const s = this.entity(key)?.state;
    return s !== undefined && s !== "unavailable" && s !== "unknown";
  }

  protected value(key: string): string | undefined {
    return this.has(key) ? this.entity(key)!.state : undefined;
  }

  protected num(key: string): number | undefined {
    const v = Number(this.value(key));
    return this.has(key) && Number.isFinite(v) ? v : undefined;
  }

  /** Formatted with HA's own formatter (units, precision, translations). */
  protected display(key: string): string {
    const e = this.entity(key);
    if (!e || !this.has(key)) return this.explain(key);
    return this.hass?.formatEntityState ? this.hass.formatEntityState(e) : e.state;
  }

  /** Spec 08: unavailable vs unsupported vs disabled vs unknown, in words. */
  protected explain(key: string): string {
    const d = this._device;
    if (!d) return "";
    if (!d.connected) return "Device offline";
    const capId = CAPABILITY_PREFIX.find(([p]) => key.startsWith(p))?.[1];
    const cap = capId ? d.capabilities.find((c) => c.id === capId) : undefined;
    if (cap && cap.status === "unsupported") return "Not supported on this device";
    if (cap && cap.status === "disabled") return "Turned off on the device";
    if (!d.entities[key]) return "Not reported by this device";
    const reason = d.reasons[key];
    return reason ? reason.charAt(0).toUpperCase() + reason.slice(1) : "No data";
  }

  protected cardTitle(fallback: string): string {
    return this._config?.title || fallback;
  }

  protected header(fallback: string, extra?: TemplateResult): TemplateResult {
    return html`<div class="header">
      <span style="width:22px;height:22px;color:var(--primary-color);flex:none" aria-hidden="true">${unsafeSVG(GLYPH)}</span>
      <span class="grow">${this.cardTitle(fallback)}</span>
      ${extra ?? nothing}
    </div>`;
  }

  protected connectionChip(): TemplateResult {
    const d = this._device;
    if (!d) return html``;
    if (!d.connected) {
      const seen = this.entity(K.lastSeen)?.state;
      return html`<span class="chip off" title=${seen ? `Last seen ${relative(seen)}` : ""}>Offline</span>`;
    }
    return html`<span class="chip ok">${d.transport === "remote" ? "Remote" : "Local"}</span>`;
  }

  protected renderState(): TemplateResult | undefined {
    if (this._error) return html`<ha-card><div class="content note" role="status">${this._error}</div></ha-card>`;
    if (!this._device || !this.hass) return html`<ha-card><div class="content note" role="status">Loading…</div></ha-card>`;
    return undefined;
  }
}
