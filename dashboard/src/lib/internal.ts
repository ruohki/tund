import "server-only";
import { config } from "./config";

export class InternalApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}

/** Calls the tund-server internal API (see docs/SPEC.md "Internal API"). */
export async function internalApi<T = Record<string, unknown>>(path: string, body: unknown): Promise<T> {
  const c = config();
  let res: Response;
  try {
    res = await fetch(`${c.internalUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${c.internalSecret}` },
      body: JSON.stringify(body),
      cache: "no-store",
      signal: AbortSignal.timeout(30_000),
    });
  } catch (err) {
    throw new InternalApiError(
      `Can't reach tund-server at ${c.internalUrl} (${err instanceof Error ? err.message : String(err)}). Check TUND_INTERNAL_URL.`,
      503,
    );
  }
  const text = await res.text();
  let data: Record<string, unknown> = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    /* non-JSON error body */
  }
  if (!res.ok) {
    const msg = typeof data.error === "string" ? data.error : text.slice(0, 200) || res.statusText;
    if (res.status === 401) throw new InternalApiError("tund-server rejected the internal secret. TUND_INTERNAL_SECRET must match on both sides.", 401);
    if (typeof data.error !== "string") {
      // Not an answer from tund-server's internal API.
      throw new InternalApiError(
        `${c.internalUrl}${path} answered HTTP ${res.status}, which doesn't look like tund-server. Check that TUND_INTERNAL_URL points at the server's internal port.`,
        502,
      );
    }
    throw new InternalApiError(msg, res.status);
  }
  return data as T;
}

/** GET on the internal API (e.g. /internal/status). */
export async function internalGet<T = Record<string, unknown>>(path: string): Promise<T> {
  const c = config();
  let res: Response;
  try {
    res = await fetch(`${c.internalUrl}${path}`, {
      headers: { Authorization: `Bearer ${c.internalSecret}` },
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
  } catch (err) {
    throw new InternalApiError(
      `Can't reach tund-server at ${c.internalUrl} (${err instanceof Error ? err.message : String(err)}).`,
      503,
    );
  }
  const text = await res.text();
  let data: Record<string, unknown> = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    /* non-JSON body */
  }
  if (!res.ok) throw new InternalApiError(typeof data.error === "string" ? data.error : `HTTP ${res.status}`, res.status);
  return data as T;
}
