import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";
import { getCurrentUser, safeNext, usersExist, withNext } from "@/lib/auth";
import { signupCheck } from "@/lib/signup";
import { signupAction } from "@/app/actions/auth";
import { AuthForm } from "../auth-form";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { NextHint } from "../next-hint";
import { OAuthButtons } from "../oauth-buttons";
import { enabledProviders } from "@/lib/oauth";
import { turnstileSiteKey } from "@/lib/turnstile";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata(
    "/signup",
    "Create an account",
    `Create a ${name} account and share localhost on a public HTTPS address with tund http 3000.`,
  );
}

export default async function SignupPage({ searchParams }: PageProps<"/signup">) {
  const { next: rawNext } = await searchParams;
  const next = safeNext(rawNext);
  if (!(await usersExist())) redirect(withNext("/setup", next));
  if (!(await signupCheck(next)).allowed) redirect(withNext("/login", next));
  if (await getCurrentUser()) redirect(next);
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Create an account</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        Then run <code className="font-mono text-[13px] text-ink">tund http 3000</code>. Its first run logs your terminal in
        through this browser.
      </p>
      <NextHint next={next} />
      <OAuthButtons providers={await enabledProviders()} next={next} terms />
      <AuthForm action={signupAction} mode="signup" next={next} turnstileSiteKey={await turnstileSiteKey()} />
      <p className="mt-6 text-[13px] text-ink-2">
        Already have an account?{" "}
        <Link href={withNext("/login", next)} className="font-medium text-ink underline underline-offset-4">
          Sign in
        </Link>
      </p>
    </>
  );
}
