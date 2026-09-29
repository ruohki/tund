import { NextResponse, type NextRequest } from "next/server";
import { cookieValues, sessionCookieName, sessionCookieSecure } from "@/lib/session-cookie";

const PUBLIC_PAGES = new Set(["/login", "/setup", "/signup", "/forgot-password"]);
// Links from emails and invites work without a session.
const PUBLIC_PREFIXES = ["/invite/", "/reset-password/", "/verify-email/"];

// Optimistic check only: the real session validation happens in requireUser().
export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  const cookieName = sessionCookieName();
  const tokens = cookieValues(request.headers.get("cookie"), cookieName);
  const token = tokens.length > 0;

  // "/" decides for itself: overview when signed in, otherwise the public
  // landing page (sign-up enabled) or a redirect to /login.
  const isPublic = PUBLIC_PAGES.has(pathname) || PUBLIC_PREFIXES.some((p) => pathname.startsWith(p));
  if (!token && pathname !== "/" && !isPublic && !pathname.startsWith("/api/")) {
    const url = request.nextUrl.clone();
    url.pathname = "/login";
    url.search = `?next=${encodeURIComponent(pathname + search)}`;
    return NextResponse.redirect(url);
  }

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-tund-path", pathname + search);
  const response = NextResponse.next({ request: { headers: requestHeaders } });

  // Sliding session: keep the cookie alive while the dashboard is in use. Only
  // when there is exactly one candidate, so a planted duplicate is never copied
  // into our own cookie.
  if (tokens.length === 1 && request.method === "GET" && !pathname.startsWith("/api/")) {
    response.cookies.set(cookieName, tokens[0], {
      httpOnly: true,
      secure: sessionCookieSecure(),
      sameSite: "lax",
      path: "/",
      maxAge: 30 * 24 * 60 * 60,
    });
  }
  return response;
}

export const config = {
  // Static assets and metadata files (icons, OG images, robots, sitemap, manifest) are public.
  matcher: [
    "/((?!_next/static|_next/image|favicon.ico|icon.svg|apple-icon|opengraph-image|twitter-image|robots.txt|sitemap.xml|manifest.webmanifest).*)",
  ],
};
