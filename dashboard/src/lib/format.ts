// Formatting helpers usable from server and client components.

export function formatDuration(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms)) return "—";
  if (ms < 1) return `${ms.toFixed(2)} ms`;
  if (ms < 10) return `${ms.toFixed(1)} ms`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
  if (ms < 3_600_000) {
    const m = Math.floor(ms / 60_000);
    const s = Math.floor((ms % 60_000) / 1000);
    return s ? `${m}m ${s}s` : `${m}m`;
  }
  if (ms < 86_400_000) {
    const h = Math.floor(ms / 3_600_000);
    const m = Math.floor((ms % 3_600_000) / 60_000);
    return m ? `${h}h ${m}m` : `${h}h`;
  }
  const d = Math.floor(ms / 86_400_000);
  const h = Math.floor((ms % 86_400_000) / 3_600_000);
  return h ? `${d}d ${h}h` : `${d}d`;
}

export function formatBytes(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n)) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatCompact(n: number): string {
  if (n < 1000) return String(n);
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat("en").format(n);
}

export function formatPercent(ratio: number): string {
  if (!Number.isFinite(ratio)) return "—";
  const p = ratio * 100;
  if (p === 0) return "0%";
  if (p < 0.1) return "<0.1%";
  return `${p < 10 ? p.toFixed(1) : Math.round(p)}%`;
}

export function timeAgo(date: Date | string | null | undefined, now = Date.now()): string {
  if (!date) return "never";
  const t = typeof date === "string" ? Date.parse(date) : date.getTime();
  const s = Math.round((now - t) / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h ago`;
  const d = Math.round(h / 24);
  if (d < 60) return `${d}d ago`;
  return new Date(t).toLocaleDateString("en", { year: "numeric", month: "short", day: "numeric" });
}

export function formatDateTime(date: Date | string, timeZone?: string): string {
  const d = typeof date === "string" ? new Date(date) : date;
  return d.toLocaleString("en", {
    timeZone,
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatClock(date: Date | string, timeZone?: string): string {
  const d = typeof date === "string" ? new Date(date) : date;
  return d.toLocaleTimeString("en-GB", { timeZone, hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function sessionLength(start: Date | string, end: Date | string | null, now = Date.now()): string {
  const s = typeof start === "string" ? Date.parse(start) : start.getTime();
  const e = end ? (typeof end === "string" ? Date.parse(end) : end.getTime()) : now;
  return formatDuration(Math.max(0, e - s));
}

export type StatusClass = "2xx" | "3xx" | "4xx" | "5xx";

/** 1xx (websocket upgrades) counts as success; status 0 (no response) as a server-side failure. */
export function statusClass(status: number): StatusClass {
  if (status === 0 || status >= 500) return "5xx";
  if (status >= 400) return "4xx";
  if (status >= 300) return "3xx";
  return "2xx";
}

export const STATUS_TEXT: Record<number, string> = {
  100: "Continue", 101: "Switching Protocols", 200: "OK", 201: "Created", 202: "Accepted", 204: "No Content",
  206: "Partial Content", 301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified",
  307: "Temporary Redirect", 308: "Permanent Redirect", 400: "Bad Request", 401: "Unauthorized",
  403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed", 408: "Request Timeout", 409: "Conflict",
  410: "Gone", 413: "Payload Too Large", 415: "Unsupported Media Type", 422: "Unprocessable Content",
  429: "Too Many Requests", 500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway",
  503: "Service Unavailable", 504: "Gateway Timeout",
};

/** "darwin/arm64" → "macOS arm64". */
export function osLabel(os: string): string {
  const [goos, arch] = os.split("/");
  const names: Record<string, string> = { darwin: "macOS", linux: "Linux", windows: "Windows", freebsd: "FreeBSD" };
  const name = names[goos] ?? goos;
  return arch ? `${name} ${arch}` : name || "unknown OS";
}

/** "1.23 GB" in decimal units, matching how quotas are counted. */
export function formatTransfer(bytes: number): string {
  if (bytes < 1000) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = bytes / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v < 10 ? v.toFixed(2) : v < 100 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatKbps(kbps: number): string {
  if (!kbps) return "unlimited";
  return kbps >= 1000 ? `${(kbps / 1000).toLocaleString("en", { maximumFractionDigits: 1 })} Mbit/s` : `${kbps} kbit/s`;
}
