// Device authorization for `tund login` (docs/SPEC.md "Hosted service mode").

export const USER_CODE_ALPHABET = "BCDFGHJKLMNPQRSTVWXZ";

/**
 * Normalizes what a person typed ("wdjb mjht", "WDJBMJHT", "wdjb-mjht") to the
 * canonical "WDJB-MJHT", or null when it can't be a valid code.
 */
export function normalizeUserCode(input: unknown): string | null {
  if (typeof input !== "string") return null;
  const letters = input.toUpperCase().replace(/[^A-Z]/g, "");
  if (letters.length !== 8) return null;
  for (const ch of letters) if (!USER_CODE_ALPHABET.includes(ch)) return null;
  return `${letters.slice(0, 4)}-${letters.slice(4)}`;
}

/** Formats partial input while typing: uppercase, allowed letters only, dash after four. */
export function formatUserCodeInput(input: string): string {
  const letters = input
    .toUpperCase()
    .replace(/[^A-Z]/g, "")
    .split("")
    .filter((c) => USER_CODE_ALPHABET.includes(c))
    .join("")
    .slice(0, 8);
  return letters.length > 4 ? `${letters.slice(0, 4)}-${letters.slice(4)}` : letters;
}

export type DeviceRequestView = {
  userCode: string;
  clientHostname: string;
  clientOs: string;
  clientIp: string;
  createdAt: string;
  expiresAt: string;
};

/** Outcome of looking up or acting on a code. */
export type DeviceOutcome =
  | { state: "pending"; request: DeviceRequestView }
  | { state: "approved"; email: string }
  | { state: "denied" }
  | { state: "used" }
  | { state: "expired" }
  | { state: "unknown" }
  | { state: "error"; message: string };
