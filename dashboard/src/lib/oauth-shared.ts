// Sign-in with Google / GitHub: what pages and client components may import
// (the flow itself is in lib/oauth.ts).

export type OAuthProviderId = "google" | "github";

export const OAUTH_PROVIDERS: OAuthProviderId[] = ["google", "github"];

export const PROVIDER_LABEL: Record<OAuthProviderId, string> = { google: "Google", github: "GitHub" };

export function isOAuthProvider(p: unknown): p is OAuthProviderId {
  return p === "google" || p === "github";
}

/**
 * Why a sign-in didn't work, by the code the callback puts in ?error= (or
 * ?oauth_error= on /settings). Fixed texts only: the query string is not a
 * place to take messages from.
 */
export const OAUTH_ERRORS: Record<string, string> = {
  state: "That sign-in took too long or was started in another tab. Try again.",
  cancelled: "Sign-in was cancelled.",
  failed: "Signing in with the provider didn't work. Try again in a moment.",
  unavailable: "That sign-in option isn't available on this server.",
  no_email: "Your account there has no verified email address. Verify one with the provider first, or use a password.",
  disabled: "This account has been disabled. Contact an administrator of this server.",
  signup_closed: "There's no account for that email, and sign-up is closed on this server. Ask an administrator for an account.",
  signup_invite:
    "There's no account for that email. Accounts on this server are created through team invites; ask a team owner for an invite link.",
  invite_email: "This invite is for a different email address than your account there.",
  disposable: "Disposable email addresses can't be used here. Use an account with an address you'll keep.",
  rate: "Too many accounts were created from your network in the last hour. Try again later.",
  taken: "That account is already connected to another user on this server.",
  last_method: "Set a password or connect another account first, so you can still sign in.",
};

export function oauthError(code: unknown): string | null {
  return typeof code === "string" && Object.hasOwn(OAUTH_ERRORS, code) ? OAUTH_ERRORS[code] : null;
}
