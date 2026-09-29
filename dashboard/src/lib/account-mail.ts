import "server-only";
import { config } from "./config";
import { db } from "./db";
import { issueEmailToken } from "./email-tokens";
import { signupNoticeEmail, trySendMail, verifyEmail } from "./mail";
import { getSettings } from "./settings";

/** Emails a fresh verification link (older links stop working). */
export async function sendVerification(userId: string, email: string): Promise<boolean> {
  const token = await issueEmailToken(userId, "verify");
  const { instance_name } = await getSettings();
  return trySendMail(email, verifyEmail(instance_name, `${config().dashboardUrl}/verify-email/${token}`));
}

/** Tells every active admin about a new account, when the setting asks for it. */
export async function notifyAdminsOfSignup(userId: string, email: string) {
  const s = await getSettings();
  if (!s.notify_admins_on_signup) return;
  const admins = await db()`select email from users where is_admin and disabled_at is null and id <> ${userId}`;
  const mail = signupNoticeEmail(s.instance_name, email, `${config().dashboardUrl}/admin/users/${userId}`);
  await Promise.all(admins.map((a) => trySendMail(a.email as string, mail)));
}
