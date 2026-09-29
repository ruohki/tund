// Session cookie naming, shared by proxy.ts and lib/auth.ts.
//
// Tunnels are subdomains of the dashboard's domain, so a tunnel could plant a
// cookie with Domain=<parent> ("cookie tossing"). Over HTTPS the cookie uses
// the __Host- prefix, which browsers only accept when set by this exact host
// with Secure and Path=/ and no Domain. Plain-HTTP dev keeps the bare name,
// so readers must still cope with several cookies of the same name.

export function sessionCookieSecure(): boolean {
  return process.env.TUND_PUBLIC_SCHEME !== "http";
}

export function sessionCookieName(): string {
  return sessionCookieSecure() ? "__Host-tund_session" : "tund_session";
}

/** Every distinct value of cookie `name` in a raw Cookie header, in header order. */
export function cookieValues(header: string | null | undefined, name: string): string[] {
  if (!header) return [];
  const out: string[] = [];
  for (const part of header.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0 || part.slice(0, eq).trim() !== name) continue;
    let value = part.slice(eq + 1).trim();
    if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) value = value.slice(1, -1);
    try {
      value = decodeURIComponent(value);
    } catch {
      /* keep the raw value */
    }
    if (value && !out.includes(value)) out.push(value);
    if (out.length >= 8) break;
  }
  return out;
}
