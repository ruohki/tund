import "server-only";
import { headers } from "next/headers";
import { db } from "./db";

/** id is null for an account that no longer exists (it deleted itself). */
type Actor = { id: string | null; email: string } | null;

/**
 * Appends to the audit log (docs/SPEC.md "Audit log"). `details` must never
 * contain secrets. Failures are logged, not thrown: auditing must not break the
 * action it describes.
 */
export async function audit(actor: Actor, action: string, target = "", details: Record<string, unknown> = {}) {
  try {
    const h = await headers();
    const ip = (h.get("x-forwarded-for") ?? "").split(",")[0].trim() || h.get("x-real-ip") || "";
    await db()`
      insert into audit_log (actor_id, actor_email, action, target, details, ip)
      values (${actor?.id ?? null}, ${actor?.email ?? ""}, ${action}, ${target}, ${db().json(details as never)}, ${ip})`;
  } catch (err) {
    console.error("tund: audit log write failed", action, err);
  }
}

/** For background jobs, which have no request: the actor is "system". */
export async function systemAudit(action: string, target = "", details: Record<string, unknown> = {}) {
  try {
    await db()`
      insert into audit_log (actor_email, action, target, details)
      values ('system', ${action}, ${target}, ${db().json(details as never)})`;
  } catch (err) {
    console.error("tund: audit log write failed", action, err);
  }
}
