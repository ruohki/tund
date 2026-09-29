"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/components/ui";

const TABS = [
  { href: "/admin", label: "Overview" },
  { href: "/admin/users", label: "Users" },
  { href: "/admin/tunnels", label: "Tunnels" },
  { href: "/admin/teams", label: "Teams" },
  { href: "/admin/domains", label: "Domains" },
  { href: "/admin/abuse", label: "Abuse" },
  { href: "/admin/settings", label: "Settings" },
  { href: "/admin/email", label: "Email" },
  { href: "/admin/sign-in", label: "Sign-in" },
  { href: "/admin/audit", label: "Audit log" },
];

export function AdminNav() {
  const pathname = usePathname();
  return (
    <nav aria-label="Administration" className="-mx-1 mt-1 flex gap-1 overflow-x-auto scroll-thin border-b border-line">
      {TABS.map((t) => {
        const active = t.href === "/admin" ? pathname === "/admin" : pathname.startsWith(t.href);
        return (
          <Link
            key={t.href}
            href={t.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "-mb-px shrink-0 border-b-2 px-2.5 py-2 text-[13.5px] transition-colors",
              active ? "border-ink font-medium text-ink" : "border-transparent text-muted hover:text-ink",
            )}
          >
            {t.label}
          </Link>
        );
      })}
    </nav>
  );
}
