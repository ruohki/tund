"use client";

import { useEffect, useState, useTransition, type ReactNode } from "react";
import Link from "next/link";
import { CheckCircle2, CircleSlash, Clock3, Loader2, TerminalSquare, TriangleAlert } from "lucide-react";
import { approveDeviceAction, denyDeviceAction } from "@/app/actions/device";
import { logoutAction } from "@/app/actions/auth";
import { formatUserCodeInput, normalizeUserCode, type DeviceOutcome, type DeviceRequestView } from "@/lib/device";
import { formatClock, osLabel } from "@/lib/format";
import { useDisplayTimeZone, useHydrated } from "@/lib/use-hydrated";
import { buttonClass, cn } from "@/components/ui";
import { Command } from "@/components/client-ui";

function Frame({ children }: { children: ReactNode }) {
  return <div className="mx-auto w-full max-w-xl py-4 sm:py-10">{children}</div>;
}

function Heading({ icon, title, children }: { icon: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="mb-6">
      <div className="mb-4 text-ink-2">{icon}</div>
      <h1 className="text-[26px] font-semibold leading-tight tracking-[-0.015em] text-ink">{title}</h1>
      {children ? <div className="mt-2 text-[14.5px] text-ink-2">{children}</div> : null}
    </div>
  );
}

const RETRY = <Command className="mt-4 max-w-sm">tund login</Command>;

/** Enter a code by hand (or fix a mistyped one). Submits as GET so the URL stays shareable. */
function CodeEntry({ initial, error }: { initial: string; error?: string }) {
  const [value, setValue] = useState(() => formatUserCodeInput(initial));
  const valid = normalizeUserCode(value) !== null;
  return (
    <Frame>
      <Heading icon={<TerminalSquare size={26} strokeWidth={1.5} />} title="Log in your terminal">
        Enter the code shown by <code className="font-mono text-[13px] text-ink">tund login</code> or{" "}
        <code className="font-mono text-[13px] text-ink">tund http</code>.
      </Heading>
      <form method="get" action="/device" className="flex flex-col gap-3">
        <label htmlFor="code" className="text-[13px] font-medium text-ink">
          Code
        </label>
        <input
          id="code"
          name="code"
          value={value}
          onChange={(e) => setValue(formatUserCodeInput(e.target.value))}
          placeholder="WDJB-MJHT"
          autoFocus
          autoComplete="one-time-code"
          autoCapitalize="characters"
          spellCheck={false}
          inputMode="text"
          maxLength={9}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={error ? "code-error" : undefined}
          className={cn(
            "h-14 w-full max-w-xs rounded-md border bg-surface px-4 text-center font-mono text-[26px] tracking-[0.18em] text-ink uppercase",
            "placeholder:text-line-strong focus:outline-none focus:ring-2 focus:ring-focus/20",
            error ? "border-danger/60 focus:border-danger" : "border-line-strong focus:border-focus",
          )}
        />
        {error ? (
          <p id="code-error" role="alert" className="max-w-md text-[13px] text-danger">
            {error}
          </p>
        ) : null}
        <div>
          <button type="submit" disabled={!valid} className={buttonClass("primary")}>
            Continue
          </button>
        </div>
      </form>
    </Frame>
  );
}

/** Minutes:seconds until the code expires; only rendered after hydration (it depends on the clock). */
function useRemaining(expiresAt: string): number | null {
  const hydrated = useHydrated();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  if (!hydrated) return null;
  return Math.max(0, Math.floor((Date.parse(expiresAt) - now) / 1000));
}

function Pending({
  request,
  email,
  onDecide,
  busy,
  error,
  blocked,
}: {
  request: DeviceRequestView;
  email: string;
  onDecide: (approve: boolean) => void;
  busy: "approve" | "deny" | null;
  error: string | null;
  /** Set while the account still has to confirm its email address. */
  blocked: string | null;
}) {
  const tz = useDisplayTimeZone();
  const remaining = useRemaining(request.expiresAt);
  const expired = remaining === 0;
  const rows: [string, ReactNode][] = [
    ["Computer", request.clientHostname || "unknown"],
    ["System", request.clientOs ? osLabel(request.clientOs) : "unknown"],
    ["IP address", <span key="ip" className="font-mono text-[12.5px]">{request.clientIp || "unknown"}</span>],
    ["Requested", formatClock(request.createdAt, tz)],
  ];
  const expiry = (
    <p className="flex items-center gap-1.5 text-[12.5px] text-muted" aria-live="polite">
      <Clock3 size={13} />
      {remaining === null
        ? "Valid for 10 minutes"
        : expired
          ? "Expired"
          : `Expires in ${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, "0")}`}
    </p>
  );
  return (
    <Frame>
      <Heading icon={<TerminalSquare size={26} strokeWidth={1.5} />} title="Log in your terminal?">
        A terminal asked to use your account <span className="font-medium text-ink">{email}</span>.
      </Heading>

      <div className="rounded-lg border border-line bg-surface">
        {request.callback ? null : (
          <div className="flex flex-wrap items-end justify-between gap-4 border-b border-line px-5 py-4">
            <div>
              <p className="text-[12.5px] text-muted">Code</p>
              <p className="font-mono text-[32px] font-medium leading-tight tracking-[0.14em] text-ink">{request.userCode}</p>
            </div>
            {expiry}
          </div>
        )}
        <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 px-5 py-4 text-[13.5px]">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted">{k}</dt>
              <dd className="truncate text-ink">{v}</dd>
            </div>
          ))}
        </dl>
        {request.callback ? <div className="border-t border-line px-5 py-3">{expiry}</div> : null}
      </div>

      <p className="mt-5 flex items-start gap-2.5 rounded-md border border-sodium/60 bg-sodium-wash px-3.5 py-3 text-[13.5px] text-ink">
        <TriangleAlert size={16} className="mt-0.5 shrink-0 text-sodium-ink" />
        {request.callback ? (
          <span>
            <span className="font-semibold">Only approve if you just ran tund on this computer.</span> Approving gives
            that terminal access to your tunnels; this tab then hands the login back to it.
          </span>
        ) : (
          <span>
            <span className="font-semibold">Only approve if this code matches the one in your terminal.</span> If you
            didn&apos;t just run tund, or the code is different, deny the request. Approving gives that computer access
            to your tunnels.
          </span>
        )}
      </p>

      {blocked ? (
        <p role="alert" className="mt-4 rounded-md border border-danger/30 bg-danger-wash px-3.5 py-2.5 text-[13px] text-danger">
          {blocked}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="mt-4 text-[13px] text-danger">
          {error}
        </p>
      ) : null}

      <div className="mt-6 flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={busy !== null || expired || Boolean(blocked)}
          onClick={() => onDecide(true)}
          className={buttonClass("primary")}
        >
          {busy === "approve" ? <Loader2 size={14} className="animate-spin" /> : null}
          Approve
        </button>
        <button
          type="button"
          disabled={busy !== null || expired}
          onClick={() => onDecide(false)}
          className={buttonClass("danger")}
        >
          {busy === "deny" ? <Loader2 size={14} className="animate-spin" /> : null}
          Deny
        </button>
      </div>

      <form action={logoutAction} className="mt-8 border-t border-line pt-4 text-[12.5px] text-muted">
        <input type="hidden" name="next" value={`/device?code=${request.userCode}`} />
        Not {email}?{" "}
        <button type="submit" className="font-medium text-ink underline underline-offset-4">
          Sign in with another account
        </button>
      </form>
    </Frame>
  );
}

