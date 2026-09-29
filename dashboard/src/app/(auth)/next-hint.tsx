/** Explains where the visitor will land after signing in, for flows that start elsewhere. */
export function NextHint({ next }: { next: string }) {
  if (!next.startsWith("/device")) return null;
  return (
    <p className="mb-5 rounded-md border border-line-strong bg-surface px-3 py-2 text-[13px] text-ink-2">
      Sign in to finish logging in your terminal. You&apos;ll confirm the code on the next page.
    </p>
  );
}
