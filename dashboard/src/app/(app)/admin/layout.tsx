import { requireAdmin } from "@/lib/auth";
import { AdminNav } from "./admin-nav";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  await requireAdmin();
  return (
    <>
      <div className="mb-6">
        <p className="text-[13px] text-muted">Administration</p>
        <AdminNav />
      </div>
      {children}
    </>
  );
}
