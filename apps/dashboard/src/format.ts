// Formatting helpers (English; HA's own formatter is used where available).

export function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "";
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h} h ${m} min`;
  if (m > 0) return `${m} min`;
  return `${s} s`;
}

export function rate(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return "";
  const mb = bytesPerSecond / 1e6;
  return mb >= 1 ? `${mb.toFixed(1)} MB/s` : `${(bytesPerSecond / 1e3).toFixed(0)} kB/s`;
}

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "";
  const units = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

export function relative(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const diff = Math.round((now - t) / 1000);
  if (diff < 60) return "just now";
  if (diff < 3600) return `${Math.floor(diff / 60)} min ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} h ago`;
  return `${Math.floor(diff / 86400)} d ago`;
}
