import "server-only";
import zlib from "node:zlib";
import { db } from "./db";
import { config } from "./config";
import { teamHostnameMatch, teamSlugFor } from "./teams";

export type RequestSummary = {
  id: string;
  tunnelId: string | null;
  hostname: string;
  method: string;
  path: string;
  status: number;
  error: string;
  durationMs: number;
  reqSize: number;
  respSize: number;
  remoteAddr: string;
  replayOf: string | null;
  contentType: string;
  startedAt: string;
  /** Slug of the team that owns the hostname, when it's a team domain. */
  teamSlug: string | null;
};

export type RequestFilters = {
  host?: string;
  method?: string;
  status?: string; // 2xx | 3xx | 4xx | 5xx
  q?: string;
  tunnel?: string;
};

type Row = Record<string, unknown>;

// Used with an unaliased `from requests`.
const SUMMARY_COLUMNS = `id, tunnel_id, hostname, method, path, status, error, duration_ms, req_body_size, resp_body_size,
  remote_addr, replay_of, coalesce(resp_headers->'Content-Type'->>0, '') as content_type, started_at,
  (select tt.slug from domains td join teams tt on tt.id = td.team_id
   where td.team_id is not null and (td.hostname = requests.hostname
     or (td.hostname like '*.%' and requests.hostname = split_part(requests.hostname, '.', 1) || substr(td.hostname, 2)))
   limit 1) as team_slug`;

export function toSummary(r: Row): RequestSummary {
  return {
    id: r.id as string,
    tunnelId: (r.tunnel_id as string) ?? null,
    hostname: r.hostname as string,
    method: r.method as string,
    path: r.path as string,
    status: r.status as number,
    error: r.error as string,
    durationMs: r.duration_ms as number,
    reqSize: r.req_body_size as number,
    respSize: r.resp_body_size as number,
    remoteAddr: r.remote_addr as string,
    replayOf: (r.replay_of as string) ?? null,
    contentType: r.content_type as string,
    startedAt: (r.started_at as Date).toISOString(),
    teamSlug: (r.team_slug as string) ?? null,
  };
}

/**
 * Requests a user may see: their own tunnels' traffic plus traffic on domains
 * owned by their teams (docs/SPEC.md "Traffic visibility"). `table` is the
 * alias or table name the predicate refers to.
 */
