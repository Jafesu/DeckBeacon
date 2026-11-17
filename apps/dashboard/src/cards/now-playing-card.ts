import { css, html, nothing } from "lit";
import { getArtworkUrl } from "../api";
import { PLACEHOLDER_PORTRAIT, svgDataUrl } from "../assets";
import { DeckBeaconCard, K } from "../base";
import { duration, relative } from "../format";
import { cardStyles } from "../styles";

/** Current game with Steam artwork, session and Rich Presence activity. */
export class NowPlayingCard extends DeckBeaconCard {
  static override properties = { ...DeckBeaconCard.properties, _art: { state: true }, _close: { state: true }, _closeMsg: { state: true } };
  static override styles = [
    cardStyles,
    css`
      .layout {
        display: flex;
        gap: 16px;
        align-items: flex-start;
      }
      .art {
        width: 30%;
        max-width: 160px;
        min-width: 84px;
        aspect-ratio: 2 / 3;
        border-radius: 8px;
        object-fit: cover;
        background: var(--secondary-background-color);
        flex: none;
      }
      .game {
        font-size: 1.3em;
        font-weight: 500;
        color: var(--primary-text-color);
        margin: 0 0 4px;
        overflow-wrap: anywhere;
      }
      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
        align-items: center;
        margin-top: 12px;
      }
      .actions .msg {
        flex-basis: 100%;
        font-size: 0.875em;
      }
      button.danger {
        border-color: var(--error-color, #db4437);
        color: var(--error-color, #db4437);
      }
      .activity {
        margin-top: 12px;
        padding: 8px 12px;
        border-left: 3px solid var(--primary-color);
        background: var(--secondary-background-color, rgba(127, 127, 127, 0.1));
        border-radius: 0 8px 8px 0;
      }
    `,
  ];

  protected _art?: string;
  private _artFor?: string;
  /** Close flow: confirm -> closing -> (exited) | stuck -> force-confirm -> forcing. */
  protected _close?: "confirm" | "closing" | "stuck" | "force-confirm" | "forcing";
  protected _closeMsg?: string;
  private _closeTimer?: ReturnType<typeof setTimeout>;

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    clearTimeout(this._closeTimer);
  }

  private async _doClose(force: boolean): Promise<void> {
    this._close = force ? "forcing" : "closing";
    this._closeMsg = force ? "Force closing…" : "Closing…";
    try {
      await this.hass!.callService("deckbeacon", "close_game", { device_id: this._device!.device_id, force });
    } catch (err) {
      this._close = force ? undefined : "stuck";
      this._closeMsg = (err as Error)?.message || "Could not close the game";
      return;
    }
    clearTimeout(this._closeTimer);
    // The game sensor going away is the confirmation; no polling.
    this._closeTimer = setTimeout(() => {
      if (this.has(K.appId) && (this._close === "closing" || this._close === "forcing")) {
        this._close = "stuck";
        this._closeMsg = force ? "The game is still running." : "The game didn't close.";
      }
    }, force ? 12_000 : 25_000);
  }

  private _actions() {
    const d = this._device!;
    if (!d.connected || !d.can_launch) return nothing;
    const name = this.value(K.game) ?? "the game";
    switch (this._close) {
      case "confirm":
        return html`<div class="actions" role="group" aria-label=${`Close ${name}?`}>
          <span class="msg">Close ${name}? Unsaved progress may be lost.</span>
          <button @click=${() => (this._close = undefined)}>Cancel</button>
          <button class="primary" @click=${() => this._doClose(false)}>Close game</button>
        </div>`;
      case "force-confirm":
        return html`<div class="actions" role="group" aria-label=${`Force close ${name}?`}>
          <span class="msg">Force close ends ${name} immediately. Unsaved progress will be lost.</span>
          <button @click=${() => (this._close = "stuck")}>Cancel</button>
          <button class="danger" @click=${() => this._doClose(true)}>Force close</button>
        </div>`;
      case "closing":
      case "forcing":
        return html`<div class="actions" role="status"><span class="msg muted">${this._closeMsg}</span></div>`;
      case "stuck":
        return html`<div class="actions">
          <span class="msg" role="status">${this._closeMsg}</span>
          <button @click=${() => this._doClose(false)}>Try again</button>
          <button class="danger" @click=${() => (this._close = "force-confirm")}>Force close…</button>
        </div>`;
      default:
        return html`<div class="actions"><button @click=${() => (this._close = "confirm")} aria-label=${`Close game: ${name}`}>Close game</button></div>`;
    }
  }

  override getCardSize(): number {
    return 4;
  }

  override updated(): void {
    const appId = this.num(K.appId);
    if (appId === undefined && this._close) {
      this._close = undefined; // the game exited
      clearTimeout(this._closeTimer);
    }
    const key = appId ? `${this._device?.device_id}/${appId}` : undefined;
    if (key === this._artFor) return;
    this._artFor = key;
    this._art = undefined;
    if (appId && this.hass && this._device) {
      getArtworkUrl(this.hass, this._device.device_id, appId, "capsule").then((url) => {
        if (this._artFor === key) this._art = url;
      });
    }
  }

  override render() {
    const pending = this.renderState();
    if (pending) return pending;
    const playing = this.has(K.appId);
    const name = playing ? this.value(K.game) ?? `App ${this.value(K.appId)}` : undefined;
    const details = this.value(K.activityDetails);
    const state = this.value(K.activityState);
    const size = this.num(K.partySize);
    const max = this.num(K.partyMax);
    const elapsed = this.num(K.sessionDuration);
    return html`<ha-card>
      <div class="content">
        ${this.header("Now playing", this.connectionChip())}
        ${playing
          ? html`<div class="layout">
              <img
                class="art"
                src=${this._art ?? svgDataUrl(PLACEHOLDER_PORTRAIT)}
                alt=${this._art ? `${name} artwork` : ""}
                @error=${(e: Event) => ((e.target as HTMLImageElement).src = svgDataUrl(PLACEHOLDER_PORTRAIT))}
              />
              <div style="min-width:0;flex:1">
                <p class="game">${name}</p>
                <div class="muted small">
                  ${elapsed !== undefined ? html`Playing for ${duration(elapsed)}` : nothing}
                  ${this.has(K.sessionStarted) ? html` · started ${relative(this.value(K.sessionStarted))}` : nothing}
                </div>
                ${details || state
                  ? html`<div class="activity" aria-label="Game activity">
                      ${details ? html`<div>${details}</div>` : nothing}
                      ${state ? html`<div class="muted small">${state}${max ? html` · party ${size ?? 0} of ${max}` : nothing}</div>` : nothing}
                    </div>`
                  : nothing}
                ${this._actions()}
              </div>
            </div>`
          : html`<p class="muted" role="status">${this._device!.connected ? "Not playing" : "Device offline"}</p>`}
      </div>
    </ha-card>`;
  }
}
