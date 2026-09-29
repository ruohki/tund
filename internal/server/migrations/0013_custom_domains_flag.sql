-- Custom domains are a feature the instance turns on: the setting
-- custom_domains (default off, env TUND_CUSTOM_DOMAINS) applies to every
-- account, and users.custom_domains overrides it per account.
-- NULL = the instance setting (admins: on), true = on, false = off.
alter table users add column custom_domains boolean;
