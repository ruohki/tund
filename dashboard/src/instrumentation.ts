// Runs once when the server starts (docs: app/guides/instrumentation).
export async function register() {
  if (process.env.NEXT_RUNTIME !== "nodejs") return;
  // Not while building, and only with a database to listen on.
  if (process.env.NEXT_PHASE === "phase-production-build" || !process.env.TUND_DATABASE_URL) return;
  const { startAbuseMailer } = await import("./lib/abuse-mailer");
  void startAbuseMailer();
}
