-- Account activity (docs/SPEC.md "Inactive accounts"): the last dashboard
-- sign-in or visit, CLI/API token use or client disconnect. Instances can
-- delete accounts nobody has used for a long time.
alter table users add column last_seen_at timestamptz;
-- Start from the traces older versions left. Sessions slide their 30-day
-- expiry forward at most once a day, so expiry minus 30 days is the last visit.
update users u set last_seen_at = greatest(
  u.created_at,
  (select max(s.expires_at) - interval '30 days' from sessions s where s.user_id = u.id),
  (select max(t.last_used_at) from authtokens t where t.user_id = u.id),
  (select max(coalesce(a.disconnected_at, now())) from agent_sessions a where a.user_id = u.id),
  (select max(i.last_used_at) from user_identities i where i.user_id = u.id),
  (select max(p.last_used_at) from user_passkeys p where p.user_id = u.id));
alter table users alter column last_seen_at set default now();
alter table users alter column last_seen_at set not null;

-- When the owner was told the account is about to be deleted. Activity after
-- it cancels the deletion; a later notice starts over.
alter table users add column inactivity_notice_at timestamptz;
