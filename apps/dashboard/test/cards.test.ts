import { beforeEach, describe, expect, it } from "vitest";
import "../src/index";
import { resetCaches } from "../src/api";
import { DeckBeaconDashboardStrategy } from "../src/strategy";
import { $, $$, device, hass, library, mount, settle, text } from "./fixtures";

beforeEach(() => {
  resetCaches();
  document.body.innerHTML = "";
});

describe("registration and editor", () => {
  it("registers five cards in the card picker and the strategy", () => {
    const cards = (window as unknown as { customCards: { type: string }[] }).customCards.map((c) => c.type);
    expect(cards).toEqual(["deckbeacon-device-card", "deckbeacon-now-playing-card", "deckbeacon-downloads-card", "deckbeacon-library-card", "deckbeacon-performance-card"]);
    expect(customElements.get("ll-strategy-dashboard-deckbeacon")).toBeDefined();
  });

  it("offers a visual editor filtered to DeckBeacon devices and validates config", () => {
    const Card = customElements.get("deckbeacon-library-card") as unknown as { getConfigForm(): { schema: { name: string; selector: Record<string, unknown> }[] } };
    const schema = Card.getConfigForm().schema;
    expect(schema[0]).toEqual({ name: "device_id", selector: { device: { integration: "deckbeacon" } } });
    expect(schema.map((s) => s.name)).toEqual(["device_id", "title", "sort", "show_shortcuts"]);
    const el = document.createElement("deckbeacon-device-card") as unknown as { setConfig(c: unknown): void };
    expect(() => el.setConfig({ type: "x", device_id: 5 })).toThrow();
    expect(() => el.setConfig(null)).toThrow();
  });
});

describe("device card", () => {
  it("shows connection, power and session; explains missing values", async () => {
    const h = hass({ session_type: "gaming", steam_ui_mode: "gamepad", thermal_cpu_c: "61", thermal_gpu_c: "unavailable" }, { devices: [device({ reasons: { thermal_gpu_c: "no GPU sensor" } })] });
    const el = await mount("deckbeacon-device-card", {}, h);
    const t = text(el);
    expect(t).toContain("livingroom_steamos");
    expect(t).toContain("Local");
    expect(t).toContain("Gaming Mode");
    expect(t).toContain("Gamepad UI");
    expect(t).toContain("No GPU sensor"); // reason, not a bare "unavailable"
  });

  it("offline device: chip, last seen, and per-value explanation", async () => {
    const h = hass({ last_seen: new Date(Date.now() - 5 * 60_000).toISOString() }, { devices: [device({ connected: false, transport: null, power_state: "suspended" })] });
    const el = await mount("deckbeacon-device-card", {}, h);
    const t = text(el);
    expect(t).toContain("Offline");
    expect(t).toContain("Asleep");
    expect(t).toContain("5 min ago");
    expect(t).toContain("Device offline");
  });

  it("explains unsupported and disabled capabilities", async () => {
    const h = hass({}, { devices: [device({ capabilities: [{ id: "steam.ui_mode", status: "unsupported" }, { id: "session.type", status: "disabled" }] })] });
    const t = text(await mount("deckbeacon-device-card", {}, h));
    expect(t).toContain("Not supported on this device");
    expect(t).toContain("Turned off on the device");
  });

  it("no integration / no devices", async () => {
    const h = hass({}, { devices: [] });
    expect(text(await mount("deckbeacon-device-card", {}, h))).toContain("No DeckBeacon devices");
  });
});

