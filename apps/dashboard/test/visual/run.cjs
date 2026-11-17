// Real-browser check of the built bundle: renders every card with realistic
// data in light and dark HA-like themes, at phone and desktop widths, takes
// screenshots, and runs axe-core accessibility scans (AC4). Uses the
// preinstalled Playwright + Chromium.
//   node test/visual/run.cjs [outDir]
const { chromium } = require("playwright");
const fs = require("node:fs");
const path = require("node:path");

const root = path.resolve(__dirname, "../..");
const out = path.resolve(process.argv[2] || path.join(root, "test/visual/out"));
fs.mkdirSync(out, { recursive: true });
const bundle = fs.readFileSync(path.join(root, "dist/deckbeacon-cards.js"), "utf8");
const axe = fs.readFileSync(path.join(root, "node_modules/axe-core/axe.min.js"), "utf8");
const brand = path.resolve(root, "../../docs/brand/png");

const THEMES = {
  light: { "--primary-color": "#03a9f4", "--primary-text-color": "#212121", "--secondary-text-color": "#616161", "--card-background-color": "#ffffff", "--secondary-background-color": "#e5e5e5", "--divider-color": "rgba(0,0,0,.12)", "--error-color": "#b71c1c", "--success-color": "#2e7d32", "--warning-color": "#e65100", "--text-primary-color": "#ffffff", "--page": "#fafafa" },
  dark: { "--primary-color": "#24d6c5", "--primary-text-color": "#e1e1e1", "--secondary-text-color": "#a8a8a8", "--card-background-color": "#1c1c1c", "--secondary-background-color": "#2b2b2b", "--divider-color": "rgba(225,225,225,.12)", "--error-color": "#ff6b6b", "--success-color": "#66bb6a", "--warning-color": "#ffa726", "--text-primary-color": "#101b2c", "--page": "#111111" },
};

const page = (theme) => `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>DeckBeacon cards</title><style>
  :root{${Object.entries(THEMES[theme]).map(([k, v]) => `${k}:${v}`).join(";")}}
  body{margin:0;background:var(--page);font-family:Roboto,"Noto Sans",system-ui,sans-serif;color:var(--primary-text-color)}
  main{display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:12px;padding:12px;align-items:start}
  .wide{grid-column:1/-1}
</style></head><body><main id="m"><h1 class="wide" style="font-size:1.2em;margin:4px">DeckBeacon cards (${theme})</h1></main></body></html>`;

const now = Date.now();
const iso = (msAgo) => new Date(now - msAgo).toISOString();
const STATES = {
  connection: "local", power_state: "on", last_seen: iso(0),
  game_current_app_id: "1091500", game_current_name: "Cyberpunk 2077", game_session_started_at: iso(65 * 60e3), game_session_duration_s: "3900",
  game_activity_details: "Night City — Watson", game_activity_state: "Act 1: The Heist", game_activity_party_size: "2", game_activity_party_max: "4",
  steam_ui_mode: "gamepad", session_type: "gaming", cpu_usage: "23.4", gpu_busy: "61", memory_used_percent: "48", thermal_cpu_c: ["64", "°C"], thermal_gpu_c: ["58", "°C"], fan_rpm: ["2234", "RPM"], storage_home_free_bytes: ["730.9", "GB"],
  downloads_status: "downloading", downloads_name: "Grand Theft Auto V Legacy", downloads_progress: "95", downloads_rate_bps: ["39600000", "B/s"], downloads_eta_s: ["3", "min"], library_updates: "1", library_games: "32", library_shortcuts: "8",
};
const GAMES = [
  ["Among Us", 945360, "game", "installed", 2], ["Castle Crashers", 204360, "game", "installed", 3], ["Cyberpunk 2077", 1091500, "game", "installed", 1],
  ["Discord", 2961728726, "shortcut", "installed", 50], ["Hollow Acre", 4244106195, "shortcut", "installed", 4], ["Satisfactory", 526870, "game", "installed", 9],
  ["Stardew Valley", 413150, "game", "installed", 20], ["Starfield", 1716740, "game", "downloading", 0], ["The Tux InVader", 2994840599, "shortcut", "installed", 0],
];

