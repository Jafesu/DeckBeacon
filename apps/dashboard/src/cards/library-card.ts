import { css, html, nothing } from "lit";
import { getLibrary } from "../api";
import { PLACEHOLDER_PORTRAIT, svgDataUrl } from "../assets";
import { DeckBeaconCard } from "../base";
import { relative } from "../format";
import { cardStyles } from "../styles";
import type { Game, Library } from "../types";

type Filter = "all" | "game" | "shortcut";
type Sort = "name" | "recent";

/** Game library with Steam artwork, search, filters, and confirmed launch. */
export class LibraryCard extends DeckBeaconCard {
  static override properties = {
    ...DeckBeaconCard.properties,
    _lib: { state: true },
    _query: { state: true },
    _filter: { state: true },
    _sort: { state: true },
    _confirm: { state: true },
    _status: { state: true },
  };
  static override styles = [
    cardStyles,
    css`
      .controls {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
        margin-bottom: 12px;
      }
      input,
      select {
        font: inherit;
        color: var(--primary-text-color);
        background: var(--secondary-background-color, transparent);
        border: 1px solid var(--divider-color, rgba(127, 127, 127, 0.4));
        border-radius: 8px;
        padding: 6px 10px;
        min-height: 36px;
        box-sizing: border-box;
      }
      input {
        flex: 1 1 160px;
        min-width: 0;
      }
      .grid {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(112px, 1fr));
        gap: 12px;
        max-height: var(--deckbeacon-library-height, 560px);
        overflow-y: auto;
        padding: 2px;
      }
      .tile {
        display: flex;
        flex-direction: column;
        gap: 6px;
        min-width: 0;
      }
      .tile img {
        width: 100%;
        aspect-ratio: 2 / 3;
        object-fit: cover;
        border-radius: 8px;
        background: var(--secondary-background-color);
        display: block;
      }
      .name {
        font-size: 0.85em;
        line-height: 1.25;
        color: var(--primary-text-color);
        overflow: hidden;
        display: -webkit-box;
        -webkit-line-clamp: 2;
        -webkit-box-orient: vertical;
      }
      [role="listitem"] {
        display: flex;
      }
      .tile {
        flex: 1;
      }
      .tile > button,
      .tile > .confirm {
        margin-top: auto;
      }
      .tile button {
        padding: 4px 8px;
        min-height: 32px;
        font-size: 0.85em;
      }
      .confirm {
        display: flex;
        gap: 4px;
      }
      .confirm button {
        flex: 1;
      }
      .status {
        font-size: 0.875em;
        min-height: 1.2em;
        margin-bottom: 8px;
      }
      .status.error {
        color: var(--error-color, #db4437);
      }
    `,
  ];

  protected _lib?: Library;
  protected _query = "";
  protected _filter: Filter = "all";
  protected _sort: Sort = "name";
  protected _confirm?: number;
  protected _status?: { kind: "ok" | "error"; text: string };
  private _libKey = "";

  static override getConfigForm() {
    const base = DeckBeaconCard.getConfigForm();
    return {
      ...base,
      schema: [
        ...base.schema,
        { name: "sort", selector: { select: { mode: "dropdown", options: [{ value: "name", label: "Name" }, { value: "recent", label: "Recently played" }] } } },
        { name: "show_shortcuts", selector: { boolean: {} } },
      ],
      computeLabel: (s: { name: string }) => ({ sort: "Default sort", show_shortcuts: "Show non-Steam shortcuts" })[s.name] ?? base.computeLabel(s),
    };
  }

  override getCardSize(): number {
    return 8;
  }

  override willUpdate(changed: Map<string, unknown>): void {
    super.willUpdate(changed as never);
    if (changed.has("_config") && this._config?.sort === "recent") this._sort = "recent";
    const d = this._device;
    if (!d || !this.hass) return;
    const key = `${d.device_id}@${d.library_revision}`;
    if (key !== this._libKey) {
      this._libKey = key;
      getLibrary(this.hass, d.device_id, d.library_revision)
        .then((lib) => (this._lib = lib))
        .catch(() => (this._status = { kind: "error", text: "Could not load the library" }));
    }
  }

