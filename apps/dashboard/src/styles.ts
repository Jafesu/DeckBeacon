import { css } from "lit";

// Theme-aware: only Home Assistant CSS variables, with sane fallbacks.
export const cardStyles = css`
  :host {
    display: block;
  }
  ha-card {
    height: 100%;
    overflow: hidden;
  }
  .content {
    padding: 16px;
  }
  .header {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 1.1em;
    font-weight: 500;
    color: var(--primary-text-color);
    margin-bottom: 12px;
  }
  .header .grow {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .muted {
    color: var(--secondary-text-color);
  }
  .small {
    font-size: 0.875em;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    border-radius: 12px;
    font-size: 0.8em;
    background: var(--secondary-background-color, rgba(127, 127, 127, 0.15));
    color: var(--primary-text-color);
    white-space: nowrap;
  }
  .chip.ok {
    background: color-mix(in srgb, var(--success-color, #43a047) 20%, transparent);
  }
  .chip.warn {
    background: color-mix(in srgb, var(--warning-color, #ffa600) 25%, transparent);
  }
  .chip.off {
    background: color-mix(in srgb, var(--error-color, #db4437) 20%, transparent);
  }
  .rows {
    display: grid;
    gap: 8px;
  }
  .row {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: baseline;
  }
  .row .label {
    color: var(--secondary-text-color);
  }
  .row .value {
    color: var(--primary-text-color);
    text-align: end;
    font-variant-numeric: tabular-nums;
  }
  .note {
    font-size: 0.85em;
    color: var(--secondary-text-color);
    font-style: italic;
  }
  .bar {
    height: 8px;
    border-radius: 4px;
    background: var(--secondary-background-color, rgba(127, 127, 127, 0.2));
    overflow: hidden;
  }
  .bar > div {
    height: 100%;
    background: var(--primary-color);
    transition: width 0.4s ease;
  }
  button {
    font: inherit;
    cursor: pointer;
    border-radius: 8px;
    border: 1px solid var(--divider-color, rgba(127, 127, 127, 0.4));
    background: transparent;
    color: var(--primary-text-color);
    padding: 6px 12px;
    min-height: 36px;
  }
  button.primary {
    background: var(--primary-color);
    border-color: var(--primary-color);
    color: var(--text-primary-color, #fff);
  }
  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
  button:focus-visible,
  input:focus-visible,
  select:focus-visible {
    outline: 2px solid var(--primary-color);
    outline-offset: 2px;
  }
  .placeholder {
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--secondary-background-color, rgba(127, 127, 127, 0.15));
    color: var(--secondary-text-color);
    font-size: 0.85em;
    text-align: center;
    padding: 8px;
    box-sizing: border-box;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
  @media (prefers-reduced-motion: reduce) {
    .bar > div {
      transition: none;
    }
  }
`;
