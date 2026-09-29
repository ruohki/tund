"use server";

import { refresh } from "next/cache";
import { requireUser } from "@/lib/auth";
import { db } from "@/lib/db";
import { internalApi, InternalApiError } from "@/lib/internal";
import { isUuid } from "@/lib/requests";
import { config } from "@/lib/config";
import { pinLabel } from "@/lib/static-hostnames";
import { reserveTcpPort } from "@/lib/tcp";
import type { FormState } from "./auth";

export async function stopTunnelAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (!isUuid(id)) return { error: "Unknown tunnel." };
  const [t] = await db()`select id, ended_at from tunnels where id = ${id} and user_id = ${user.id}`;
  if (!t) return { error: "Unknown tunnel." };
  if (t.ended_at) return { ok: "Tunnel is already offline." };
  try {
    await internalApi("/internal/tunnels/stop", { tunnel_id: id, user_id: user.id });
  } catch (err) {
    return { error: err instanceof InternalApiError ? err.message : "Could not stop the tunnel." };
  }
  refresh();
  return { ok: "Tunnel stopped." };
}

/** Keeps a tunnel's base-domain hostname as a static hostname of the account. */
export async function pinTunnelAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (!isUuid(id)) return { error: "Unknown tunnel." };
  const [t] = await db()`select hostname, proto, remote_port from tunnels where id = ${id} and user_id = ${user.id}`;
  if (!t) return { error: "Unknown tunnel." };
  if (t.proto === "tcp") {
    // TCP tunnels keep their port instead of a hostname.
    if (!t.remote_port) return { error: "This tunnel has no port to keep." };
    const res = await reserveTcpPort(user, { teamId: null }, t.remote_port as number);
    if (!res.ok) return { error: res.error };
    refresh();
    return { ok: `Reserved port ${res.port}.` };
  }
  const base = config().baseDomain;
  const hostname = t.hostname as string;
  const label = hostname.endsWith(`.${base}`) ? hostname.slice(0, -(base.length + 1)) : "";
  if (!label || label.includes(".")) return { error: "Only hostnames under the server's own domain can be pinned." };
  const res = await pinLabel(user, label);
  if (!res.ok) return { error: res.error };
  refresh();
  return { ok: res.isDefault ? `Pinned ${res.hostname} as your default.` : `Pinned ${res.hostname}.` };
}
