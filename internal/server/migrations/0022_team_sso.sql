-- Team single sign-on (docs/SPEC.md "Teams"): when required, every HTTP tunnel
-- on a team hostname (team static hostnames and custom domains) asks visitors
-- to sign in with the team's provider, whatever auth flags the client passes.
-- A team domain with its own single sign-on settings keeps them.

alter table teams add column auth_oidc_required boolean not null default false;
-- Set null when the provider goes away: the edge then fails closed.
alter table teams add column auth_oidc_provider_id uuid references oidc_providers(id) on delete set null;
alter table teams add column auth_oidc_allow text[] not null default '{}';
