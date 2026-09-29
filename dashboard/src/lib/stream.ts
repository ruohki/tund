import "server-only";
import { EventEmitter } from "node:events";
import { db } from "./db";
import { getSummary, type RequestSummary } from "./requests";
import { teamMembersForHostname } from "./teams";
import { getConnection, type ConnectionSummary } from "./connections";

export type TunnelEvent = { id: string; hostname: string; event: "online" | "offline" };

export type LiveEvent =
  | { type: "request"; data: RequestSummary }
  | { type: "tunnel"; data: TunnelEvent }
  | { type: "connection"; data: ConnectionSummary };

type Hub = { emitter: EventEmitter; ready: Promise<void> | null };

const g = globalThis as unknown as { __tundHub?: Hub };

function hub(): Hub {
  if (!g.__tundHub) {
    const emitter = new EventEmitter();
    emitter.setMaxListeners(0);
    g.__tundHub = { emitter, ready: null };
  }
  return g.__tundHub;
}

function parse(payload: string): Record<string, string> | null {
  try {
    return JSON.parse(payload);
  } catch {
    return null;
  }
}

// Members of the team owning a hostname, cached briefly so busy tunnels don't
// cost one query per request.
const teamCache = new Map<string, { at: number; userIds: string[] }>();

/** The tunnel owner plus every member of the team that owns the hostname. */
async function recipients(ownerId: string, hostname: string | undefined): Promise<string[]> {
  if (!hostname) return [ownerId];
  let hit = teamCache.get(hostname);
  if (!hit || Date.now() - hit.at > 5000) {
    hit = { at: Date.now(), userIds: (await teamMembersForHostname(hostname)).userIds };
    teamCache.set(hostname, hit);
    if (teamCache.size > 5000) teamCache.clear();
  }
  return [...new Set([ownerId, ...hit.userIds])];
}

/** Starts one LISTEN per channel for the whole process (postgres.js reconnects automatically). */
function ensureListening(h: Hub): Promise<void> {
  if (h.ready) return h.ready;
  const sql = db();
  h.ready = Promise.all([
    sql.listen("tund_requests", async (payload) => {
      const p = parse(payload);
      if (!p?.id || !p.user_id) return;
      try {
        const channels = (await recipients(p.user_id, p.hostname)).map((u) => `user:${u}`);
        if (!channels.some((c) => h.emitter.listenerCount(c) > 0)) return;
        const summary = await getSummary(p.user_id, p.id);
        if (!summary) return;
        for (const c of channels) h.emitter.emit(c, { type: "request", data: summary } satisfies LiveEvent);
      } catch (err) {
        console.error("tund: failed to load request summary", err);
      }
    }),
    sql.listen("tund_connections", async (payload) => {
      const p = parse(payload);
      if (!p?.id || !p.user_id) return;
      try {
        // Same fan-out as requests: the tunnel owner plus members of the team owning the address.
        const channels = (await recipients(p.user_id, p.address)).map((u) => `user:${u}`);
        if (!channels.some((c) => h.emitter.listenerCount(c) > 0)) return;
        const summary = await getConnection(p.user_id, p.id);
        if (!summary) return;
        for (const c of channels) h.emitter.emit(c, { type: "connection", data: summary } satisfies LiveEvent);
      } catch (err) {
        console.error("tund: failed to load connection", err);
      }
    }),
    sql.listen("tund_tunnels", async (payload) => {
      const p = parse(payload);
      if (!p?.id || !p.user_id) return;
      const event = {
        type: "tunnel",
        data: { id: p.id, hostname: p.hostname ?? "", event: p.event === "online" ? "online" : "offline" },
      } satisfies LiveEvent;
      try {
        for (const u of await recipients(p.user_id, p.hostname)) h.emitter.emit(`user:${u}`, event);
      } catch (err) {
        console.error("tund: failed to fan out tunnel event", err);
        h.emitter.emit(`user:${p.user_id}`, event);
      }
    }),
  ]).then(
    () => undefined,
    (err) => {
      h.ready = null; // retry on the next subscriber
      throw err;
    },
  );
  return h.ready;
}

export async function subscribe(userId: string, fn: (e: LiveEvent) => void): Promise<() => void> {
  const h = hub();
  await ensureListening(h);
  const channel = `user:${userId}`;
  h.emitter.on(channel, fn);
  return () => h.emitter.off(channel, fn);
}