describe("now playing card", () => {
  it("shows the game, artwork from the signed URL, session and activity", async () => {
    const h = hass({
      game_current_app_id: "1091500",
      game_current_name: "Cyberpunk 2077",
      game_session_duration_s: "3900",
      game_activity_details: "Night City — Watson",
      game_activity_state: "Act 1",
      game_activity_party_size: "2",
      game_activity_party_max: "4",
    });
    const el = await mount("deckbeacon-now-playing-card", {}, h);
    await settle(el);
    const t = text(el);
    expect(t).toContain("Cyberpunk 2077");
    expect(t).toContain("Playing for 1 h 5 min");
    expect(t).toContain("Night City — Watson");
    expect(t).toContain("party 2 of 4");
    const img = $<HTMLImageElement>(el, "img")!;
    expect(img.getAttribute("src")).toContain("/api/deckbeacon/artwork/entry1/1091500/capsule?authSig=");
    expect(img.getAttribute("alt")).toBe("Cyberpunk 2077 artwork");
    expect(h.calls.filter((c) => c.type === "deckbeacon/artwork_url")).toHaveLength(1);
  });

  it("close asks for confirmation; force close is offered only after a failed close", async () => {
    const h = hass({ game_current_app_id: "413150", game_current_name: "Stardew Valley" }, { failLaunch: "the game did not close within 20s; use force close" });
    const el = await mount("deckbeacon-now-playing-card", {}, h);
    const button = (label: string) => $$<HTMLButtonElement>(el, ".actions button").find((b) => b.textContent!.trim().startsWith(label))!;
    button("Close game").click();
    await settle(el);
    expect(text(el)).toContain("Close Stardew Valley? Unsaved progress may be lost.");
    expect(h.services).toHaveLength(0);
    button("Close game").click();
    await settle(el);
    expect(h.services[0]).toEqual({ domain: "deckbeacon", service: "close_game", data: { device_id: "dev-g15", force: false } });
    expect(text(el)).toContain("use force close");
    button("Force close").click();
    await settle(el);
    expect(text(el)).toContain("Unsaved progress will be lost");
    button("Force close").click();
    await settle(el);
    expect(h.services[1].data).toEqual({ device_id: "dev-g15", force: true });
  });

  it("no close button when launching/closing is not permitted", async () => {
    const el = await mount("deckbeacon-now-playing-card", {}, hass({ game_current_app_id: "413150" }, { devices: [device({ can_launch: false })] }));
    expect($(el, ".actions")).toBeNull();
  });

  it("not playing", async () => {
    const el = await mount("deckbeacon-now-playing-card", {}, hass({ game_current_app_id: "unavailable" }));
    expect(text(el)).toContain("Not playing");
    expect($(el, "img")).toBeNull();
  });
});

describe("downloads card", () => {
  it("progress bar, speed and time remaining in the user's unit", async () => {
    const h = hass({
      downloads_status: "downloading",
      downloads_name: "Grand Theft Auto V Legacy",
      downloads_progress: "95.0",
      downloads_rate_bps: ["39600000", { unit_of_measurement: "B/s" }],
      downloads_eta_s: ["3", { unit_of_measurement: "min" }],
      library_updates: "1",
    });
    const el = await mount("deckbeacon-downloads-card", {}, h);
    const t = text(el);
    expect(t).toContain("Grand Theft Auto V Legacy");
    expect(t).toContain("39.6 MB/s");
    expect(t).toContain("3 min");
    expect(t).toContain("1 update");
    expect($(el, '[role="progressbar"]')!.getAttribute("aria-valuenow")).toBe("95");
  });

  it("paused shows no speed but the reason", async () => {
    const h = hass({ downloads_status: "paused", downloads_progress: "40", downloads_rate_bps: "unavailable", downloads_eta_s: "unavailable" }, { devices: [device({ reasons: { downloads_rate_bps: "paused", downloads_eta_s: "paused" } })] });
    const t = text(await mount("deckbeacon-downloads-card", {}, h));
    expect(t).toContain("Paused");
    expect(t).not.toContain("MB/s");
  });

  it("idle", async () => {
    expect(text(await mount("deckbeacon-downloads-card", {}, hass({ downloads_status: "idle" })))).toContain("No downloads in progress");
  });
});