  private _games(): Game[] {
    const q = this._query.trim().toLowerCase();
    const showShortcuts = this._config?.show_shortcuts !== false;
    const out = (this._lib?.games ?? []).filter(
      (g) => (this._filter === "all" || g.kind === this._filter) && (showShortcuts || g.kind !== "shortcut") && (!q || g.name.toLowerCase().includes(q)),
    );
    if (this._sort === "recent") out.sort((a, b) => (b.last_played ?? "").localeCompare(a.last_played ?? "") || a.name.localeCompare(b.name));
    return out;
  }

  private async _launch(game: Game): Promise<void> {
    this._confirm = undefined;
    this._status = { kind: "ok", text: `Starting ${game.name}…` };
    try {
      await this.hass!.callService("deckbeacon", "launch_game", { device_id: this._device!.device_id, app_id: game.app_id });
      this._status = { kind: "ok", text: `${game.name} is starting on ${this._device!.name}` };
    } catch (err) {
      this._status = { kind: "error", text: (err as Error)?.message || `Could not start ${game.name}` };
    }
  }

  private _tile(g: Game, canLaunch: boolean) {
    const confirming = this._confirm === g.app_id;
    const ready = g.status === "installed";
    return html`<div class="tile">
      <img
        loading="lazy"
        decoding="async"
        src=${g.artwork.capsule}
        alt=""
        @error=${(e: Event) => ((e.target as HTMLImageElement).src = svgDataUrl(PLACEHOLDER_PORTRAIT))}
      />
      <span class="name" title=${g.name}>${g.name}</span>
      ${g.kind === "shortcut" ? html`<span class="muted small">Non-Steam</span>` : g.last_played ? html`<span class="muted small">${relative(g.last_played)}</span>` : nothing}
      ${confirming
        ? html`<div class="confirm" role="group" aria-label=${`Launch ${g.name}?`}>
            <button @click=${() => (this._confirm = undefined)} aria-label=${`Cancel launching ${g.name}`}>Cancel</button>
            <button class="primary" @click=${() => this._launch(g)} aria-label=${`Confirm launch of ${g.name}`}>Launch</button>
          </div>`
        : html`<button
            ?disabled=${!canLaunch || !ready}
            title=${!canLaunch ? "Launching is not allowed on this connection (DeckBeacon options)" : !ready ? `Not ready (${g.status})` : ""}
            aria-label=${`Launch ${g.name}`}
            @click=${() => (this._confirm = g.app_id)}
          >
            Launch
          </button>`}
    </div>`;
  }

  override render() {
    const pending = this.renderState();
    if (pending) return pending;
    const canLaunch = !!this._lib?.can_launch && this._device!.connected;
    const games = this._games();
    const total = this._lib?.games.length ?? 0;
    return html`<ha-card>
      <div class="content">
        ${this.header("Game library", html`<span class="chip">${total}</span>`)}
        <div class="controls">
          <label class="sr-only" for="q">Search games</label>
          <input id="q" type="search" placeholder="Search" .value=${this._query} @input=${(e: Event) => (this._query = (e.target as HTMLInputElement).value)} />
          <label class="sr-only" for="f">Show</label>
          <select id="f" .value=${this._filter} @change=${(e: Event) => (this._filter = (e.target as HTMLSelectElement).value as Filter)}>
            <option value="all">All</option>
            <option value="game">Steam games</option>
            <option value="shortcut">Non-Steam</option>
          </select>
          <label class="sr-only" for="s">Sort</label>
          <select id="s" .value=${this._sort} @change=${(e: Event) => (this._sort = (e.target as HTMLSelectElement).value as Sort)}>
            <option value="name">Name</option>
            <option value="recent">Recently played</option>
          </select>
        </div>
        ${!this._device!.connected
          ? html`<p class="note">Device offline: showing the last known library. Launching needs the device online.</p>`
          : this._lib && !this._lib.can_launch
            ? html`<p class="note">Launching is not allowed on the current connection. Change it in the DeckBeacon options.</p>`
            : nothing}
        <div role="status" aria-live="polite" class=${this._status?.kind === "error" ? "status error" : "status muted"}>${this._status?.text ?? ""}</div>
        ${!this._lib
          ? html`<p class="muted" role="status">Loading library…</p>`
          : games.length
            ? html`<div class="grid" role="list" aria-label="Games">${games.map((g) => html`<div role="listitem">${this._tile(g, canLaunch)}</div>`)}</div>`
            : html`<p class="muted">No matching games</p>`}
      </div>
    </ha-card>`;
  }
}
