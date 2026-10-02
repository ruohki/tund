"use server";

import { requireUser } from "@/lib/auth";
import { db } from "@/lib/db";
import { internalApi, InternalApiError } from "@/lib/internal";
import { deleteConnections } from "@/lib/connections";
import { listTunnels } from "@/lib/metrics";
import { deleteRequests, getSummary, isUuid, visibleTo, type RequestFilters, type RequestSummary } from "@/lib/requests";

/** Changes to a captured request before replaying it (docs/SPEC.md "Public API"). */
export type ReplayOverride = {
  /** Another online tunnel (hostname) to send it through. */
  hostname?: string;
  method?: string;
  /** Path and query. */
  path?: string;
  /** Replaces all headers. */
  headers?: Record<string, string[]>;
  /** Replaces the body (text). */
  body?: string;
};

function checkOverride(o: ReplayOverride): string | null {
  if (o.hostname !== undefined && !/^[a-z0-9.-]{1,253}$/i.test(o.hostname)) return "That is not a hostname.";
  if (o.method !== undefined && !/^[A-Za-z]{1,16}$/.test(o.method)) return "The method must be a single word like GET or POST.";
  if (o.path !== undefined && (!o.path.startsWith("/") || o.path.length > 8192 || /\s/.test(o.path)))
    return "The path must start with / and contain no spaces (encode them as %20).";
  if (o.headers !== undefined) {
    for (const [k, vs] of Object.entries(o.headers)) {
      if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(k)) return `Invalid header name “${k}”.`;
      if (!Array.isArray(vs) || vs.some((v) => typeof v !== "string" || /[\r\n]/.test(v))) return `Invalid value for header ${k}.`;
    }
  }
  if (o.body !== undefined && o.body.length > 16 * 1024 * 1024) return "The body is too large to replay (16 MB at most).";
  return null;
}

export async function replayRequestAction(
  id: string,
  override?: ReplayOverride,
): Promise<{ ok: true; summary: RequestSummary | null; requestId: string } | { ok: false; error: string }> {
  const user = await requireUser();
  if (!isUuid(id)) return { ok: false, error: "Unknown request." };
  const [row] = await db()`select hostname, user_id from requests where id = ${id} and ${visibleTo(user.id)}`;
  if (!row) return { ok: false, error: "This request no longer exists." };
  if (override) {
    const problem = checkOverride(override);
    if (problem) return { ok: false, error: problem };
  }
  const target = override?.hostname || (row.hostname as string);
  try {
    // Team members may replay traffic on their team's domains (docs/SPEC.md "Traffic
    // visibility"). The edge re-checks that with the requesting user's id, also for
    // another target hostname.
    const res = await internalApi<{ request_id?: string }>("/internal/replay", {
      request_id: id,
      user_id: user.id,
      ...(override ? { override } : {}),
    });
    const newId = res.request_id ?? "";
    // The server records the replay asynchronously; give it a moment before reading it back.
    let summary: RequestSummary | null = null;
    for (let i = 0; i < 10 && isUuid(newId) && !summary; i++) {
      summary = await getSummary(user.id, newId);
      if (!summary) await new Promise((r) => setTimeout(r, 150));
    }
    return { ok: true, requestId: newId, summary };
  } catch (err) {
    if (err instanceof InternalApiError && err.status === 409) {
      return { ok: false, error: `No tunnel of yours is online for ${target}. Start it to replay requests through it.` };
    }
    if (err instanceof InternalApiError && err.status === 400) {
      return { ok: false, error: err.message };
    }
    return { ok: false, error: err instanceof Error ? err.message : "Replay failed." };
  }
}

/** Hostnames of the online HTTP tunnels the user can replay through. */
export async function replayTargetsAction(): Promise<string[]> {
  const user = await requireUser();
  const online = await listTunnels(user.id, { online: true, limit: 500, includeTeams: true });
  return [...new Set(online.filter((t) => t.proto === "http").map((t) => t.hostname))].sort();
}

export async function clearRequestsAction(filters: RequestFilters): Promise<{ deleted: number }> {
  const user = await requireUser();
  const deleted = await deleteRequests(user.id, {
    host: filters.host || undefined,
    method: filters.method || undefined,
    status: filters.status || undefined,
    q: filters.q || undefined,
    body: Boolean(filters.body),
  });
  return { deleted };
}

export async function clearConnectionsAction(address: string): Promise<{ deleted: number }> {
  const user = await requireUser();
  return { deleted: await deleteConnections(user.id, address || undefined) };
}
