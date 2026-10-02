/**
 * Joins class names. A later height (h-*) or width (w-*) utility replaces an
 * earlier one with the same variants, so callers can override the size of a
 * base class like inputClass (Tailwind alone would let the stylesheet order
 * decide).
 */
export function cn(...parts: (string | false | null | undefined)[]) {
  const classes = parts.filter(Boolean).join(" ").split(/\s+/).filter(Boolean);
  const last = new Map<string, number>();
  classes.forEach((c, i) => {
    const key = sizeKey(c);
    if (key) last.set(key, i);
  });
  return classes.filter((c, i) => {
    const key = sizeKey(c);
    return !key || last.get(key) === i;
  }).join(" ");
}

/** "sm:h-8" → "sm:h", "w-full" → "w"; null for anything else. */
function sizeKey(c: string): string | null {
  // Variants end at the last ':' outside brackets (arbitrary values may contain ':').
  let depth = 0;
  let split = -1;
  for (let i = 0; i < c.length; i++) {
    if (c[i] === "[") depth++;
    else if (c[i] === "]") depth--;
    else if (c[i] === ":" && depth === 0) split = i;
  }
  const base = c.slice(split + 1).replace(/^!/, "");
  const m = /^(h|w)-/.exec(base);
  return m ? c.slice(0, split + 1) + m[1] : null;
}

export const inputClass = cn(
  "h-8.5 w-full rounded-[5px] border border-line-strong bg-surface px-2.5 text-sm text-ink",
  "placeholder:text-muted/80 focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20",
  "disabled:opacity-60",
);
