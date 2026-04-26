create or replace function public.get_org_id()
returns uuid
language sql
stable
set search_path = public, auth
as $$
  select u.org_id
  from public.users as u
  where u.id = auth.uid()
  limit 1
$$;
