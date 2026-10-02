import type { NextRequest } from "next/server";
import { generateAuthenticationOptions, verifyAuthenticationResponse, type AuthenticationResponseJSON } from "@simplewebauthn/server";
import { safeNext, startSession } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { db } from "@/lib/db";
import { rateLimited } from "@/lib/email-tokens";
import { clientIp } from "@/lib/client-ip";
import { passkeyByCredential, passkeyError, passkeyRequest, userPasskeys } from "@/lib/passkeys";
import { clearSecondFactorFailures, endFlow, readFlow, relyingParty, startFlow } from "@/lib/two-factor";

export const dynamic = "force-dynamic";

type Body =
  | { step: "options"; mode: "login" | "second_factor"; next?: string }
  | { step: "verify"; response: AuthenticationResponseJSON };

/**
 * Signing in with a passkey, either on its own (mode "login", no email typed:
 * the passkey says who it is) or as the second step after the password
 * (mode "second_factor"). {step:"options"} returns the challenge,
 * {step:"verify", response} signs in and returns where to go.
 */
export async function POST(req: NextRequest) {
  const body = await passkeyRequest<Body>(req);
  if (!body) return passkeyError("Bad request.");
  if (rateLimited(`passkey-ip:${(await clientIp()) || "unknown"}`, 30, 5 * 60_000)) {
    return passkeyError("Too many attempts. Wait a few minutes.", 429);
  }
  const rp = await relyingParty();

  if (body.step === "options") {
    if (body.mode === "second_factor") {
      const second = await readFlow("second_factor");
      if (!second?.userId) return passkeyError("Sign in with your password first.", 401);
      const allow = await userPasskeys(second.userId);
      if (!allow.length) return passkeyError("This account has no passkeys.");
      const options = await generateAuthenticationOptions({ rpID: rp.rpID, allowCredentials: allow, userVerification: "preferred" });
      await startFlow("passkey_login", { userId: second.userId, data: options.challenge });
      return Response.json(options);
    }
    // On its own a passkey must verify the person (PIN, fingerprint, face), not just presence.
    const options = await generateAuthenticationOptions({ rpID: rp.rpID, userVerification: "required" });
    await startFlow("passkey_login", { data: options.challenge, next: safeNext(body.next) });
    return Response.json(options);
  }

  const flow = await readFlow("passkey_login");
  if (!flow) return passkeyError("That took too long. Try again.");
  await endFlow("passkey_login", flow);
  const stored = await passkeyByCredential(String(body.response?.id ?? ""));
  if (!stored || (flow.userId && stored.userId !== flow.userId)) {
    return passkeyError("This passkey isn't registered here. Sign in another way and add it in your settings.");
  }
  let newCounter: number;
  try {
    const v = await verifyAuthenticationResponse({
      response: body.response,
      expectedChallenge: flow.data,
      expectedOrigin: rp.origin,
      expectedRPID: rp.rpID,
      credential: { id: stored.credentialId, publicKey: stored.publicKey, counter: stored.counter, transports: stored.transports },
      requireUserVerification: !flow.userId,
    });
    if (!v.verified) return passkeyError("The passkey couldn't be verified.");
    newCounter = v.authenticationInfo.newCounter;
  } catch (err) {
    return passkeyError(`The passkey couldn't be verified: ${err instanceof Error ? err.message : String(err)}`);
  }
  // A counter that doesn't move forward means the key may have been cloned.
  if (stored.counter > 0 && newCounter <= stored.counter) {
    return passkeyError("This passkey reported an old signature counter. Remove it and add it again.");
  }
  await db()`update user_passkeys set counter = ${newCounter}, last_used_at = now() where id = ${stored.id}`;

  const [user] = await db()`select id, email, disabled_at from users where id = ${stored.userId}`;
  if (!user || user.disabled_at) return passkeyError("This account has been disabled. Contact an administrator of this server.", 403);

  let next = flow.next;
  if (flow.userId) {
    // The second step of a password sign-in.
    const second = await readFlow("second_factor");
    if (!second || second.userId !== stored.userId) return passkeyError("Sign in with your password first.", 401);
    await endFlow("second_factor", second);
    await clearSecondFactorFailures(stored.userId);
    next = second.next;
  } else {
    await audit({ id: user.id as string, email: user.email as string }, "user.passkey_login", user.email as string);
  }
  await startSession(user.id as string);
  return Response.json({ redirect: next });
}
