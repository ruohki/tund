import type { NextRequest } from "next/server";
import { generateRegistrationOptions, verifyRegistrationResponse, type RegistrationResponseJSON } from "@simplewebauthn/server";
import { getCurrentUser } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { config } from "@/lib/config";
import { db } from "@/lib/db";
import { securityNoticeEmail, smtpConfigured, trySendMail } from "@/lib/mail";
import { passkeyError, passkeyRequest, userPasskeys } from "@/lib/passkeys";
import { getSettings } from "@/lib/settings";
import { endFlow, readFlow, relyingParty, startFlow } from "@/lib/two-factor";

export const dynamic = "force-dynamic";

type Body = { step: "options" } | { step: "verify"; response: RegistrationResponseJSON; name?: string };

/** Adding a passkey to the signed-in account: {step:"options"}, then {step:"verify", response, name}. */
export async function POST(req: NextRequest) {
  const user = await getCurrentUser();
  if (!user) return passkeyError("Your session expired. Sign in again.", 401);
  const body = await passkeyRequest<Body>(req);
  if (!body) return passkeyError("Bad request.");
  const rp = await relyingParty();

  if (body.step === "options") {
    const existing = await userPasskeys(user.id);
    if (existing.length >= 20) return passkeyError("You have 20 passkeys already. Remove one first.");
    const options = await generateRegistrationOptions({
      rpName: rp.rpName,
      rpID: rp.rpID,
      userName: user.email,
      userDisplayName: user.name || user.email,
      userID: new TextEncoder().encode(user.id),
      attestationType: "none",
      excludeCredentials: existing.map((p) => ({ id: p.id, transports: p.transports })),
      // Discoverable, so the passkey signs in without typing the email first.
      authenticatorSelection: { residentKey: "required", userVerification: "preferred" },
    });
    await startFlow("passkey_register", { userId: user.id, data: options.challenge });
    return Response.json(options);
  }

  const flow = await readFlow("passkey_register");
  if (!flow || flow.userId !== user.id) return passkeyError("That took too long. Try again.");
  await endFlow("passkey_register", flow);
  let info;
  try {
    const v = await verifyRegistrationResponse({
      response: body.response,
      expectedChallenge: flow.data,
      expectedOrigin: rp.origin,
      expectedRPID: rp.rpID,
      requireUserVerification: false,
    });
    if (!v.verified) return passkeyError("The passkey couldn't be verified.");
    info = v.registrationInfo;
  } catch (err) {
    return passkeyError(`The passkey couldn't be verified: ${err instanceof Error ? err.message : String(err)}`);
  }
  const name = String(body.name ?? "").trim().slice(0, 60) || "Passkey";
  try {
    await db()`
      insert into user_passkeys (user_id, credential_id, public_key, counter, transports, name, backed_up)
      values (${user.id}, ${info.credential.id}, ${Buffer.from(info.credential.publicKey)}, ${info.credential.counter},
        ${info.credential.transports ?? []}, ${name}, ${info.credentialBackedUp})`;
  } catch (err) {
    if ((err as { code?: string }).code === "23505") return passkeyError("This passkey is already registered.");
    throw err;
  }
  await audit({ id: user.id, email: user.email }, "user.passkey_add", user.email, { name });
  if (await smtpConfigured()) {
    const { instance_name } = await getSettings();
    await trySendMail(
      user.email,
      securityNoticeEmail(instance_name, "A passkey was added", `The passkey “${name}” can now sign in to your account.`, `${config().dashboardUrl}/settings#security`),
    );
  }
  return Response.json({ ok: true });
}
