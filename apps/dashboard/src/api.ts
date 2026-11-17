// Integration WebSocket API (deckbeacon/*). Cards never talk to the agent.
// Results are shared between cards and refreshed only when something they
// depend on changes: no polling (spec 08).

import type { DeviceInfo, HomeAssistant, Library } from "./types";

let devices: Promise<DeviceInfo[]> | undefined;
let devicesKey = "";
const libraries = new Map<string, { revision: number; promise: Promise<Library> }>();
const artwork = new Map<string, Promise<string | undefined>>();

/** All DeckBeacon devices. `key` changes whenever a refresh is warranted. */
export function getDevices(hass: HomeAssistant, key: string): Promise<DeviceInfo[]> {
  if (!devices || key !== devicesKey) {
    devicesKey = key;
    devices = hass
      .callWS<{ devices: DeviceInfo[] }>({ type: "deckbeacon/devices" })
      .then((r) => r.devices)
      .catch((err) => {
        devices = undefined;
        throw err;
      });
  }
  return devices;
}

export function getLibrary(hass: HomeAssistant, deviceId: string, revision: number): Promise<Library> {
  const hit = libraries.get(deviceId);
  if (hit && hit.revision === revision) return hit.promise;
  const promise = hass.callWS<Library>({ type: "deckbeacon/library", device_id: deviceId });
  promise.catch(() => libraries.delete(deviceId));
  libraries.set(deviceId, { revision, promise });
  return promise;
}

/** Signed artwork URL (usable in <img>), cached per app and kind. */
export function getArtworkUrl(hass: HomeAssistant, deviceId: string, appId: number, kind: string): Promise<string | undefined> {
  const key = `${deviceId}/${appId}/${kind}`;
  let p = artwork.get(key);
  if (!p) {
    p = hass
      .callWS<{ url: string }>({ type: "deckbeacon/artwork_url", device_id: deviceId, app_id: appId, kind })
      .then((r) => r.url)
      .catch(() => undefined);
    artwork.set(key, p);
  }
  return p;
}

/** Test hook. */
export function resetCaches(): void {
  devices = undefined;
  devicesKey = "";
  libraries.clear();
  artwork.clear();
}
