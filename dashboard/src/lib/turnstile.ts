import "server-only";
import { headers } from "next/headers";
import { getSettings } from "./settings";

/** Site key for the widget when both Turnstile keys are configured, else null. */
export async function turnstileSiteKey(): Promise<string | null> {
  const s = await getSettings();
  return s.turnstile_site_key && s.turnstile_secret_key ? s.turnstile_site_key : null;
}

/** Verifies the widget's token from a form; passes when Turnstile isn't configured. */
export async function verifyTurnstile(fd: FormData): Promise<string | null> {
  const s = await getSettings();
  if (!s.turnstile_site_key || !s.turnstile_secret_key) return null;
  const token = String(fd.get("cf-turnstile-response") ?? "");
  if (!token) return "Complete the “I'm human” check.";
  const h = await headers();
  const ip = (h.get("x-forwarded-for") ?? "").split(",")[0].trim();
  try {
    const res = await fetch("https://challenges.cloudflare.com/turnstile/v0/siteverify", {
      method: "POST",
      body: new URLSearchParams({ secret: s.turnstile_secret_key, response: token, ...(ip ? { remoteip: ip } : {}) }),
      signal: AbortSignal.timeout(8000),
    });
    const data = (await res.json()) as { success?: boolean };
    return data.success ? null : "The “I'm human” check failed. Try again.";
  } catch {
    return "Couldn't verify the “I'm human” check. Try again in a moment.";
  }
}
