import "server-only";
import { randomBytes } from "node:crypto";
import { sha256, type User } from "./auth";
import { db } from "./db";
import { normalizeUserCode, type DeviceOutcome } from "./device";

type Row = {
  id: string;
  status: "pending" | "authorized" | "approved" | "denied";
  user_id: string | null;
  token_hash: string;
  token_prefix: string;
  client_hostname: string;
  client_os: string;
  client_ip: string;
  created_at: Date;
  expires_at: Date;
  callback_port: number | null;
  callback_state: string | null;
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
      callback: row.callback_port !== null,
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
  // Authorized but not yet redeemed by the terminal: whoever authorized it may
  // approve again (the redirect failed, or the tab was reloaded).
  if (row.status === "authorized" && row.user_id !== user.id) return { state: "used" };
  if (row.status === "denied") return { state: "denied" };
  if (!row.live) return { state: "expired" };
  return null;
}

/**
 * Approve or deny a pending code for `user` in one transaction (row lock on the
 * code). Approving a callback login returns the redirect to the terminal instead
 * of creating the token here.
 */
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
      if (row.callback_port !== null) {
        // The token is created when the terminal redeems this code, which only
        // travels through the redirect to 127.0.0.1 on the approver's machine.
        // A login link sent by someone else is useless to them.
        const callbackCode = randomBytes(32).toString("base64url");
        await tx`
          update device_codes set status = 'authorized', user_id = ${user.id}, callback_code_hash = ${sha256(callbackCode)}
          where id = ${row.id}`;
        const url = new URL(`http://127.0.0.1:${row.callback_port}/callback`);
        url.searchParams.set("state", row.callback_state ?? "");
        url.searchParams.set("code", callbackCode);
        return { state: "redirect", url: url.toString() } as const;
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

