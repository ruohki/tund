"use server";

import { requireUser } from "@/lib/auth";
import { db } from "@/lib/db";
import { internalApi, InternalApiError } from "@/lib/internal";
import { deleteConnections } from "@/lib/connections";
import { deleteRequests, getSummary, isUuid, visibleTo, type RequestFilters, type RequestSummary } from "@/lib/requests";

export async function replayRequestAction(
  id: string,
): Promise<{ ok: true; summary: RequestSummary | null; requestId: string } | { ok: false; error: string }> {
  const user = await requireUser();
  if (!isUuid(id)) return { ok: false, error: "Unknown request." };
  const [row] = await db()`select hostname, user_id from requests where id = ${id} and ${visibleTo(user.id)}`;
  if (!row) return { ok: false, error: "This request no longer exists." };
  try {
    // Team members may replay traffic on their team's domains (docs/SPEC.md "Traffic
    // visibility"). The edge re-checks that with the requesting user's id.
    const res = await internalApi<{ request_id?: string }>("/internal/replay", { request_id: id, user_id: user.id });
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
      return { ok: false, error: `No tunnel for ${row.hostname} is online. Start it again to replay requests.` };
    }
    return { ok: false, error: err instanceof Error ? err.message : "Replay failed." };
  }
}

export async function clearRequestsAction(filters: RequestFilters): Promise<{ deleted: number }> {
  const user = await requireUser();
  const deleted = await deleteRequests(user.id, {
    host: filters.host || undefined,
    method: filters.method || undefined,
    status: filters.status || undefined,
    q: filters.q || undefined,
  });
  return { deleted };
}

export async function clearConnectionsAction(address: string): Promise<{ deleted: number }> {
  const user = await requireUser();
  return { deleted: await deleteConnections(user.id, address || undefined) };
}