describe("library card", () => {
  it("lists games with lazy artwork; search and filter", async () => {
    const el = await mount("deckbeacon-library-card", {}, hass());
    await settle(el);
    expect($$(el, ".tile .name").map((n) => n.textContent)).toEqual(["Castle Crashers", "Hollow Acre", "Starfield"]);
    const img = $<HTMLImageElement>(el, ".tile img")!;
    expect(img.getAttribute("loading")).toBe("lazy");
    expect(img.getAttribute("src")).toContain("authSig=");

    const q = $<HTMLInputElement>(el, "#q")!;
    q.value = "hollow";
    q.dispatchEvent(new Event("input"));
    await settle(el);
    expect($$(el, ".tile .name").map((n) => n.textContent)).toEqual(["Hollow Acre"]);

    q.value = "";
    q.dispatchEvent(new Event("input"));
    const f = $<HTMLSelectElement>(el, "#f")!;
    f.value = "game";
    f.dispatchEvent(new Event("change"));
    await settle(el);
    expect($$(el, ".tile .name").map((n) => n.textContent)).toEqual(["Castle Crashers", "Starfield"]);
  });

  it("launch asks for confirmation, then calls the action", async () => {
    const h = hass();
    const el = await mount("deckbeacon-library-card", {}, h);
    await settle(el);
    const btn = $$<HTMLButtonElement>(el, ".tile button")[0];
    expect(btn.getAttribute("aria-label")).toBe("Launch Castle Crashers");
    btn.click();
    await settle(el);
    expect(h.services).toHaveLength(0); // nothing until confirmed
    $<HTMLButtonElement>(el, '.confirm button[aria-label^="Cancel"]')!.click();
    await settle(el);
    expect($(el, ".confirm")).toBeNull();
    $$<HTMLButtonElement>(el, ".tile button")[0].click();
    await settle(el);
    $<HTMLButtonElement>(el, ".confirm button.primary")!.click();
    await settle(el);
    expect(h.services).toEqual([{ domain: "deckbeacon", service: "launch_game", data: { device_id: "dev-g15", app_id: 204360 } }]);
    expect(text(el)).toContain("Castle Crashers is starting");
  });

  it("not-ready games and denied tier disable launch with an explanation", async () => {
    const el = await mount("deckbeacon-library-card", {}, hass({}, { library: library({ can_launch: false }) }));
    await settle(el);
    expect($$<HTMLButtonElement>(el, ".tile button").every((b) => b.disabled)).toBe(true);
    expect(text(el)).toContain("Launching is not allowed on the current connection");
    resetCaches();
    const el2 = await mount("deckbeacon-library-card", {}, hass());
    await settle(el2);
    const star = $$<HTMLButtonElement>(el2, ".tile button")[2];
    expect(star.disabled).toBe(true);
    expect(star.title).toContain("downloading");
  });

  it("surfaces a refused launch", async () => {
    const h = hass({}, { failLaunch: "Game management is not allowed over the remote connection" });
    const el = await mount("deckbeacon-library-card", {}, h);
    await settle(el);
    $$<HTMLButtonElement>(el, ".tile button")[0].click();
    await settle(el);
    $<HTMLButtonElement>(el, ".confirm button.primary")!.click();
    await settle(el);
    expect(text(el)).toContain("not allowed over the remote connection");
  });
});

describe("performance card", () => {
  it("meters with aria values and explanations", async () => {
    const h = hass({ cpu_usage: "23.4", gpu_busy: "unavailable", memory_used_percent: "61" }, { devices: [device({ reasons: { gpu_busy: "no gpu_busy_percent on this GPU" } })] });
    const el = await mount("deckbeacon-performance-card", {}, h);
    const bars = $$(el, '[role="progressbar"]').map((b) => [b.getAttribute("aria-label"), b.getAttribute("aria-valuenow")]);
    expect(bars).toEqual([["CPU", "23"], ["Memory", "61"]]);
    expect(text(el)).toContain("No gpu_busy_percent on this GPU");
  });
});

describe("no polling", () => {
  it("re-describes the device only when a watched state changes", async () => {
    let h = hass({ connection: "local", cpu_usage: "10" });
    const el = (await mount("deckbeacon-device-card", {}, h)) as HTMLElement & { hass: unknown };
    const count = () => h.calls.filter((c) => c.type === "deckbeacon/devices").length;
    expect(count()).toBe(1);
    for (let i = 0; i < 5; i++) {
      h = Object.assign(h.set("cpu_usage", String(20 + i)), { calls: h.calls });
      el.hass = h;
      await settle(el);
    }
    expect(count()).toBe(1); // telemetry updates don't refetch
    el.hass = Object.assign(h.set("connection", "offline"), { calls: h.calls });
    await settle(el);
    expect(count()).toBe(2);
  });
});

describe("dashboard strategy", () => {
  it("one sections view per device", async () => {
    const h = hass({}, { devices: [device(), device({ device_id: "dev2", name: "Steam Deck" })] });
    const dash = await DeckBeaconDashboardStrategy.generate({}, h);
    expect(dash.views.map((v: { title: string; path?: string }) => [v.title, v.path])).toEqual([["livingroom_steamos", "livingroom-steamos"], ["Steam Deck", "steam-deck"]]);
    const cards = (dash.views[0] as { sections: { cards: { type: string; device_id: string }[] }[] }).sections.flatMap((s) => s.cards);
    expect(cards.map((c) => c.type)).toContain("custom:deckbeacon-library-card");
    expect(cards.every((c) => c.device_id === "dev-g15")).toBe(true);
  });
});
