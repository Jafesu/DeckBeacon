import { html, nothing } from "lit";
import { DeckBeaconCard, K } from "../base";
import { duration, rate } from "../format";
import { cardStyles } from "../styles";

const STATUS: Record<string, string> = {
  idle: "Idle",
  downloading: "Downloading",
  paused: "Paused",
  installing: "Installing",
  updating: "Updating",
  verifying: "Verifying",
  update_required: "Update required",
  uninstalling: "Uninstalling",
  files_missing: "Files missing",
  installed: "Installed",
};

/** Steam download progress, speed and time remaining. */
export class DownloadsCard extends DeckBeaconCard {
  static override styles = cardStyles;

  override render() {
    const pending = this.renderState();
    if (pending) return pending;
    const status = this.value(K.dlStatus);
    const active = status !== undefined && status !== "idle";
    const progress = this.num(K.dlProgress);
    const speed = this.num(K.dlRate);
    const eta = this.num(K.dlEta);
    const updates = this.num(K.updates);
    const speedUnit = this.entity(K.dlRate)?.attributes.unit_of_measurement;
    return html`<ha-card>
      <div class="content">
        ${this.header("Downloads", updates ? html`<span class="chip warn">${updates} update${updates === 1 ? "" : "s"}</span>` : undefined)}
        ${!this.has(K.dlStatus)
          ? html`<p class="note" role="status">${this.explain(K.dlStatus)}</p>`
          : !active
            ? html`<p class="muted" role="status">No downloads in progress</p>`
            : html`<div class="rows">
                <div class="row"><span class="value" style="text-align:start;font-weight:500">${this.value(K.dlName) ?? "Unknown game"}</span><span class="chip">${STATUS[status] ?? status}</span></div>
                ${progress !== undefined
                  ? html`<div class="bar" role="progressbar" aria-label="Download progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow=${progress}><div style="width:${progress}%"></div></div>
                      <div class="row small"><span class="label">${progress.toFixed(1)}%</span><span class="value">${status === "paused" ? "Paused" : nothing}</span></div>`
                  : nothing}
                <div class="row"><span class="label">Speed</span><span class="value">${speed !== undefined ? (speedUnit === "B/s" ? rate(speed) : this.display(K.dlRate)) : this.explain(K.dlRate)}</span></div>
                <div class="row"><span class="label">Time remaining</span><span class="value">${eta !== undefined ? this._eta(eta) : this.explain(K.dlEta)}</span></div>
              </div>`}
      </div>
    </ha-card>`;
  }

  private _eta(v: number): string {
    // The sensor's unit can be converted by the user (s, min, h); normalise.
    const unit = this.entity(K.dlEta)?.attributes.unit_of_measurement;
    const seconds = unit === "min" ? v * 60 : unit === "h" ? v * 3600 : unit === "d" ? v * 86400 : v;
    return duration(seconds);
  }
}
