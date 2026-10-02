import Link from "next/link";
import { formatNumber } from "@/lib/format";
import { buttonClass } from "./ui";

export const PAGE_SIZE = 50;

/** The 1-based page from a `?page=` (or other) search parameter. */
export function pageParam(v: unknown): number {
  const n = typeof v === "string" ? Number.parseInt(v, 10) : 1;
  return Number.isInteger(n) && n > 0 && n <= 100_000 ? n : 1;
}

/** Offset of the first row on `page`. */
export const pageOffset = (page: number, size = PAGE_SIZE) => (page - 1) * size;

/**
 * "51–100 of 1,234" with previous/next links. `params` are the page's other
 * search parameters (filters, search), kept on every link; `name` is the page
 * parameter, so two lists on one page can page independently.
 */
export function Pager({
  path,
  params = {},
  page,
  total,
  size = PAGE_SIZE,
  name = "page",
}: {
  path: string;
  params?: Record<string, string>;
  page: number;
  total: number;
  size?: number;
  name?: string;
}) {
  if (page === 1 && total <= size) return null;
  const pages = Math.max(1, Math.ceil(total / size));
  const href = (p: number) => {
    const q = new URLSearchParams(Object.entries(params).filter(([k, v]) => v && k !== name));
    if (p > 1) q.set(name, String(p));
    const s = q.toString();
    return s ? `${path}?${s}` : path;
  };
  const from = pageOffset(page, size) + 1;
  const to = Math.min(total, page * size);
  const past = from > total;
  return (
    <div className="flex items-center justify-between gap-3 border-t border-line px-4 py-2.5 text-[12.5px] text-muted">
      <span className="tabular">
        {past ? `Past the last page (${formatNumber(total)} in total)` : `${formatNumber(from)}–${formatNumber(to)} of ${formatNumber(total)}`}
      </span>
      <span className="flex gap-2">
        {page > 1 ? (
          <Link href={href(Math.min(page - 1, pages))} className={buttonClass("ghost", "sm")}>
            Previous
          </Link>
        ) : null}
        {page < pages ? (
          <Link href={href(page + 1)} className={buttonClass("secondary", "sm")}>
            Next
          </Link>
        ) : null}
      </span>
    </div>
  );
}

/** A page's search parameters as plain strings (arrays and empty values dropped). */
export function plainParams(sp: Record<string, string | string[] | undefined>): Record<string, string> {
  return Object.fromEntries(Object.entries(sp).filter((e): e is [string, string] => typeof e[1] === "string" && e[1] !== ""));
}
