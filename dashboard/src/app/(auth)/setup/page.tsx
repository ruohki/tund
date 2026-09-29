import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { safeNext, usersExist, withNext } from "@/lib/auth";
import { setupAction } from "@/app/actions/auth";
import { AuthForm } from "../auth-form";

export const metadata: Metadata = { title: "Set up" };

export default async function SetupPage({ searchParams }: PageProps<"/setup">) {
  const next = safeNext((await searchParams).next);
  if (await usersExist()) redirect(withNext("/login", next));
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Set up this server</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        Create the first account. It becomes the administrator and can add more users later.
      </p>
      <AuthForm action={setupAction} mode="setup" next={next} />
    </>
  );
}
