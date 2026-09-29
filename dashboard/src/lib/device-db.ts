import "server-only";
import type { User } from "./auth";
import { db } from "./db";
import { normalizeUserCode, type DeviceOutcome } from "./device";

type Row = {
  id: string;
  status: "pending" | "approved" | "denied";
  user_id: string | null;
  token_hash: string;
  token_prefix: string;
  client_hostname: string;
  client_os: string;
  client_ip: string;
  created_at: Date;
  expires_at: Date;
  live: boolean;
};

/** What the page shows for a code. Settled codes (approved/denied/expired) never show the Approve button. */
export async function lookupDeviceCode(user: User, code: string): Promise<DeviceOutcome> {
  const [row] = await db()<Row[]>`
    select *, expires_at > now() as live from device_codes where user_code = ${code}`;
  if (!row) return { state: "unknown" };
  return settled(row, user) ?? {
    state: "pending",
    request: {
      userCode: code,
      clientHostname: row.client_hostname,
      clientOs: row.client_os,
      clientIp: row.client_ip,
      createdAt: row.created_at.toISOString(),
      expiresAt: row.expires_at.toISOString(),
    },
  };
}

function settled(row: Row, user: User): DeviceOutcome | null {
  if (row.status === "approved") return row.user_id === user.id ? { state: "approved", email: user.email } : { state: "used" };
  if (row.status === "denied") return { state: "denied" };
  if (!row.live) return { state: "expired" };
  return null;
}

/** Approve or deny a pending code for `user` in one transaction (row lock on the code). */
export async function decideDeviceCode(user: User, input: string, approve: boolean): Promise<DeviceOutcome> {
  const code = normalizeUserCode(input);
  if (!code) return { state: "unknown" };
  try {
    return await db().begin(async (tx) => {
      // Lock the row so a concurrent approve/deny (or the CLI poll deleting it) can't interleave.
      const [row] = await tx<Row[]>`
        select *, expires_at > now() as live from device_codes where user_code = ${code} for update`;
      if (!row) return { state: "unknown" } as const;
      const done = settled(row, user);
      if (done) return done;
      if (!approve) {
        await tx`update device_codes set status = 'denied' where id = ${row.id}`;
        return { state: "denied" } as const;
      }
      const name = `CLI on ${row.client_hostname || "unknown host"}`.slice(0, 80);
      const [token] = await tx`
        insert into authtokens (user_id, name, token_hash, token_prefix)
        values (${user.id}, ${name}, ${row.token_hash}, ${row.token_prefix})
        returning id`;
      await tx`
        update device_codes set status = 'approved', user_id = ${user.id}, authtoken_id = ${token.id}
        where id = ${row.id}`;
      return { state: "approved", email: user.email } as const;
    });
  } catch (err) {
    if ((err as { code?: string }).code === "23505") {
      return { state: "error", message: "This terminal's token is already registered. Run tund login again to get a new code." };
    }
    console.error("tund: device authorization failed", err);
    return { state: "error", message: "Something went wrong while saving your decision. Try again." };
  }
}

