// Minimal slices of Home Assistant's frontend types used by the cards.

export interface HassEntity {
  entity_id: string;
  state: string;
  attributes: Record<string, unknown>;
  last_changed: string;
  last_updated: string;
}

export interface HomeAssistant {
  states: Record<string, HassEntity>;
  language?: string;
  locale?: { language: string };
  themes?: { darkMode?: boolean };
  callWS<T>(msg: Record<string, unknown>): Promise<T>;
  callService(domain: string, service: string, data?: Record<string, unknown>): Promise<unknown>;
  formatEntityState?(entity: HassEntity, state?: string): string;
}

export interface Capability {
  id: string;
  status: "available" | "unsupported" | "disabled" | "unavailable" | "unknown";
  reason?: string;
  experimental?: boolean;
}

export interface DeviceInfo {
  device_id: string;
  entry_id: string;
  name: string;
  connected: boolean;
  transport: string | null;
  power_state: string;
  entities: Record<string, string>;
  reasons: Record<string, string>;
  capabilities: Capability[];
  can_launch: boolean;
  library_revision: number;
}

export interface Game {
  app_id: number;
  name: string;
  kind: "game" | "shortcut" | "tool";
  status: string;
  size_on_disk?: number;
  last_played?: string;
  artwork: { capsule: string; header: string };
}

export interface Library {
  revision: number;
  games: Game[];
  can_launch: boolean;
}

export interface CardConfig {
  type: string;
  device_id?: string;
  title?: string;
  [key: string]: unknown;
}
