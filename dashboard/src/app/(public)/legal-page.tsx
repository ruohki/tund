import Link from "next/link";
import { getCurrentUser } from "@/lib/auth";
import { legalMarkdown, renderMarkdown, type Page } from "@/lib/legal";

/** Renders /terms, /acceptable-use, /privacy or /imprint; admins get a pointer to where the text is edited. */
export async function LegalPage({ page }: { page: Page }) {
  const [{ markdown, custom }, user] = await Promise.all([legalMarkdown(page), getCurrentUser()]);
  return (
    <>
      {user?.isAdmin ? (
        <p className="mb-8 rounded-md border border-line bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
          {custom
            ? "This is your own text."
            : page === "privacy"
              ? "This text is generated from this instance's settings; check it, and add what it can't know (such as your hosting providers) in your own version."
              : page === "imprint"
                ? "This text is generated from your details under Imprint; replace it with your own if it doesn't fit."
                : "This is the built-in default text."}{" "}
          Edit it under{" "}
          <Link
            href={!custom && page === "imprint" ? "/admin/settings#imprint" : "/admin/settings#legal-pages"}
            className="font-medium text-ink underline underline-offset-4"
          >
            Admin → Settings → {!custom && page === "imprint" ? "Imprint" : "Legal pages"}
          </Link>
          {!custom && page === "imprint" ? "." : " (Markdown)."} Only admins see this note.
        </p>
      ) : null}
      <article className="md-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(markdown) }} />
    </>
  );
}
