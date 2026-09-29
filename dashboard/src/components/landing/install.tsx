"use client";

import { useState } from "react";
import { Command, useDetectedOs } from "../client-ui";
import { installCommand, OsSwitch } from "../install-steps";

type Os = "mac" | "linux" | "windows";

/** Install + run, with an OS switch that defaults to the visitor's system. */
export function LandingInstall({ dashboardUrl }: { dashboardUrl: string }) {
  const detected = useDetectedOs();
  const [chosen, setChosen] = useState<Os | null>(null);
  const os = chosen ?? detected ?? "mac";
  const prompt = os === "windows" ? ">" : "$";
  return (
    <div>
      <OsSwitch value={os} onChange={setChosen} />
      <ol className="mt-5 flex flex-col gap-5">
        <li className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3">
          <span className="mt-1 grid h-6 w-6 place-items-center rounded-full border border-line-strong text-[12px] font-semibold text-ink-2">
            1
          </span>
          <div className="min-w-0">
            <p className="mb-2 text-[15px] font-medium text-ink">Install the client</p>
            <Command prompt={prompt}>{installCommand(os, dashboardUrl)}</Command>
            <p className="mt-1.5 text-[13px] text-muted">One binary, no admin rights, no network drivers.</p>
          </div>
        </li>
        <li className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3">
          <span className="mt-1 grid h-6 w-6 place-items-center rounded-full border border-line-strong text-[12px] font-semibold text-ink-2">
            2
          </span>
          <div className="min-w-0">
            <p className="mb-2 text-[15px] font-medium text-ink">Share a local port</p>
            <Command prompt={prompt}>tund http 3000</Command>
            <p className="mt-1.5 text-[13px] text-muted">
              The first run opens your browser to sign in or create an account. After that,{" "}
              <code className="font-mono text-[12.5px] text-ink-2">tund http 3000</code> gives you the same URL every time.
            </p>
          </div>
        </li>
      </ol>
    </div>
  );
}
