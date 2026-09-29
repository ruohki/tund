"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Table2, BarChart3 } from "lucide-react";
import type { TrafficBucket } from "@/lib/metrics";
import { formatNumber } from "@/lib/format";
import { cn } from "./ui";
import { useDisplayTimeZone } from "@/lib/use-hydrated";

const SERIES = [
  { key: "s2", label: "Success", codes: "1xx–2xx", color: "var(--s2xx)" },
  { key: "s3", label: "Redirect", codes: "3xx", color: "var(--s3xx)" },
  { key: "s4", label: "Client error", codes: "4xx", color: "var(--s4xx)" },
  { key: "s5", label: "Server error", codes: "5xx & failed", color: "var(--s5xx)" },
] as const;

type Key = (typeof SERIES)[number]["key"];

function niceMax(v: number): { max: number; step: number } {
  if (v <= 0) return { max: 4, step: 1 };
  const raw = v / 4;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? 10 * pow;
  const s = Math.max(1, step);
  return { max: Math.ceil(v / s) * s, step: s };
}

const hourLabel = (iso: string, timeZone?: string) =>
  new Date(iso).toLocaleTimeString("en-GB", { timeZone, hour: "2-digit", minute: "2-digit" });

export function TrafficChart({ buckets }: { buckets: TrafficBucket[] }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  const [active, setActive] = useState<number | null>(null);
  const [table, setTable] = useState(false);
  const tz = useDisplayTimeZone();

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => setWidth(Math.max(280, Math.floor(entry.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const totals = buckets.map((b) => b.s2 + b.s3 + b.s4 + b.s5);
  const { max, step } = useMemo(() => niceMax(Math.max(0, ...totals)), [totals]);
  const total = totals.reduce((a, b) => a + b, 0);

  const height = 200;
  const pad = { top: 8, right: 4, bottom: 24, left: 40 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;
  const band = plotW / Math.max(1, buckets.length);
  const barW = Math.min(24, Math.max(4, band * 0.62));
  const y = (v: number) => pad.top + plotH - (v / max) * plotH;
  const ticks: number[] = [];
  for (let t = 0; t <= max; t += step) ticks.push(t);
  const labelEvery = width < 520 ? 6 : 3;

  const activeBucket = active != null ? buckets[active] : null;

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <ul className="flex flex-wrap items-center gap-x-4 gap-y-1" aria-label="Legend">
          {SERIES.map((s) => (
            <li key={s.key} className="flex items-center gap-1.5 text-[12.5px] text-ink-2">
              <span aria-hidden className="h-2.5 w-2.5 rounded-[2px]" style={{ background: s.color }} />
              {s.label}
              <span className="text-muted">{s.codes}</span>
            </li>
          ))}
        </ul>
        <button
          type="button"
          onClick={() => setTable((t) => !t)}
          className="inline-flex items-center gap-1.5 rounded-[5px] px-2 py-1 text-[12.5px] text-muted hover:bg-surface-3 hover:text-ink"
        >
          {table ? <BarChart3 size={14} /> : <Table2 size={14} />}
          {table ? "Show chart" : "Show table"}
        </button>
      </div>

      {table ? (
        <div className="max-h-[216px] overflow-auto scroll-thin rounded-md border border-line">
          <table className="w-full text-[12.5px]">
            <thead className="sticky top-0 bg-surface-2 text-left text-muted">
              <tr>
                <th className="px-3 py-1.5 font-medium">Hour</th>
                {SERIES.map((s) => (
                  <th key={s.key} className="px-3 py-1.5 text-right font-medium">
                    {s.label}
                  </th>
                ))}
                <th className="px-3 py-1.5 text-right font-medium">Total</th>
              </tr>
            </thead>
            <tbody className="tabular">
              {buckets.map((b, i) => (
                <tr key={b.t} className="border-t border-line">
                  <td className="px-3 py-1 text-ink-2">{hourLabel(b.t, tz)}</td>
                  {SERIES.map((s) => (
                    <td key={s.key} className="px-3 py-1 text-right text-ink">
                      {formatNumber(b[s.key as Key])}
                    </td>
                  ))}
                  <td className="px-3 py-1 text-right font-medium text-ink">{formatNumber(totals[i])}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div ref={wrap} className="relative">
          <svg
            width={width}
            height={height}
            role="img"
            aria-label={`Requests per hour over the last 24 hours, ${formatNumber(total)} in total. Use arrow keys to read each hour.`}
            tabIndex={0}
            className="block overflow-visible outline-none focus-visible:ring-2 focus-visible:ring-focus/40 rounded"
            onKeyDown={(e) => {
              if (e.key === "ArrowRight") setActive((a) => Math.min(buckets.length - 1, (a ?? -1) + 1));
              else if (e.key === "ArrowLeft") setActive((a) => Math.max(0, (a ?? buckets.length) - 1));
              else if (e.key === "Escape") setActive(null);
              else return;
              e.preventDefault();
            }}
            onBlur={() => setActive(null)}
            onPointerLeave={() => setActive(null)}
          >
            {ticks.map((t) => (
              <g key={t}>
                <line
                  x1={pad.left}
                  x2={width - pad.right}
                  y1={y(t)}
                  y2={y(t)}
                  stroke={t === 0 ? "var(--line-strong)" : "var(--line)"}
                  strokeWidth={1}
                  shapeRendering="crispEdges"
                />
                <text x={pad.left - 8} y={y(t)} dy="0.32em" textAnchor="end" className="fill-muted tabular text-[11px]">
                  {t >= 1000 ? `${t / 1000}k` : t}
                </text>
              </g>
            ))}
            {buckets.map((b, i) => {
              const cx = pad.left + band * i + band / 2;
              let acc = 0;
              const segs = SERIES.map((s) => ({ ...s, v: b[s.key as Key] })).filter((s) => s.v > 0);
              const topIndex = segs.length - 1;
              return (
                <g key={b.t} opacity={active != null && active !== i ? 0.55 : 1}>
                  {segs.map((s, si) => {
                    const y0 = y(acc);
                    acc += s.v;
                    const y1 = y(acc);
                    // 2px surface gap between stacked segments.
                    const h = Math.max(1, y0 - y1 - (si > 0 ? 2 : 0));
                    const top = y1;
                    const r = si === topIndex ? Math.min(4, h, barW / 2) : 0;
                    const x = cx - barW / 2;
                    const d =
                      r > 0
                        ? `M${x},${top + h} V${top + r} Q${x},${top} ${x + r},${top} H${x + barW - r} Q${x + barW},${top} ${x + barW},${top + r} V${top + h} Z`
                        : `M${x},${top + h} V${top} H${x + barW} V${top + h} Z`;
                    return <path key={s.key} d={d} fill={s.color} />;
                  })}
                  {i % labelEvery === 0 ? (
                    <text x={cx} y={height - 6} textAnchor="middle" className="fill-muted tabular text-[11px]">
                      {hourLabel(b.t, tz)}
                    </text>
                  ) : null}
                  <rect
                    x={pad.left + band * i}
                    y={pad.top}
                    width={band}
                    height={plotH}
                    fill="transparent"
                    onPointerEnter={() => setActive(i)}
                    onPointerMove={() => setActive(i)}
                  />
                </g>
              );
            })}
          </svg>
          {activeBucket ? (
            <div
              className="pointer-events-none absolute top-0 z-10 w-48 rounded-md border border-line bg-surface px-3 py-2 shadow-pop"
              style={(() => {
                // Sit beside the hovered column so it never hides the bars being read.
                const colLeft = pad.left + band * active!;
                const right = colLeft + band + 8 + 192 <= width;
                return { left: right ? colLeft + band + 8 : Math.max(0, colLeft - 8 - 192) };
              })()}
            >
              <p className="text-[12px] text-muted">
                {hourLabel(activeBucket.t, tz)}–{hourLabel(new Date(Date.parse(activeBucket.t) + 3600_000).toISOString(), tz)}
              </p>
              <p className="text-[15px] font-semibold text-ink">
                {formatNumber(totals[active!])} <span className="text-[12px] font-normal text-muted">requests</span>
              </p>
              <ul className="mt-1.5 flex flex-col gap-0.5">
                {SERIES.map((s) => (
                  <li key={s.key} className="flex items-center justify-between gap-3 text-[12.5px]">
                    <span className="flex items-center gap-1.5 text-ink-2">
                      <span aria-hidden className="h-[2px] w-3 rounded-full" style={{ background: s.color }} />
                      {s.label}
                    </span>
                    <span className={cn("tabular font-medium", activeBucket[s.key as Key] ? "text-ink" : "text-muted")}>
                      {formatNumber(activeBucket[s.key as Key])}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