/** Also the landing page after a callback login (/device/done). */
export function DeviceApproved({ email }: { email: string }) {
  return (
    <Frame>
      <Heading icon={<CheckCircle2 size={28} strokeWidth={1.5} className="text-ok" />} title="Terminal logged in">
        Your terminal is now logged in as <span className="font-medium text-ink">{email}</span>. You can close this tab
        and go back to it.
      </Heading>
      <p className="text-[13.5px] text-ink-2">
        The tunnel starts in the terminal on its own. Its requests show up under{" "}
        <Link href="/inspect" className="font-medium text-ink underline underline-offset-4">
          Inspect
        </Link>
        , and the new token is listed under{" "}
        <Link href="/authtokens" className="font-medium text-ink underline underline-offset-4">
          Auth tokens
        </Link>
        .
      </p>
    </Frame>
  );
}

function Outcome({ outcome, input }: { outcome: DeviceOutcome; input: string }) {
  switch (outcome.state) {
    case "approved":
      return <DeviceApproved email={outcome.email} />;
    case "denied":
      return (
        <Frame>
          <Heading icon={<CircleSlash size={28} strokeWidth={1.5} className="text-danger" />} title="Request denied">
            The terminal was not logged in. If that was a mistake, start again for a new code:
          </Heading>
          {RETRY}
        </Frame>
      );
    case "expired":
      return (
        <Frame>
          <Heading icon={<Clock3 size={28} strokeWidth={1.5} />} title="This code has expired">
            Codes are valid for 10 minutes. Run the command again in your terminal for a new one:
          </Heading>
          {RETRY}
        </Frame>
      );
    case "used":
      return (
        <Frame>
          <Heading icon={<CircleSlash size={28} strokeWidth={1.5} />} title="This code was already used">
            Another account approved it. Each code works once; run the command again for a new one:
          </Heading>
          {RETRY}
        </Frame>
      );
    case "unknown":
      return (
        <CodeEntry
          initial={input}
          error="No pending login uses this code. Check it against your terminal; if the terminal already finished logging in, the code is gone."
        />
      );
    case "error":
      return <CodeEntry initial={input} error={outcome.message} />;
    case "pending":
    case "redirect":
      return null;
  }
}

export function DeviceFlow({
  email,
  input,
  code,
  initial,
  blocked = null,
}: {
  email: string;
  input: string;
  code: string | null;
  initial: DeviceOutcome | null;
  blocked?: string | null;
}) {
  const [outcome, setOutcome] = useState<DeviceOutcome | null>(initial);
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [, startTransition] = useTransition();

  if (!input) return <CodeEntry initial="" />;
  if (!code) return <CodeEntry initial={input} error="That isn't a valid code. Codes have eight letters, like WDJB-MJHT." />;
  if (!outcome) return <CodeEntry initial={input} />;

  if (outcome.state === "pending") {
    const decide = (approve: boolean) => {
      setBusy(approve ? "approve" : "deny");
      setError(null);
      startTransition(async () => {
        const res = await (approve ? approveDeviceAction : denyDeviceAction)(code);
        // Hand the login to the terminal's listener; it sends the browser on
        // to /device/done. The button keeps spinning until the page changes.
        if (res.state === "redirect") return window.location.assign(res.url);
        setBusy(null);
        if (res.state === "error") setError(res.message);
        else setOutcome(res);
      });
    };
    return <Pending request={outcome.request} email={email} onDecide={decide} busy={busy} error={error} blocked={blocked} />;
  }
  return <Outcome outcome={outcome} input={input} />;
}
