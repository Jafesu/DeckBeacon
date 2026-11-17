import { css, html, nothing } from "lit";
import { getArtworkUrl } from "../api";
import { PLACEHOLDER_PORTRAIT, svgDataUrl } from "../assets";
import { DeckBeaconCard, K } from "../base";
import { duration, relative } from "../format";
import { cardStyles } from "../styles";

/** Current game with Steam artwork, session and Rich Presence activity. */
export class NowPlayingCard extends DeckBeaconCard {
  static override properties = { ...DeckBeaconCard.properties, _art: { state: true } };
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

  override getCardSize(): number {
    return 4;
  }

  override updated(): void {
    const appId = this.num(K.appId);
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
              </div>
            </div>`
          : html`<p class="muted" role="status">${this._device!.connected ? "Not playing" : "Device offline"}</p>`}
      </div>
    </ha-card>`;
  }
}
