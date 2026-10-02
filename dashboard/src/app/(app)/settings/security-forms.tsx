"use client";

import { useActionState, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Download, KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { browserSupportsWebAuthn, startRegistration } from "@simplewebauthn/browser";
import {
  confirmTotpSetupAction,
  disableTotpAction,
  regenerateRecoveryCodesAction,
  startTotpSetupAction,
  type TotpSetup,
} from "@/app/actions/two-factor";
import { buttonClass, Field, FormMessage, Input } from "@/components/ui";
import { CopyButton, SubmitButton } from "@/components/client-ui";
import { passkeyErrorText } from "@/components/passkey-button";
import { useHydrated } from "@/lib/use-hydrated";

/** New recovery codes, shown once, with copy and download. */
function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const text = codes.join("\n");
  const download = () => {
    const url = URL.createObjectURL(new Blob([`${text}\n`], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[13px] text-ink-2">
        Save these recovery codes somewhere safe, like your password manager. Each one signs you in once if you lose your
        phone. They won&apos;t be shown again.
      </p>
      <ul className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-md border border-line bg-surface-2 px-4 py-3 font-mono text-[13px] text-ink">
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className="flex flex-wrap gap-2">
        <CopyButton value={text} label="Copy" variant="secondary" />
        <button type="button" onClick={download} className={buttonClass("secondary", "sm")}>
          <Download size={13} />
          Download
        </button>
        <button type="button" onClick={onDone} className={buttonClass("primary", "sm")}>
          I saved them
        </button>
      </div>
    </div>
  );
}

function CodeField({ id }: { id: string }) {
  return (
    <Input
      id={id}
      name="code"
      autoComplete="one-time-code"
      inputMode="numeric"
      required
      maxLength={20}
      placeholder="123456"
      className="w-36 font-mono tracking-[0.12em]"
    />
  );
}

function SetupTotp({ setup, onCancel, onDone }: { setup: TotpSetup; onCancel: () => void; onDone: () => void }) {
  const [state, action] = useActionState(confirmTotpSetupAction, null);
  if (state?.codes) return <RecoveryCodes codes={state.codes} onDone={onDone} />;
  if (!setup.ok) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-[13px] text-danger">{setup.error}</p>
        <div>
          <button type="button" onClick={onCancel} className={buttonClass("secondary", "sm")}>
            Back
          </button>
        </div>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <ol className="list-decimal space-y-1 pl-5 text-[13px] text-ink-2">
        <li>Scan the QR code with an authenticator app (1Password, Google Authenticator, Authy, …).</li>
        <li>Enter the 6-digit code it shows.</li>
      </ol>
      <div className="flex flex-wrap items-center gap-4">
        {/* eslint-disable-next-line @next/next/no-img-element -- a data: URL generated on the server */}
        <img src={setup.qr} alt="QR code for your authenticator app" width={168} height={168} className="rounded-md border border-line bg-white p-1" />
        <div className="min-w-0 text-[12.5px] text-ink-2">
          <p>Can&apos;t scan it? Enter this key:</p>
          <p className="mt-1 flex items-center gap-1 font-mono text-[13px] text-ink">
            <span className="break-all">{setup.secret}</span>
            <CopyButton value={setup.secret.replace(/\s/g, "")} />
          </p>
        </div>
      </div>
      <form action={action} className="flex flex-col gap-3">
        <Field label="Code from the app" htmlFor="totp-setup-code">
          <CodeField id="totp-setup-code" />
        </Field>
        <FormMessage state={state} />
        <div className="flex gap-2">
          <SubmitButton pendingText="Checking…">Turn on</SubmitButton>
          <button type="button" onClick={onCancel} className={buttonClass("ghost", "md")}>
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}

function TotpOn({ enabledAt, recoveryLeft }: { enabledAt: string | null; recoveryLeft: number }) {
  const [mode, setMode] = useState<"idle" | "codes" | "off">("idle");
  const [codesState, codesAction] = useActionState(regenerateRecoveryCodesAction, null);
  const [offState, offAction] = useActionState(disableTotpAction, null);
  const [seen, setSeen] = useState<string[] | null>(null);
  const hydrated = useHydrated();

  if (codesState?.codes && codesState.codes !== seen) {
    const codes = codesState.codes;
    return (
      <RecoveryCodes
        codes={codes}
        onDone={() => {
          setSeen(codes);
          setMode("idle");
        }}
      />
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[13px] text-ink-2">
        <span className="font-medium text-ok">On</span>
        {enabledAt && hydrated ? ` since ${new Date(enabledAt).toLocaleDateString()}` : ""}. Signing in asks for a code from your
        authenticator app, a passkey or a recovery code. {recoveryLeft} of 10 recovery codes left.
      </p>
      {mode === "idle" ? (
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={() => setMode("codes")} className={buttonClass("secondary", "sm")}>
            New recovery codes
          </button>
          <button type="button" onClick={() => setMode("off")} className={buttonClass("danger", "sm")}>
            Turn off
          </button>
        </div>
      ) : (
        <form action={mode === "codes" ? codesAction : offAction} className="flex flex-col gap-3">
          <Field
            label={mode === "codes" ? "Code from your authenticator app" : "Code from your authenticator app or a recovery code"}
            htmlFor="totp-confirm-code"
            hint={mode === "codes" ? "Your current recovery codes stop working." : undefined}
          >
            <CodeField id="totp-confirm-code" />
          </Field>
          <FormMessage state={mode === "codes" ? codesState : offState} />
          <div className="flex gap-2">
            <SubmitButton variant={mode === "off" ? "danger" : "primary"} pendingText="Checking…">
              {mode === "codes" ? "Get new codes" : "Turn off two-factor"}
            </SubmitButton>
            <button type="button" onClick={() => setMode("idle")} className={buttonClass("ghost", "md")}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

export function TwoFactorSettings({ on, enabledAt, recoveryLeft }: { on: boolean; enabledAt: string | null; recoveryLeft: number }) {
  const router = useRouter();
  // Kept here, above the on/off switch: confirming the setup re-renders the
  // page as "on" while the new recovery codes still have to be shown.
  const [setup, setSetup] = useState<TotpSetup | null>(null);
  const [pending, start] = useTransition();
  if (setup) {
    return (
      <SetupTotp
        setup={setup}
        onCancel={() => setSetup(null)}
        onDone={() => {
          setSetup(null);
          router.refresh();
        }}
      />
    );
  }
  if (on) return <TotpOn enabledAt={enabledAt} recoveryLeft={recoveryLeft} />;
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[13px] text-ink-2">
        <span className="font-medium text-ink">Off.</span> Turn it on to also ask for a code from an authenticator app after your
        password (or after signing in with Google or GitHub).
      </p>
      <div>
        <button
          type="button"
          disabled={pending}
          onClick={() => start(async () => setSetup(await startTotpSetupAction()))}
          className={buttonClass("primary", "sm")}
        >
          {pending ? <Loader2 size={13} className="animate-spin" /> : <ShieldCheck size={13} />}
          Turn on
        </button>
      </div>
    </div>
  );
}

/** Registers a new passkey for the signed-in account. */
export function AddPasskey() {
  const router = useRouter();
  const hydrated = useHydrated();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (hydrated && !browserSupportsWebAuthn()) {
    return <p className="text-[13px] text-muted">This browser doesn&apos;t support passkeys.</p>;
  }
  const post = async (body: unknown) => {
    const res = await fetch("/api/auth/passkey/register", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
    return data;
  };
  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const optionsJSON = await post({ step: "options" });
      const response = await startRegistration({ optionsJSON });
      await post({ step: "verify", response, name });
      setName("");
      router.refresh();
    } catch (err) {
      setError(passkeyErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={add} className="flex flex-col gap-2">
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Name" htmlFor="passkey-name" className="min-w-48 flex-1">
          <Input id="passkey-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="e.g. MacBook Touch ID" />
        </Field>
        <button type="submit" disabled={busy} className={buttonClass("primary", "md")}>
          {busy ? <Loader2 size={14} className="animate-spin" /> : <KeyRound size={14} />}
          Add a passkey
        </button>
      </div>
      {error ? (
        <p role="alert" className="text-[13px] text-danger">
          {error}
        </p>
      ) : null}
    </form>
  );
}
