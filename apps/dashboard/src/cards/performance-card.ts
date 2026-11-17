import { css, html, nothing } from "lit";
import { DeckBeaconCard, K } from "../base";
import { cardStyles } from "../styles";

const METERS: { key: string; label: string }[] = [
  { key: K.cpu, label: "CPU" },
  { key: K.gpu, label: "GPU" },
  { key: K.memory, label: "Memory" },
];
const READINGS: { key: string; label: string }[] = [
  { key: K.cpuTemp, label: "CPU temperature" },
  { key: K.gpuTemp, label: "GPU temperature" },
  { key: K.fan, label: "Fan" },
];

/** Live performance: usage meters with a short trend, temperatures, fan. */
export class PerformanceCard extends DeckBeaconCard {
  static override styles = [
    cardStyles,
    css`
      .meter {
        display: grid;
        grid-template-columns: 5.5em 1fr 3.5em;
        gap: 8px;
        align-items: center;
      }
      svg.spark {
        width: 100%;
        height: 18px;
        display: block;
        color: var(--primary-color);
      }
    `,
  ];

  // Trend from live state changes only (no history polling), bounded.
  private _trend = new Map<string, number[]>();
  private _stamp = new Map<string, string>();

  override willUpdate(changed: Map<string, unknown>): void {
    super.willUpdate(changed as never);
    if (!changed.has("hass")) return;
    for (const { key } of METERS) {
      const v = this.num(key);
      const stamp = this.entity(key)?.last_updated;
      if (v === undefined || !stamp || this._stamp.get(key) === stamp) continue;
      this._stamp.set(key, stamp);
      const arr = this._trend.get(key) ?? [];
      arr.push(v);
      if (arr.length > 60) arr.shift();
      this._trend.set(key, arr);
    }
  }

  private _spark(points: number[]) {
    if (points.length < 2) return nothing;
    const step = 100 / (points.length - 1);
    const d = points.map((p, i) => `${i ? "L" : "M"}${(i * step).toFixed(1)},${(18 - (Math.min(100, Math.max(0, p)) / 100) * 16 - 1).toFixed(1)}`).join(" ");
    return html`<svg class="spark" viewBox="0 0 100 18" preserveAspectRatio="none" aria-hidden="true"><path d=${d} fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" /></svg>`;
  }

  override render() {
    const pending = this.renderState();
    if (pending) return pending;
    return html`<ha-card>
      <div class="content">
        ${this.header("Performance", this.connectionChip())}
        <div class="rows">
          ${METERS.map(({ key, label }) => {
            const v = this.num(key);
            return html`<div class="meter">
              <span class="label muted">${label}</span>
              ${v !== undefined
                ? html`<div>
                    <div class="bar" role="progressbar" aria-label=${label} aria-valuemin="0" aria-valuemax="100" aria-valuenow=${Math.round(v)}><div style="width:${Math.min(100, v)}%"></div></div>
                    ${this._spark(this._trend.get(key) ?? [])}
                  </div>`
                : html`<span class="note">${this.explain(key)}</span>`}
              <span class="value" style="text-align:end">${v !== undefined ? `${Math.round(v)}%` : ""}</span>
            </div>`;
          })}
          ${READINGS.map(({ key, label }) => html`<div class="row"><span class="label">${label}</span><span class="value">${this.display(key)}</span></div>`)}
        </div>
      </div>
    </ha-card>`;
  }
}
