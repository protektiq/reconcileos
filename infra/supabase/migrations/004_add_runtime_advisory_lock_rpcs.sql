create or replace function public.runtime_try_lock(lock_key bigint)
returns boolean
language plpgsql
security definer
set search_path = pg_catalog, public
as $$
declare
  did_lock boolean;
begin
  if lock_key is null then
    return false;
  end if;

  did_lock := pg_try_advisory_lock(lock_key);
  return coalesce(did_lock, false);
end;
$$;

create or replace function public.runtime_unlock(lock_key bigint)
returns boolean
language plpgsql
security definer
set search_path = pg_catalog, public
as $$
declare
  did_unlock boolean;
begin
  if lock_key is null then
    return false;
  end if;

  did_unlock := pg_advisory_unlock(lock_key);
  return coalesce(did_unlock, false);
end;
$$;

revoke all on function public.runtime_try_lock(bigint) from public;
revoke all on function public.runtime_unlock(bigint) from public;

grant execute on function public.runtime_try_lock(bigint) to service_role;
grant execute on function public.runtime_unlock(bigint) to service_role;
