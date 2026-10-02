import "server-only";
import { STATUS_CODES } from "node:http";
import { db } from "./db";
import { config } from "./config";
import { getSettings } from "./settings";
import { decodeBody, filterSql, isUuid, visibleTo, type Headers, type RequestFilters } from "./requests";

// Captured requests as an HTTP Archive (HAR 1.2), for browser dev tools,
// Postman, k6 and friends.

/** Newest requests per export; HAR files are read whole by most tools. */
export const HAR_LIMIT = 200;
/** Bodies beyond this total are left out (with a comment) to keep the file loadable. */
const BODY_BUDGET = 32 * 1024 * 1024;

type HarHeader = { name: string; value: string };
type Row = Record<string, unknown>;

function harHeaders(h: Headers): HarHeader[] {
  const out: HarHeader[] = [];
  for (const [name, values] of Object.entries(h ?? {})) for (const value of values ?? []) out.push({ name, value });
  return out;
}

function find(h: Headers, name: string): string {
  const key = Object.keys(h ?? {}).find((k) => k.toLowerCase() === name);
  return key ? (h[key]?.[0] ?? "") : "";
}

function isText(buf: Buffer): boolean {
  if (buf.includes(0)) return false;
  try {
    new TextDecoder("utf-8", { fatal: true }).decode(buf);
    return true;
  } catch {
    return false;
  }
}

type HarBody = { text?: string; encoding?: "base64"; comment?: string };

/** A captured body as HAR text: decoded and UTF-8 when possible, base64 otherwise. */
function body(raw: Buffer | null, h: Headers, truncated: boolean, budget: { left: number }): HarBody | null {
  if (!raw || raw.length === 0) return null;
  const notes: string[] = [];
  if (truncated) notes.push("only the beginning of this body was captured");
  if (raw.length > budget.left) {
    notes.push("body left out: the export is too large");
    return { comment: notes.join("; ") };
  }
  budget.left -= raw.length;
  const { data, error } = decodeBody(raw, find(h, "content-encoding"));
  if (error) notes.push(error);
  const comment = notes.length ? { comment: notes.join("; ") } : {};
  return isText(data)
    ? { text: data.toString("utf8"), ...comment }
    : { text: data.toString("base64"), encoding: "base64", ...comment };
}

function entry(r: Row, budget: { left: number }) {
  const reqHeaders = (r.req_headers ?? {}) as Headers;
  const respHeaders = (r.resp_headers ?? {}) as Headers;
  const path = r.path as string;
  const url = config().publicUrl(r.hostname as string) + path;
  const q = path.indexOf("?");
  const queryString = q >= 0 ? [...new URLSearchParams(path.slice(q + 1)).entries()].map(([name, value]) => ({ name, value })) : [];
  const status = r.status as number;
  const duration = Math.max(0, Number(r.duration_ms) || 0);
  const ttfb = Math.min(duration, Math.max(0, Number(r.ttfb_ms) || 0));
  const httpVersion = (r.proto as string) || "HTTP/1.1";

  const reqBody = body(r.req_body as Buffer | null, reqHeaders, r.req_body_truncated as boolean, budget);
  const respBody = body(r.resp_body as Buffer | null, respHeaders, r.resp_body_truncated as boolean, budget);
  const postData = reqBody
    ? {
        mimeType: find(reqHeaders, "content-type"),
        text: reqBody.text ?? "",
        ...(reqBody.encoding ? { _encoding: reqBody.encoding } : {}),
        ...(reqBody.comment ? { comment: reqBody.comment } : {}),
      }
    : undefined;

  return {
    startedDateTime: (r.started_at as Date).toISOString(),
    time: duration,
    request: {
      method: r.method as string,
      url,
      httpVersion,
      cookies: [],
      headers: harHeaders(reqHeaders),
      queryString,
      ...(postData ? { postData } : {}),
      headersSize: -1,
      bodySize: Number(r.req_body_size) || 0,
    },
    response: {
      status,
      statusText: status ? (STATUS_CODES[status] ?? "") : "",
      httpVersion,
      cookies: [],
      headers: harHeaders(respHeaders),
      content: {
        size: Number(r.resp_body_size) || 0,
        mimeType: find(respHeaders, "content-type"),
        ...(respBody ?? {}),
      },
      redirectURL: find(respHeaders, "location"),
      headersSize: -1,
      bodySize: Number(r.resp_body_size) || 0,
      ...(r.error ? { comment: r.error as string } : {}),
    },
    cache: {},
    timings: { blocked: -1, dns: -1, connect: -1, ssl: -1, send: 0, wait: ttfb, receive: Math.round((duration - ttfb) * 1000) / 1000 },
    _id: r.id as string,
    _remoteAddress: r.remote_addr as string,
    ...(r.replay_of ? { _replayOf: r.replay_of as string } : {}),
  };
}

/** The selected requests (newest HAR_LIMIT of the filters, or the given ids) as a HAR log. */
export async function requestsAsHar(userId: string, sel: { ids: string[] } | { filters: RequestFilters }) {
  const sql = db();
  const where =
    "ids" in sel
      ? sql`id = any(${sel.ids.filter(isUuid)}::uuid[]) and ${visibleTo(userId)}`
      : filterSql(userId, sel.filters);
  const rows = await sql`
    select id, hostname, method, path, proto, status, error, duration_ms, ttfb_ms, remote_addr, replay_of, started_at,
      req_headers, req_body, req_body_size, req_body_truncated, resp_headers, resp_body, resp_body_size, resp_body_truncated
    from requests where ${where}
    order by started_at desc, id desc limit ${HAR_LIMIT}`;
  const budget = { left: BODY_BUDGET };
  // Oldest first, like a browser's network log.
  const entries = rows.reverse().map((r) => entry(r, budget));
  const { instance_name } = await getSettings();
  return {
    count: entries.length,
    har: { log: { version: "1.2", creator: { name: instance_name, version: "1" }, pages: [], entries } },
  };
}