export function visibleTo(userId: string, table = "requests") {
  const sql = db();
  return sql`(${sql.unsafe(table)}.user_id = ${userId} or ${teamHostnameMatch(sql, `${table}.hostname`, userId)})`;
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const isUuid = (s: unknown): s is string => typeof s === "string" && UUID_RE.test(s);

function filterSql(userId: string, f: RequestFilters, opts: { ownOnly?: boolean } = {}) {
  const sql = db();
  const parts = [opts.ownOnly ? sql`user_id = ${userId}` : visibleTo(userId)];
  if (f.host) parts.push(sql`hostname = ${f.host.toLowerCase()}`);
  if (f.tunnel && isUuid(f.tunnel)) parts.push(sql`tunnel_id = ${f.tunnel}`);
  if (f.method) parts.push(sql`method = ${f.method.toUpperCase()}`);
  switch (f.status) {
    case "2xx":
      parts.push(sql`status between 100 and 299`);
      break;
    case "3xx":
      parts.push(sql`status between 300 and 399`);
      break;
    case "4xx":
      parts.push(sql`status between 400 and 499`);
      break;
    case "5xx":
      parts.push(sql`(status >= 500 or status = 0)`);
      break;
  }
  if (f.q) parts.push(sql`path ilike ${"%" + f.q.replace(/[\\%_]/g, (c) => "\\" + c) + "%"}`);
  return parts.reduce((acc, p) => sql`${acc} and ${p}`);
}

export async function listRequests(
  userId: string,
  f: RequestFilters,
  cursor: { before?: string; beforeId?: string } = {},
  limit = 100,
): Promise<RequestSummary[]> {
  const sql = db();
  const where = filterSql(userId, f);
  const before = cursor.before && !Number.isNaN(Date.parse(cursor.before)) ? new Date(cursor.before) : null;
  const cursorSql =
    before && isUuid(cursor.beforeId)
      ? sql`and (started_at, id) < (${before}, ${cursor.beforeId})`
      : before
        ? sql`and started_at < ${before}`
        : sql``;
  const rows = await sql`
    select ${sql.unsafe(SUMMARY_COLUMNS)} from requests
    where ${where} ${cursorSql}
    order by started_at desc, id desc
    limit ${Math.min(Math.max(limit, 1), 500)}`;
  return rows.map(toSummary);
}

export async function getSummary(userId: string, id: string): Promise<RequestSummary | null> {
  if (!isUuid(id)) return null;
  const [row] = await db()`select ${db().unsafe(SUMMARY_COLUMNS)} from requests where id = ${id} and ${visibleTo(userId)}`;
  return row ? toSummary(row) : null;
}

/** Deletes the user's own captured requests only; team traffic belongs to the tunnel owners. */
export async function deleteRequests(userId: string, f: RequestFilters): Promise<number> {
  const res = await db()`delete from requests where ${filterSql(userId, f, { ownOnly: true })}`;
  return res.count;
}

// ---------------------------------------------------------------------------
// Detail + body rendering

export type Headers = Record<string, string[]>;

export type BodyView = {
  size: number;
  captured: number;
  truncated: boolean;
  contentType: string;
  encoding: string;
  kind: "empty" | "json" | "form" | "text" | "image" | "binary";
  text?: string;
  form?: [string, string][];
  hex?: string;
  decodeError?: string;
  renderedLimit?: boolean;
};

export type RequestDetail = RequestSummary & {
  proto: string;
  ttfbMs: number;
  url: string;
  query: [string, string][];
  request: { headers: [string, string][]; body: BodyView };
  response: { headers: [string, string][]; body: BodyView };
  tunnel: { name: string; localAddr: string; online: boolean } | null;
};

const RENDER_LIMIT = 512 * 1024;

function header(h: Headers, name: string): string {
  const key = Object.keys(h).find((k) => k.toLowerCase() === name.toLowerCase());
  return key ? (h[key]?.[0] ?? "") : "";
}

function flatten(h: Headers): [string, string][] {
  const out: [string, string][] = [];
  for (const [k, vs] of Object.entries(h ?? {})) for (const v of vs ?? []) out.push([k, v]);
  return out.sort((a, b) => a[0].localeCompare(b[0]));
}

/** Decodes Content-Encoding. Truncated captures are decoded as far as possible. */
export function decodeBody(buf: Buffer, encoding: string): { data: Buffer; error?: string } {
  const enc = encoding.toLowerCase().trim();
  if (!enc || enc === "identity") return { data: buf };
  const lenient = { finishFlush: zlib.constants.Z_SYNC_FLUSH };
  try {
    switch (enc) {
      case "gzip":
      case "x-gzip":
        try {
          return { data: zlib.gunzipSync(buf) };
        } catch {
          return { data: zlib.gunzipSync(buf, lenient) };
        }
      case "deflate":
        try {
          return { data: zlib.inflateSync(buf) };
        } catch {
          return { data: zlib.inflateRawSync(buf, lenient) };
        }
      case "br":
        try {
          return { data: zlib.brotliDecompressSync(buf) };
        } catch {
          return {
            data: zlib.brotliDecompressSync(buf, { finishFlush: zlib.constants.BROTLI_OPERATION_FLUSH }),
          };
        }
      case "zstd": {
        const z = zlib as unknown as { zstdDecompressSync?: (b: Buffer) => Buffer };
        if (!z.zstdDecompressSync) return { data: buf, error: "zstd is not supported by this Node.js version." };
        return { data: z.zstdDecompressSync(buf) };
      }
      default:
        return { data: buf, error: `Unknown content encoding “${encoding}”; showing raw bytes.` };
    }
  } catch (err) {
    return { data: buf, error: `Could not decode ${enc} body: ${err instanceof Error ? err.message : String(err)}` };
  }
}

function isProbablyText(buf: Buffer): boolean {
  const sample = buf.subarray(0, 4096);
  let control = 0;
  for (const b of sample) {
    if (b === 0) return false;
    if (b < 9 || (b > 13 && b < 32)) control++;
  }
  if (control > sample.length * 0.02) return false;
  try {
    new TextDecoder("utf-8", { fatal: true }).decode(sample.length < buf.length ? sample.subarray(0, sample.length - 4) : sample);
    return true;
  } catch {
    return false;
  }
}

function hexDump(buf: Buffer, max = 1024): string {
  const lines: string[] = [];
  const slice = buf.subarray(0, max);
  for (let i = 0; i < slice.length; i += 16) {
    const chunk = slice.subarray(i, i + 16);
    const hex = Array.from(chunk, (b) => b.toString(16).padStart(2, "0")).join(" ");
    const ascii = Array.from(chunk, (b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : ".")).join("");
    lines.push(`${i.toString(16).padStart(8, "0")}  ${hex.padEnd(47)}  ${ascii}`);
  }
  return lines.join("\n");
}

export function renderBody(raw: Buffer | null, size: number, truncated: boolean, h: Headers): BodyView {
  const contentType = header(h, "Content-Type");
  const encoding = header(h, "Content-Encoding");
  const base = { size, captured: raw?.length ?? 0, truncated, contentType, encoding };
  if (!raw || raw.length === 0) return { ...base, kind: "empty" };

  const { data, error } = decodeBody(raw, encoding);
  const ct = contentType.toLowerCase();
  const view: BodyView = { ...base, kind: "binary", decodeError: error };

  if (ct.startsWith("image/")) return { ...view, kind: "image" };

  const textual =
    /json|xml|javascript|ecmascript|text\/|x-www-form-urlencoded|graphql|yaml|csv|html|svg/.test(ct) ||
    (!ct.startsWith("application/octet-stream") && isProbablyText(data));

  if (!textual) return { ...view, hex: hexDump(data) };

  let text = data.toString("utf8");
  if (text.length > RENDER_LIMIT) {
    text = text.slice(0, RENDER_LIMIT);
    view.renderedLimit = true;
  }

  if (ct.includes("x-www-form-urlencoded")) {
    const form = [...new URLSearchParams(text).entries()];
    return { ...view, kind: "form", text, form };
  }

  if (ct.includes("json") || /^\s*[[{]/.test(text)) {
    try {
      return { ...view, kind: "json", text: JSON.stringify(JSON.parse(text), null, 2) };
    } catch {
      // Truncated or invalid JSON: show as text.
      if (ct.includes("json")) return { ...view, kind: "text", text };
    }
  }
  return { ...view, kind: "text", text };
}

export async function getRequestDetail(userId: string, id: string): Promise<RequestDetail | null> {
  if (!isUuid(id)) return null;
  const sql = db();
  const [r] = await sql`
    select r.*, coalesce(r.resp_headers->'Content-Type'->>0, '') as content_type,
           t.name as tunnel_name, t.local_addr as tunnel_local_addr, (t.ended_at is null) as tunnel_online,
           ${teamSlugFor(sql, "r.hostname")} as team_slug
    from requests r left join tunnels t on t.id = r.tunnel_id
    where r.id = ${id} and ${visibleTo(userId, "r")}`;
  if (!r) return null;
  const summary = toSummary(r);
  const reqHeaders = (r.req_headers ?? {}) as Headers;
  const respHeaders = (r.resp_headers ?? {}) as Headers;
  const url = config().publicUrl(summary.hostname) + summary.path;
  const qIndex = summary.path.indexOf("?");
  const query: [string, string][] = qIndex >= 0 ? [...new URLSearchParams(summary.path.slice(qIndex + 1)).entries()] : [];
  return {
    ...summary,
    proto: r.proto as string,
    ttfbMs: r.ttfb_ms as number,
    url,
    query,
    request: {
      headers: flatten(reqHeaders),
      body: renderBody(r.req_body as Buffer | null, r.req_body_size as number, r.req_body_truncated as boolean, reqHeaders),
    },
    response: {
      headers: flatten(respHeaders),
      body: renderBody(r.resp_body as Buffer | null, r.resp_body_size as number, r.resp_body_truncated as boolean, respHeaders),
    },
    tunnel: r.tunnel_name
      ? { name: r.tunnel_name as string, localAddr: r.tunnel_local_addr as string, online: Boolean(r.tunnel_online) }
      : null,
  };
}

export async function getRawBody(
  userId: string,
  id: string,
  part: "req" | "resp",
): Promise<{ data: Buffer; headers: Headers; truncated: boolean } | null> {
  if (!isUuid(id)) return null;
  const sql = db();
  const [r] =
    part === "req"
      ? await sql`select req_body as body, req_headers as headers, req_body_truncated as truncated from requests where id = ${id} and ${visibleTo(userId)}`
      : await sql`select resp_body as body, resp_headers as headers, resp_body_truncated as truncated from requests where id = ${id} and ${visibleTo(userId)}`;
  if (!r) return null;
  return { data: (r.body as Buffer) ?? Buffer.alloc(0), headers: (r.headers ?? {}) as Headers, truncated: r.truncated as boolean };
}

export async function requestHostnames(userId: string): Promise<string[]> {
  const rows = await db()`
    select hostname from (
      select hostname, max(started_at) as last from requests where ${visibleTo(userId)} group by hostname
      union all
      select hostname, max(started_at) as last from tunnels where ${visibleTo(userId, "tunnels")} group by hostname
    ) h group by hostname order by max(last) desc limit 100`;
  return rows.map((r) => r.hostname as string);
}
