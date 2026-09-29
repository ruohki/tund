import Link from "next/link";
import { Wordmark } from "@/components/brand";
import { ThemeToggle } from "@/components/theme-toggle";
import { publicConfig } from "@/lib/config";

/** Public pages anyone can open: abuse reports and the legal pages. */
export default function PublicLayout({ children }: { children: React.ReactNode }) {
  const c = publicConfig();
  const link = "underline-offset-4 hover:text-ink hover:underline";
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="border-b border-line">
        <div className="mx-auto flex max-w-[820px] items-center px-5 py-4 sm:px-8">
          <Link href="/" aria-label="Home">
            <Wordmark />
          </Link>
        </div>
      </header>
      <main className="mx-auto w-full max-w-[820px] flex-1 px-5 py-10 sm:px-8 sm:py-14">{children}</main>
      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-[820px] flex-wrap items-center justify-between gap-4 px-5 py-6 text-[12.5px] text-muted sm:px-8">
          <span className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <span>{c.dashboardHost}</span>
            <Link href="/terms" className={link}>
              Terms
            </Link>
            <Link href="/acceptable-use" className={link}>
              Acceptable use
            </Link>
            <Link href="/report" className={link}>
              Report abuse
            </Link>
          </span>
          <ThemeToggle />
        </div>
      </footer>
    </div>
  );
}
