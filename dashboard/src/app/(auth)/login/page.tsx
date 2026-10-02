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
import { OAuthButtons } from "../oauth-buttons";
import { enabledProviders } from "@/lib/oauth";
import { oauthError } from "@/lib/oauth-shared";
import { PasskeyButton } from "@/components/passkey-button";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/login", "Sign in", `Sign in to ${name} to manage your tunnels, domains and captured requests.`, "product");
}

export default async function LoginPage({ searchParams }: PageProps<"/login">) {
  const { next: rawNext, reset, error, two_factor: twoFactor } = await searchParams;
  const next = safeNext(rawNext);
  if (!(await usersExist())) redirect(withNext("/setup", next));
  if (await getCurrentUser()) redirect(next);
  const [signup, mailOn, providers] = await Promise.all([signupCheck(next), smtpConfigured(), enabledProviders()]);
  const oauthFailed = oauthError(error);
  const { name: brand } = await siteInfo();
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Sign in</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">Manage tunnels, domains and access for this {brand} server.</p>
      {reset === "1" ? (
        <p role="status" className="mb-5 rounded-md border border-ok/30 bg-ok-wash px-3 py-2 text-[13px] text-ok">
          Your password was changed and you were signed out everywhere. Sign in with the new password.
        </p>
      ) : null}
      {twoFactor === "expired" || twoFactor === "attempts" ? (
        <p role="alert" className="mb-5 rounded-md border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
          {twoFactor === "attempts"
            ? "Too many wrong codes. Sign in again to get new attempts."
            : "The sign-in took too long. Start again."}
        </p>
      ) : null}
      {oauthFailed ? (
        <p role="alert" className="mb-5 rounded-md border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
          {oauthFailed}
        </p>
      ) : null}
      <NextHint next={next} />
      <OAuthButtons providers={providers} next={next} terms={signup.allowed} />
      <AuthForm action={loginAction} mode="login" next={next} />
      <PasskeyButton mode="login" next={next} className="mt-3" />
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
