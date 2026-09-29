export function cn(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(" ");
}

export const inputClass = cn(
  "h-8.5 w-full rounded-[5px] border border-line-strong bg-surface px-2.5 text-sm text-ink",
  "placeholder:text-muted/80 focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20",
  "disabled:opacity-60",
);
