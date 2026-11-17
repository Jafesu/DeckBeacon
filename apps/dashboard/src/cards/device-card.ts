import { html, nothing } from "lit";
import { DeckBeaconCard, K } from "../base";
import { relative } from "../format";
import { cardStyles } from "../styles";

const POWER: Record<string, string> = { on: "On", suspended: "Asleep", shutting_down: "Shutting down", rebooting: "Restarting", off: "Off", unknown: "Unknown" };
const SESSION: Record<string, string> = { gaming: "Gaming Mode", desktop: "Desktop Mode" };
const UI: Record<string, string> = { gamepad: "Gamepad UI", desktop: "Desktop UI", unknown: "Unknown" };

/** Device status: connection, power, session, battery, temperatures. */
export class DeviceCard extends DeckBeaconCard {
  static override styles = cardStyles;

  override render() {
    const pending = this.renderState();
    if (pending) return pending;
    const d = this._device!;
    const power = d.power_state;
    const battery = this.num(K.battery);
    const rows: [string, string][] = [
      ["Power", POWER[power] ?? power],
      ["Session", this.has(K.sessionType) ? SESSION[this.value(K.sessionType)!] ?? this.value(K.sessionType)! : this.explain(K.sessionType)],
      ["Steam UI", this.has(K.uiMode) ? UI[this.value(K.uiMode)!] ?? this.value(K.uiMode)! : this.explain(K.uiMode)],
      ["CPU temperature", this.display(K.cpuTemp)],
      ["GPU temperature", this.display(K.gpuTemp)],
      ["Free storage", this.display(K.storageFree)],
    ];
    if (!d.connected) rows.push(["Last seen", relative(this.entity(K.lastSeen)?.state) || "Unknown"]);
    return html`<ha-card>
      <div class="content">
        ${this.header(d.name, this.connectionChip())}
        ${battery !== undefined
          ? html`<div class="rows" style="margin-bottom:12px">
              <div class="row"><span class="label">Battery</span><span class="value">${battery}%${this.has(K.batteryStatus) ? html` · ${this.value(K.batteryStatus)}` : nothing}</span></div>
              <div class="bar" role="progressbar" aria-label="Battery" aria-valuemin="0" aria-valuemax="100" aria-valuenow=${battery}><div style="width:${battery}%"></div></div>
            </div>`
          : nothing}
        <div class="rows">
          ${rows.map(([label, value]) => html`<div class="row"><span class="label">${label}</span><span class="value">${value}</span></div>`)}
        </div>
      </div>
    </ha-card>`;
  }
}
