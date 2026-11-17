import type { DeviceInfo, HassEntity, HomeAssistant, Library } from "../src/types";

export const DEVICE_ID = "dev-g15";

const ent = (key: string) => `sensor.g15_${key}`;

export function device(over: Partial<DeviceInfo> = {}): DeviceInfo {
  const keys = [
    "connection", "power_state", "last_seen", "game_current_name", "game_current_app_id", "game_session_started_at", "game_session_duration_s",
    "game_activity_details", "game_activity_state", "game_activity_party_size", "game_activity_party_max", "steam_ui_mode", "session_type",
    "cpu_usage", "gpu_busy", "memory_used_percent", "thermal_cpu_c", "thermal_gpu_c", "fan_rpm", "storage_home_free_bytes",
    "downloads_status", "downloads_name", "downloads_progress", "downloads_rate_bps", "downloads_eta_s", "library_updates", "library_games", "library_shortcuts",
  ];
  return {
    device_id: DEVICE_ID,
    entry_id: "entry1",
    name: "livingroom_steamos",
    connected: true,
    transport: "local",
    power_state: "on",
    entities: Object.fromEntries(keys.map((k) => [k, ent(k)])),
    reasons: {},
    capabilities: [],
    can_launch: true,
    library_revision: 3,
    ...over,
  };
}

export function library(over: Partial<Library> = {}): Library {
  const art = (id: number) => ({ capsule: `/api/deckbeacon/artwork/entry1/${id}/capsule?authSig=x`, header: `/api/deckbeacon/artwork/entry1/${id}/header?authSig=x` });
  return {
    revision: 3,
    can_launch: true,
    games: [
      { app_id: 204360, name: "Castle Crashers", kind: "game", status: "installed", last_played: "2026-11-17T01:47:20Z", artwork: art(204360) },
      { app_id: 4244106195, name: "Hollow Acre", kind: "shortcut", status: "installed", last_played: "2026-11-17T02:19:37Z", artwork: art(4244106195) },
      { app_id: 1716740, name: "Starfield", kind: "game", status: "downloading", artwork: art(1716740) },
    ],
    ...over,
  };
}

export interface FakeHass extends HomeAssistant {
  calls: Record<string, unknown>[];
  services: { domain: string; service: string; data?: Record<string, unknown> }[];
  set(key: string, state: string, attributes?: Record<string, unknown>): FakeHass;
}

export function hass(states: Record<string, string | [string, Record<string, unknown>]> = {}, opts: { devices?: DeviceInfo[]; library?: Library; failLaunch?: string } = {}): FakeHass {
  const devices = opts.devices ?? [device()];
  const lib = opts.library ?? library();
  const calls: Record<string, unknown>[] = [];
  const services: FakeHass["services"] = [];
  const st: Record<string, HassEntity> = {};
  const make = (key: string, v: string | [string, Record<string, unknown>]): HassEntity => {
    const [state, attributes] = Array.isArray(v) ? v : [v, {}];
    return { entity_id: ent(key), state, attributes, last_changed: "2026-11-17T03:00:00Z", last_updated: `2026-11-17T03:00:${String(Object.keys(st).length).padStart(2, "0")}Z` };
  };
  for (const [k, v] of Object.entries(states)) st[ent(k)] = make(k, v);
  const h: FakeHass = {
    states: st,
    calls,
    services,
    async callWS<T>(msg: Record<string, unknown>): Promise<T> {
      calls.push(msg);
      if (msg.type === "deckbeacon/devices") return { devices } as T;
      if (msg.type === "deckbeacon/library") return lib as T;
      if (msg.type === "deckbeacon/artwork_url") return { url: `/api/deckbeacon/artwork/entry1/${msg.app_id}/${msg.kind}?authSig=y` } as T;
      throw new Error("unknown");
    },
    async callService(domain, service, data) {
      services.push({ domain, service, data });
      if (opts.failLaunch) throw new Error(opts.failLaunch);
      return {};
    },
    set(key, state, attributes = {}) {
      const next = { ...st, [ent(key)]: { ...make(key, [state, attributes]), last_updated: new Date().toISOString() } };
      return { ...h, states: next };
    },
  };
  return h;
}

export async function mount<T extends HTMLElement>(tag: string, config: Record<string, unknown>, h: HomeAssistant): Promise<T> {
  const el = document.createElement(tag) as T & { setConfig(c: unknown): void; hass: HomeAssistant; updateComplete: Promise<unknown> };
  el.setConfig({ type: `custom:${tag}`, ...config });
  el.hass = h;
  document.body.appendChild(el);
  await settle(el);
  return el;
}

export async function settle(el: HTMLElement): Promise<void> {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await (el as unknown as { updateComplete: Promise<unknown> }).updateComplete;
  }
}

export const text = (el: HTMLElement) => (el.shadowRoot?.textContent ?? "").replace(/\s+/g, " ").trim();
export const $ = <T extends Element = HTMLElement>(el: HTMLElement, sel: string) => el.shadowRoot!.querySelector(sel) as T | null;
export const $$ = <T extends Element = HTMLElement>(el: HTMLElement, sel: string) => Array.from(el.shadowRoot!.querySelectorAll(sel)) as T[];
