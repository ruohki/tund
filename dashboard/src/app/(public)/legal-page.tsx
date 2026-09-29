import Link from "next/link";
import { getCurrentUser } from "@/lib/auth";
import { legalMarkdown, renderMarkdown } from "@/lib/legal";

/** Renders /terms or /acceptable-use; admins get a pointer to where the text is edited. */
export async function LegalPage({ page }: { page: "terms" | "acceptable-use" }) {
  const [{ markdown, custom }, user] = await Promise.all([legalMarkdown(page), getCurrentUser()]);
  return (
    <>
      {user?.isAdmin ? (
        <p className="mb-8 rounded-md border border-line bg-surface-2 px-3.5 py-2.5 text-[13px] text-ink-2">
          {custom ? "This is your own text." : "This is the built-in default text."} Edit it under{" "}
          <Link href="/admin/settings#legal-pages" className="font-medium text-ink underline underline-offset-4">
            Admin → Settings → Legal pages
          </Link>{" "}
          (Markdown). Only admins see this note.
        </p>
      ) : null}
      <article className="md-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(markdown) }} />
    </>
  );
}
