-- TCP and TLS passthrough tunnels are a feature the instance turns on, like
-- custom domains: the setting passthrough (default off, env TUND_PASSTHROUGH)
-- applies to every account, and users.passthrough overrides it per account.
-- NULL = the instance setting (admins: on), true = on, false = off.
alter table users add column passthrough boolean;
