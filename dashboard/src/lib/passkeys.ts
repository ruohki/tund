import "server-only";
import type { NextRequest } from "next/server";
import { config } from "./config";
import { db } from "./db";

// Shared bits of the passkey route handlers (app/api/auth/passkey/*).

export type StoredPasskey = {
  id: string;
  userId: string;
  credentialId: string;
  publicKey: Uint8Array<ArrayBuffer>;
  counter: number;
  transports: string[];
};

export async function passkeyByCredential(credentialId: string): Promise<StoredPasskey | null> {
  const [row] = await db()`
    select id, user_id, credential_id, public_key, counter, transports from user_passkeys where credential_id = ${credentialId}`;
  if (!row) return null;
  const key = row.public_key as Buffer;
  return {
    id: row.id as string,
    userId: row.user_id as string,
    credentialId: row.credential_id as string,
    publicKey: new Uint8Array(key.buffer.slice(key.byteOffset, key.byteOffset + key.byteLength)) as Uint8Array<ArrayBuffer>,
    counter: Number(row.counter),
    transports: (row.transports as string[]) ?? [],
  };
}

export async function userPasskeys(userId: string): Promise<{ id: string; transports: string[] }[]> {
  const rows = await db()`select credential_id, transports from user_passkeys where user_id = ${userId}`;
  return rows.map((r) => ({ id: r.credential_id as string, transports: (r.transports as string[]) ?? [] }));
}

/** JSON body of a passkey request, or null for a cross-origin or malformed call. */
export async function passkeyRequest<T>(req: NextRequest): Promise<T | null> {
  // Cookies are SameSite=Lax already; insist on our own origin as well.
  const origin = req.headers.get("origin");
  if (origin && origin !== config().dashboardUrl) return null;
  try {
    return (await req.json()) as T;
  } catch {
    return null;
  }
}

export const passkeyError = (error: string, status = 400) => Response.json({ error }, { status });
