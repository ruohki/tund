import type { RequestDetail } from "@/lib/requests";

const SKIP = new Set([
  "host", "content-length", "connection", "keep-alive", "transfer-encoding", "te", "upgrade",
  "proxy-connection", "x-forwarded-for", "x-forwarded-proto", "x-forwarded-host", "x-real-ip",
  "accept-encoding",
]);

const q = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`;

/** Builds a copy-pasteable curl command for a captured request. */
export function toCurl(d: RequestDetail): string {
  const lines = [d.method === "GET" ? `curl ${q(d.url)}` : `curl -X ${d.method} ${q(d.url)}`];
  for (const [k, v] of d.request.headers) {
    if (!SKIP.has(k.toLowerCase())) lines.push(`-H ${q(`${k}: ${v}`)}`);
  }
  const b = d.request.body;
  if (b.kind !== "empty") {
    if ((b.kind === "json" || b.kind === "text" || b.kind === "form") && !b.truncated && b.text != null) {
      let text = b.text;
      if (b.kind === "json") {
        try {
          text = JSON.stringify(JSON.parse(b.text)); // undo pretty-printing
        } catch {
          /* keep as-is */
        }
      }
      lines.push(`--data-binary ${q(text)}`);
    } else {
      lines.push(`--data-binary @request-${d.id.slice(0, 8)}.bin`);
    }
  }
  return lines.join(" \\\n  ");
}
