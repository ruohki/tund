import type { ReactNode } from "react";

/** label · value · optional context line. Proportional figures for the value. */
export function StatTile({ label, value, context, live }: { label: string; value: ReactNode; context?: ReactNode; live?: boolean }) {
  return (
    <div className="flex flex-col justify-between gap-2 px-4 py-3.5">
      <p className="flex items-center gap-2 text-[13px] text-ink-2">
        {live ? <span className="live-dot h-1.5! w-1.5!" aria-hidden /> : null}
        {label}
      </p>
      <p className="text-[28px] font-semibold leading-none tracking-[-0.02em] text-ink">{value}</p>
      <p className="min-h-4 text-[12px] text-muted">{context}</p>
    </div>
  );
}
