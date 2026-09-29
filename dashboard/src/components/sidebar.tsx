"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import {
  Activity,
  BookOpen,
  Cable,
  Globe,
  KeyRound,
  LayoutGrid,
  LogOut,
  Menu,
  Settings,
  ShieldCheck,
  Users,
  UsersRound,
  X,
} from "lucide-react";
import { Wordmark } from "./brand";
import { ThemeToggle } from "./theme-toggle";
import { LiveIndicator } from "./live";
import { cn } from "./ui";
import { logoutAction } from "@/app/actions/auth";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutGrid },
  { href: "/tunnels", label: "Tunnels", icon: Cable },
  { href: "/inspect", label: "Inspect", icon: Activity },
  { href: "/domains", label: "Domains", icon: Globe },
  { href: "/access", label: "Access control", icon: ShieldCheck },
  { href: "/teams", label: "Teams", icon: UsersRound },
  { href: "/authtokens", label: "Auth tokens", icon: KeyRound },
  { href: "/get-started", label: "Get started", icon: BookOpen },
];

function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(href + "/");
}

function NavLink({ href, label, icon: Icon, pathname }: { href: string; label: string; icon: typeof Globe; pathname: string }) {
  const active = isActive(pathname, href);
  return (
    <Link
      href={href}
      aria-current={active ? "page" : undefined}
      className={cn(
        "group relative flex h-8 items-center gap-2.5 rounded-[5px] px-2.5 text-[14px] transition-colors",
        active ? "bg-surface-3 font-medium text-ink" : "text-ink-2 hover:bg-surface-2 hover:text-ink",
      )}
    >
      {active ? <span aria-hidden className="absolute -left-3 top-1.5 bottom-1.5 w-[3px] rounded-r bg-sodium" /> : null}
      <Icon size={16} strokeWidth={1.75} className={active ? "text-ink" : "text-muted group-hover:text-ink-2"} />
      {label}
    </Link>
  );
}

export function Sidebar({ user, brand }: { user: { email: string; name: string; isAdmin: boolean }; brand: string }) {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => setOpen(false), [pathname]);

  const content = (
    <div className="flex h-full flex-col">
      <div className="flex h-14 items-center justify-between px-4">
        <Link href="/" className="rounded-sm">
          <Wordmark name={brand} />
        </Link>
        <LiveIndicator />
      </div>
      <nav className="flex flex-col gap-0.5 px-3 pt-2" aria-label="Main">
        {NAV.map((item) => (
          <NavLink key={item.href} {...item} pathname={pathname} />
        ))}
      </nav>
      <div className="mt-6 px-3">
        <p className="px-2.5 pb-1.5 text-[12px] text-muted">Account</p>
        <div className="flex flex-col gap-0.5">
          <NavLink href="/settings" label="Settings" icon={Settings} pathname={pathname} />
          {user.isAdmin ? <NavLink href="/admin" label="Admin" icon={Users} pathname={pathname} /> : null}
        </div>
      </div>
      <div className="mt-auto border-t border-line px-4 py-3">
        <div className="flex items-center justify-between gap-2">
          <div className="min-w-0">
            <p className="truncate text-[13px] font-medium text-ink">{user.name || user.email.split("@")[0]}</p>
            <p className="truncate text-[12px] text-muted">{user.email}</p>
          </div>
          <form action={logoutAction}>
            <button
              type="submit"
              title="Sign out"
              aria-label="Sign out"
              className="grid h-7 w-7 place-items-center rounded-[5px] text-muted hover:bg-surface-3 hover:text-ink"
            >
              <LogOut size={15} />
            </button>
          </form>
        </div>
        <div className="mt-3">
          <ThemeToggle />
        </div>
      </div>
    </div>
  );

  return (
    <>
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-60 border-r border-line bg-surface lg:block">{content}</aside>
      <div className="sticky top-0 z-30 flex h-12 items-center justify-between border-b border-line bg-surface px-4 lg:hidden">
        <Link href="/">
          <Wordmark name={brand} />
        </Link>
        <button
          type="button"
          aria-label={open ? "Close menu" : "Open menu"}
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="grid h-8 w-8 place-items-center rounded-[5px] text-ink-2 hover:bg-surface-3"
        >
          {open ? <X size={18} /> : <Menu size={18} />}
        </button>
      </div>
      {open ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/30" onClick={() => setOpen(false)} />
          <aside className="absolute inset-y-0 left-0 w-72 border-r border-line bg-surface shadow-pop">{content}</aside>
        </div>
      ) : null}
    </>
  );
}
