"use client";

import Link from "next/link";
import { useState } from "react";
import { Command, useDetectedOs } from "./client-ui";
import { cn } from "./ui";

type Os = "mac" | "linux" | "windows";

export function installCommand(os: Os, dashboardUrl: string) {
  return os === "windows"
    ? `irm ${dashboardUrl}/install.ps1 | iex`
    : `curl -fsSL ${dashboardUrl}/install.sh | sh`;
}

export function OsSwitch({ value, onChange }: { value: Os; onChange: (o: Os) => void }) {
  const opts: { id: Os; label: string }[] = [
    { id: "mac", label: "macOS" },
    { id: "linux", label: "Linux" },
    { id: "windows", label: "Windows" },
  ];
  return (
    <div role="radiogroup" aria-label="Operating system" className="inline-flex rounded-[5px] border border-line p-0.5">
      {opts.map((o) => (
        <button
          key={o.id}
          type="button"
          role="radio"
          aria-checked={value === o.id}
          onClick={() => onChange(o.id)}
          className={cn(
            "h-6 rounded-[3px] px-2.5 text-[12.5px] transition-colors",
            value === o.id ? "bg-surface-3 font-medium text-ink" : "text-muted hover:text-ink",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/**
 * Two steps: install, then start a tunnel (its first run logs in through the
 * browser). Auth tokens are the alternative for CI and headless machines.
 */
export function InstallSteps({
  dashboardUrl,
  staticUrl,
  showManual = true,
}: {
  dashboardUrl: string;
  /** The account's default static hostname URL, when it has one. */
  staticUrl?: string | null;
  showManual?: boolean;
}) {
  const detected = useDetectedOs();
  const [chosen, setChosen] = useState<Os | null>(null);
  const os = chosen ?? detected ?? "mac";
  const prompt = os === "windows" ? ">" : "$";
  const host = dashboardUrl.replace(/^https?:\/\//, "");

  const steps = [
    {
      title: "Install the client",
      body: (
        <>
          <Command prompt={prompt}>{installCommand(os, dashboardUrl)}</Command>
          <p className="mt-1.5 text-[12.5px] text-muted">
            A single binary for macOS, Linux and Windows. No admin rights or network drivers needed.
          </p>
        </>
      ),
    },
    {
      title: "Expose a local port",
      body: (
        <>
          <Command prompt={prompt}>tund http 3000</Command>
          <p className="mt-1.5 text-[12.5px] text-muted">
            The first run opens your browser to log the terminal in to {host}. After that,{" "}
            <code className="font-mono text-ink-2">tund http 3000</code> gives you the same URL every time
            {staticUrl ? (
              <>
                : <span className="font-mono text-ink">{staticUrl}</span>
              </>
            ) : null}
            . Requests show up under Inspect as they happen.
          </p>
        </>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4">
        <OsSwitch value={os} onChange={setChosen} />
      </div>
      <ol className="flex flex-col gap-5">
        {steps.map((s, i) => (
          <li key={s.title} className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3">
            <span className="mt-0.5 grid h-6 w-6 place-items-center rounded-full border border-line-strong text-[12px] font-semibold text-ink-2 tabular">
              {i + 1}
            </span>
            <div className="min-w-0">
              <p className="mb-2 text-[14px] font-medium text-ink">{s.title}</p>
              {s.body}
            </div>
          </li>
        ))}
      </ol>
      {showManual ? (
        <div className="mt-6 border-t border-line pt-4">
          <p className="text-[13.5px] font-medium text-ink">No browser on that machine, or running in CI?</p>
          <p className="mt-1 text-[12.5px] text-muted">
            <Link href="/authtokens" className="font-medium text-ink underline underline-offset-4">
              Create an auth token
            </Link>{" "}
            and save it once, or pass it as <code className="font-mono text-ink-2">TUND_AUTHTOKEN</code>:
          </p>
          <Command prompt={prompt} className="mt-2">
            {"tund config add-authtoken <your-token>"}
          </Command>
        </div>
      ) : null}
    </div>
  );
}
