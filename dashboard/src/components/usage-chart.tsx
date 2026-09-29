"use client";

import { useEffect, useRef, useState } from "react";
import { BarChart3, Table2 } from "lucide-react";
import { formatTransfer } from "@/lib/format";
import { cn } from "./ui";

type Day = { day: string; bytesIn: number; bytesOut: number };

const SERIES = [
  { key: "bytesIn", label: "Visitors to you", color: "var(--series-in)" },
  { key: "bytesOut", label: "You to visitors", color: "var(--series-out)" },
] as const;

function niceMax(v: number) {
  if (v <= 0) return { max: 1000, step: 250 };
  const raw = v / 4;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? 10 * pow;
  return { max: Math.ceil(v / step) * step, step };
}

// Day labels come from "YYYY-MM-DD" strings, so they're identical on server and client.
const dayNum = (d: string) => String(Number(d.slice(8, 10)));

/** Daily transfer this month, stacked by direction. */
export function UsageChart({ days }: { days: Day[] }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  const [active, setActive] = useState<number | null>(null);
  const [table, setTable] = useState(false);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(280, Math.floor(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, [table]);

  const totals = days.map((d) => d.bytesIn + d.bytesOut);
  const { max, step } = niceMax(Math.max(0, ...totals));
  const height = 170;
  const pad = { top: 8, right: 4, bottom: 22, left: 58 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;
  const band = plotW / Math.max(1, days.length);
  const barW = Math.min(18, Math.max(3, band * 0.62));
  const y = (v: number) => pad.top + plotH - (v / max) * plotH;
  const ticks: number[] = [];
  for (let t = 0; t <= max; t += step) ticks.push(t);
  const every = width < 520 ? 7 : 3;
  const a = active != null ? days[active] : null;

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <ul className="flex flex-wrap gap-x-4 gap-y-1" aria-label="Legend">
          {SERIES.map((s) => (
            <li key={s.key} className="flex items-center gap-1.5 text-[12.5px] text-ink-2">
              <span aria-hidden className="h-2.5 w-2.5 rounded-[2px]" style={{ background: s.color }} />
              {s.label}
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
        <div className="max-h-[200px] overflow-auto scroll-thin rounded-md border border-line">
          <table className="w-full text-[12.5px]">
            <thead className="sticky top-0 bg-surface-2 text-left text-muted">
              <tr>
                <th className="px-3 py-1.5 font-medium">Day</th>
                {SERIES.map((s) => (
                  <th key={s.key} className="px-3 py-1.5 text-right font-medium">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tabular">
              {days.map((d) => (
                <tr key={d.day} className="border-t border-line">
                  <td className="px-3 py-1 text-ink-2">{d.day}</td>
                  <td className="px-3 py-1 text-right text-ink">{formatTransfer(d.bytesIn)}</td>
                  <td className="px-3 py-1 text-right text-ink">{formatTransfer(d.bytesOut)}</td>
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
            aria-label="Transfer per day this month"
            className="block overflow-visible"
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
                  shapeRendering="crispEdges"
                />
                <text x={pad.left - 8} y={y(t)} dy="0.32em" textAnchor="end" className="fill-muted tabular text-[11px]">
                  {formatTransfer(t)}
                </text>
              </g>
            ))}
            {days.map((d, i) => {
              const cx = pad.left + band * i + band / 2;
              const x = cx - barW / 2;
              const inTop = y(d.bytesIn);
              const outTop = y(d.bytesIn + d.bytesOut);
              const inH = Math.max(0, y(0) - inTop);
              const outH = Math.max(0, inTop - outTop - (d.bytesIn > 0 && d.bytesOut > 0 ? 2 : 0));
              return (
                <g key={d.day} opacity={active != null && active !== i ? 0.55 : 1}>
                  {d.bytesIn > 0 ? <rect x={x} y={inTop} width={barW} height={Math.max(1, inH)} fill="var(--series-in)" /> : null}
                  {d.bytesOut > 0 ? (
                    <rect x={x} y={outTop} width={barW} height={Math.max(1, outH)} rx={Math.min(3, outH / 2)} fill="var(--series-out)" />
                  ) : null}
                  {i % every === 0 ? (
                    <text x={cx} y={height - 5} textAnchor="middle" className="fill-muted tabular text-[11px]">
                      {dayNum(d.day)}
                    </text>
                  ) : null}
                  <rect
                    x={pad.left + band * i}
                    y={pad.top}
                    width={band}
                    height={plotH}
                    fill="transparent"
                    onPointerEnter={() => setActive(i)}
                  />
                </g>
              );
            })}
          </svg>
          {a ? (
            <div
              className="pointer-events-none absolute top-0 z-10 w-48 rounded-md border border-line bg-surface px-3 py-2 shadow-pop"
              style={{
                left: (() => {
                  const col = pad.left + band * active!;
                  return col + band + 8 + 192 <= width ? col + band + 8 : Math.max(0, col - 200);
                })(),
              }}
            >
              <p className="text-[12px] text-muted">{a.day}</p>
              <p className="text-[15px] font-semibold text-ink">{formatTransfer(a.bytesIn + a.bytesOut)}</p>
              <ul className="mt-1 flex flex-col gap-0.5">
                {SERIES.map((s) => (
                  <li key={s.key} className="flex items-center justify-between gap-3 text-[12.5px]">
                    <span className="flex items-center gap-1.5 text-ink-2">
                      <span aria-hidden className="h-[2px] w-3 rounded-full" style={{ background: s.color }} />
                      {s.label}
                    </span>
                    <span className={cn("tabular font-medium text-ink")}>{formatTransfer(a[s.key])}</span>
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
