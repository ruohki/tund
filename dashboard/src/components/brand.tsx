/** The tund mark: a tunnel portal with a lit bore. */
export function BrandMark({ size = 22, lit = true }: { size?: number; lit?: boolean }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
      <path d="M5 27V15a11 11 0 0 1 22 0v12" fill="none" stroke="currentColor" strokeWidth="3" opacity="0.9" />
      <path
        d="M10.5 27V15.5a5.5 5.5 0 0 1 11 0V27"
        fill={lit ? "var(--sodium)" : "var(--line-strong)"}
        style={lit ? { filter: "drop-shadow(0 0 3px var(--sodium-glow))" } : undefined}
      />
    </svg>
  );
}

export function Wordmark() {
  return (
    <span className="inline-flex items-center gap-2 text-ink">
      <BrandMark />
      <span className="text-[19px] font-bold tracking-[-0.03em]">TUNd</span>
    </span>
  );
}
