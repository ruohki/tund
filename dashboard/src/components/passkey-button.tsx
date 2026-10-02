"use client";

import { useEffect, useState } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { browserSupportsWebAuthn, startAuthentication, WebAuthnError } from "@simplewebauthn/browser";
import { buttonClass, cn } from "./ui";

/** A browser error from WebAuthn, in words. */
export function passkeyErrorText(err: unknown): string {
  if (err instanceof WebAuthnError) {
    if (err.code === "ERROR_CEREMONY_ABORTED") return "Cancelled.";
    if (err.code === "ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED") return "This passkey is already registered.";
  }
  if (err instanceof Error) {
    if (err.name === "NotAllowedError") return "Cancelled or timed out. Try again.";
    if (err.name === "SecurityError") return "Passkeys need the dashboard's own HTTPS address.";
    return err.message;
  }
  return String(err);
}

async function post<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const data = (await res.json().catch(() => ({}))) as T & { error?: string };
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data;
}

/**
 * Signs in with a passkey: on its own from the login page, or as the second
 * step after the password. Hidden in browsers without WebAuthn.
 */
export function PasskeyButton({
  mode,
  next,
  label = "Sign in with a passkey",
  className,
}: {
  mode: "login" | "second_factor";
  next?: string;
  label?: string;
  className?: string;
}) {
  const [supported, setSupported] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => setSupported(browserSupportsWebAuthn()), []);
  if (!supported) return null;

  const run = async () => {
    setBusy(true);
    setError(null);
    try {
      const optionsJSON = await post<Parameters<typeof startAuthentication>[0]["optionsJSON"]>("/api/auth/passkey/login", {
        step: "options",
        mode,
        next,
      });
      const response = await startAuthentication({ optionsJSON });
      const done = await post<{ redirect: string }>("/api/auth/passkey/login", { step: "verify", response });
      window.location.assign(done.redirect || "/");
    } catch (err) {
      setError(passkeyErrorText(err));
      setBusy(false);
    }
  };

  return (
    <div className={className}>
      <button type="button" onClick={run} disabled={busy} className={cn(buttonClass("secondary", "md"), "w-full")}>
        {busy ? <Loader2 size={15} className="animate-spin" /> : <KeyRound size={15} />}
        {label}
      </button>
      {error ? (
        <p role="alert" className="mt-2 text-[13px] text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
