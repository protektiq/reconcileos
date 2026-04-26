create table public.github_installations (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id) on delete cascade,
  installation_id bigint not null unique,
  account_login text,
  account_type text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

alter table public.github_installations enable row level security;

create policy github_installations_org_scope_all
on public.github_installations
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create index idx_github_installations_org_id
  on public.github_installations (org_id);
