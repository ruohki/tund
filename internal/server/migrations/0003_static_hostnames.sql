-- Static hostnames: subdomains of the base domain pinned to an account
-- (domains.kind = 'subdomain'). One of them can be the account's default,
-- used by `tund http <port>` when no name is given.
alter table domains add column is_default boolean not null default false;
create unique index domains_one_default_per_user on domains(user_id) where is_default;
