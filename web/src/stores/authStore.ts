import type { Session, User } from '@supabase/supabase-js'
import { create } from 'zustand'

type OrgContext = {
  id: string | null
  name: string
}

type AuthStore = {
  user: User | null
  session: Session | null
  org: OrgContext
  setSession: (session: Session) => void
  clearSession: () => void
}

const defaultOrg: OrgContext = {
  id: null,
  name: 'No organization selected',
}

const getOrgFromSession = (session: Session): OrgContext => {
  const metadata = session.user.user_metadata
  const orgId =
    typeof metadata?.org_id === 'string' && metadata.org_id.trim().length > 0
      ? metadata.org_id.trim()
      : null

  const orgName =
    typeof metadata?.org_name === 'string' && metadata.org_name.trim().length > 0
      ? metadata.org_name.trim()
      : defaultOrg.name

  return {
    id: orgId,
    name: orgName,
  }
}

export const useAuthStore = create<AuthStore>((set) => ({
  user: null,
  session: null,
  org: defaultOrg,
  setSession: (session) =>
    set(() => ({
      user: session.user,
      session,
      org: getOrgFromSession(session),
    })),
  clearSession: () =>
    set(() => ({
      user: null,
      session: null,
      org: defaultOrg,
    })),
}))
