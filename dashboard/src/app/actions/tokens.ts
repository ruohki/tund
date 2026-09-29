"use server";

import { randomBytes } from "node:crypto";
import { refresh } from "next/cache";
import { requireUser, sha256 } from "@/lib/auth";
import { unverifiedError } from "@/lib/mail";
import { db, notify } from "@/lib/db";
import { isUuid } from "@/lib/requests";

export type CreateTokenState = { error?: string; token?: string; name?: string } | null;

export async function createTokenAction(_: CreateTokenState, fd: FormData): Promise<CreateTokenState> {
  const user = await requireUser();
  const blocked = await unverifiedError(user);
  if (blocked) return { error: blocked };
  const name = String(fd.get("name") ?? "").trim().slice(0, 80) || "Unnamed token";
  const token = `tund_${randomBytes(20).toString("hex")}`;
  await db()`
    insert into authtokens (user_id, name, token_hash, token_prefix)
    values (${user.id}, ${name}, ${sha256(token)}, ${token.slice(0, 13)})`;
  refresh();
  return { token, name };
}

export async function revokeTokenAction(fd: FormData) {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (!isUuid(id)) return;
  const res = await db()`delete from authtokens where id = ${id} and user_id = ${user.id}`;
  if (res.count) await notify("tund_config", { kind: "authtoken_revoked", id });
  refresh();
}