async function render(browser, theme, width) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, deviceScaleFactor: 2 });
  const p = await ctx.newPage();
  const errors = [];
  p.on("pageerror", (e) => errors.push(e.message));
  p.on("console", (m) => m.type() === "error" && !m.text().includes("status of 404") && errors.push(m.text()));
  // Artwork through the authenticated proxy path: brand art stands in for Steam
  // art (not redistributable); ids without art return 404 -> placeholder.
  await p.route("**/api/deckbeacon/artwork/**", (route) => {
    const id = Number(route.request().url().split("/").at(-2));
    if (id === 4244106195 || id === 2994840599) return route.fulfill({ status: 404 });
    return route.fulfill({ status: 200, contentType: "image/png", body: fs.readFileSync(path.join(brand, id % 2 ? "deckbeacon-icon-dark-ui-1024.png" : "deckbeacon-avatar-512.png")) });
  });
  await p.route("http://ha.test/", (r) => r.fulfill({ contentType: "text/html", body: page(theme) }));
  await p.goto("http://ha.test/");
  await p.addScriptTag({ content: `customElements.define("ha-card", class extends HTMLElement { connectedCallback(){ this.style.cssText="display:block;background:var(--card-background-color);border-radius:12px;box-shadow:0 1px 3px rgba(0,0,0,.2);color:var(--primary-text-color)"; } });` });
  await p.addScriptTag({ content: bundle, type: "module" });
  await p.addScriptTag({ content: axe });
  await p.evaluate(async ({ STATES, GAMES }) => {
    const ent = (k) => `sensor.g15_${k}`;
    const states = {};
    for (const [k, v] of Object.entries(STATES)) {
      const [state, unit] = Array.isArray(v) ? v : [v, undefined];
      states[ent(k)] = { entity_id: ent(k), state, attributes: unit ? { unit_of_measurement: unit } : {}, last_changed: "", last_updated: "" };
    }
    const device = { device_id: "dev", entry_id: "e", name: "livingroom_steamos", connected: true, transport: "local", power_state: "on", entities: Object.fromEntries(Object.keys(STATES).map((k) => [k, ent(k)])), reasons: {}, capabilities: [], can_launch: true, library_revision: 1 };
    const lib = { revision: 1, can_launch: true, games: GAMES.map(([name, app_id, kind, status, days]) => ({ name, app_id, kind, status, last_played: days ? new Date(Date.now() - days * 864e5).toISOString() : undefined, artwork: { capsule: `/api/deckbeacon/artwork/e/${app_id}/capsule?authSig=x`, header: "" } })) };
    const hass = {
      states,
      formatEntityState: (e) => (e.attributes.unit_of_measurement ? `${e.state} ${e.attributes.unit_of_measurement}` : e.state),
      callWS: async (m) => (m.type === "deckbeacon/devices" ? { devices: [device] } : m.type === "deckbeacon/library" ? lib : { url: `/api/deckbeacon/artwork/e/${m.app_id}/${m.kind}?authSig=x` }),
      callService: async () => ({}),
    };
    for (const t of ["deckbeacon-device-card", "deckbeacon-now-playing-card", "deckbeacon-downloads-card", "deckbeacon-performance-card", "deckbeacon-library-card"]) await customElements.whenDefined(t);
    const m = document.getElementById("m");
    for (const t of ["now-playing", "device", "downloads", "performance", "library"]) {
      const el = document.createElement(`deckbeacon-${t}-card`);
      el.setConfig({ type: `custom:deckbeacon-${t}-card` });
      el.hass = hass;
      if (t === "library") el.className = "wide";
      m.appendChild(el);
    }
  }, { STATES, GAMES });
  await p.waitForTimeout(800);
  await p.screenshot({ path: path.join(out, `cards-${theme}-${width}.png`), fullPage: true });
  const violations = await p.evaluate(async () => {
    const r = await window.axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] } });
    return r.violations.map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.length, sample: v.nodes[0]?.target?.join(" ") }));
  });
  // Keyboard: Tab reaches the library's search, filters and launch buttons.
  const focusable = await p.evaluate(() => {
    const lib = document.querySelector("deckbeacon-library-card").shadowRoot;
    return [...lib.querySelectorAll("input,select,button")].filter((e) => !e.disabled && e.tabIndex >= 0).length;
  });
  await ctx.close();
  return { theme, width, errors, violations, focusable };
}

(async () => {
  const browser = await chromium.launch();
  const results = [];
  for (const theme of ["light", "dark"]) for (const width of [390, 1280]) results.push(await render(browser, theme, width));
  await browser.close();
  let failed = false;
  for (const r of results) {
    console.log(`${r.theme} ${r.width}px: ${r.errors.length} errors, ${r.violations.length} axe violations, ${r.focusable} focusable library controls`);
    for (const v of r.violations) console.log(`  axe ${v.impact} ${v.id} x${v.nodes} e.g. ${v.sample}`);
    for (const e of r.errors) console.log(`  error ${e}`);
    if (r.errors.length || r.violations.length || r.focusable < 4) failed = true;
  }
  console.log(`screenshots in ${out}`);
  process.exit(failed ? 1 : 0);
})();
