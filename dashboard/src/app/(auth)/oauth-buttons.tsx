import { buttonClass } from "@/components/ui";
import { ProviderIcon } from "@/components/provider-icons";
import { PROVIDER_LABEL, type OAuthProviderId } from "@/lib/oauth-shared";

/**
 * "Continue with Google / GitHub" above the email form. Plain links: the
 * start route redirects to the provider. `terms` adds the notice for people
 * whose first sign-in creates an account.
 */
export function OAuthButtons({ providers, next, terms }: { providers: OAuthProviderId[]; next: string; terms: boolean }) {
  if (!providers.length) return null;
  const query = next !== "/" ? `?next=${encodeURIComponent(next)}` : "";
  return (
    <div className="mb-5">
      <div className="flex flex-col gap-2">
        {providers.map((p) => (
          <a key={p} href={`/auth/oauth/${p}/start${query}`} className={buttonClass("secondary", "md", "w-full gap-2")}>
            <ProviderIcon provider={p} />
            Continue with {PROVIDER_LABEL[p]}
          </a>
        ))}
      </div>
      {terms ? (
        <p className="mt-2.5 text-[12px] leading-5 text-muted">
          New here? Continuing creates your account and means you agree to the{" "}
          <a href="/terms" target="_blank" className="underline underline-offset-4 hover:text-ink">
            Terms of Service
          </a>{" "}
          and the{" "}
          <a href="/acceptable-use" target="_blank" className="underline underline-offset-4 hover:text-ink">
            Acceptable Use Policy
          </a>
          .
        </p>
      ) : null}
      <div className="mt-5 flex items-center gap-3 text-[12px] text-muted" aria-hidden>
        <span className="h-px flex-1 bg-line" />
        or with email
        <span className="h-px flex-1 bg-line" />
      </div>
    </div>
  );
}
