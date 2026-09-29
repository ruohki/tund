"use client";

import { useState } from "react";
import { useLiveEvents } from "./live";
import { cn } from "./ui";
import { CopyButton } from "./client-ui";

/**
 * Public URL → local address, joined by a tube that is lit while the tunnel is
 * online. Each live request sends a pulse down the tube.
 */
export function RouteLine({
  hostname,
  publicUrl,
  localAddr,
  online,
  className,
  contents,
  proto = "http",
}: {
  hostname: string;
  publicUrl: string;
  localAddr: string;
  online: boolean;
  className?: string;
  /** tcp:// and tls:// addresses aren't browser links: they get a copy button instead. */
  proto?: "http" | "tcp" | "tls";
  /** Render the three parts straight into a parent (sub)grid so rows line up. */
  contents?: boolean;
}) {
  const [pulses, setPulses] = useState<number[]>([]);

  useLiveEvents((e) => {
    if (!online) return;
    const hit =
      (e.type === "request" && e.data.hostname === hostname) || (e.type === "connection" && e.data.address === hostname);
    if (!hit) return;
    const id = Date.now() + Math.random();
    setPulses((p) => [...p.slice(-4), id]);
    setTimeout(() => setPulses((p) => p.filter((x) => x !== id)), 1000);
  });

  return (
    <div className={cn(contents ? "contents font-mono text-[12.5px]" : "flex min-w-0 items-center gap-3 font-mono text-[12.5px]", className)}>
      {proto === "http" ? (
        <a
          href={publicUrl}
          target="_blank"
          rel="noreferrer"
          className={cn(
            "min-w-0 justify-self-start truncate rounded-[5px] border px-2 py-1 font-mono text-[12.5px] transition-colors",
            online ? "border-line-strong bg-surface text-ink hover:border-ink-2" : "border-line bg-surface-2 text-muted",
          )}
          title={publicUrl}
        >
          {publicUrl.replace(/^https?:\/\//, "")}
        </a>
      ) : (
        <span
          className={cn(
            "inline-flex min-w-0 items-center gap-0.5 justify-self-start rounded-[5px] border py-0.5 pl-2 pr-0.5 font-mono text-[12.5px]",
            online ? "border-line-strong bg-surface text-ink" : "border-line bg-surface-2 text-muted",
          )}
          title={publicUrl}
        >
          <span className="truncate">{publicUrl}</span>
          <CopyButton value={publicUrl} className="h-6 w-6" />
        </span>
      )}
      <span className="route-tube min-w-8 flex-1" data-lit={online ? "true" : "false"} aria-hidden>
        {pulses.map((id) => (
          <span key={id} className="route-pulse" />
        ))}
      </span>
      <span
        className={cn(
          "shrink-0 justify-self-start truncate rounded-[5px] border px-2 py-1 font-mono text-[12.5px]",
          online ? "border-line-strong bg-surface-2 text-ink" : "border-line bg-surface-2 text-muted",
        )}
        title={localAddr}
      >
        {localAddr.replace(/^http:\/\//, "")}
      </span>
    </div>
  );
}
