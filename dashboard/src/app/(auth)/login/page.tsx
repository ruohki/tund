import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { getCurrentUser, safeNext, usersExist, withNext } from "@/lib/auth";
import { signupCheck } from "@/lib/signup";
import { smtpConfigured } from "@/lib/mail";
import { loginAction } from "@/app/actions/auth";
import { AuthForm } from "../auth-form";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { NextHint } from "../next-hint";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/login", "Sign in", `Sign in to ${name} to manage your tunnels, domains and captured requests.`);
}

export default async function LoginPage({ searchParams }: PageProps<"/login">) {
  const { next: rawNext, reset } = await searchParams;
  const next = safeNext(rawNext);
  if (!(await usersExist())) redirect(withNext("/setup", next));
  if (await getCurrentUser()) redirect(next);
  const [signup, mailOn] = await Promise.all([signupCheck(next), smtpConfigured()]);
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Sign in</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">Manage tunnels, domains and access for this tund server.</p>
      {reset === "1" ? (
        <p role="status" className="mb-5 rounded-md border border-ok/30 bg-ok-wash px-3 py-2 text-[13px] text-ok">
          Your password was changed and you were signed out everywhere. Sign in with the new password.
        </p>
      ) : null}
      <NextHint next={next} />
      <AuthForm action={loginAction} mode="login" next={next} />
      {mailOn ? (
        <p className="mt-4 text-[13px]">
          <Link href="/forgot-password" className="text-ink-2 underline underline-offset-4 hover:text-ink">
            Forgot your password?
          </Link>
        </p>
      ) : null}
      {signup.allowed ? (
        <p className="mt-6 text-[13px] text-ink-2">
          No account yet?{" "}
          <Link href={withNext("/signup", next)} className="font-medium text-ink underline underline-offset-4">
            Create one
          </Link>
        </p>
      ) : (
        <p className="mt-6 text-[13px] text-muted">{signup.reason}</p>
      )}
    </>
  );
}
