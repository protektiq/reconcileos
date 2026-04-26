import { NavLink } from 'react-router-dom'
import { useAuthStore } from '../../stores/authStore'

type NavItem = {
  to: string
  label: string
}

const navItems: NavItem[] = [
  { to: '/dashboard', label: 'Dashboard' },
  { to: '/marketplace', label: 'Marketplace' },
  { to: '/queue', label: 'Queue' },
  { to: '/attestations', label: 'Attestations' },
]

const NavItemLink = ({ to, label }: NavItem) => (
  <NavLink
    to={to}
    className={({ isActive }) =>
      `block rounded-md border px-3 py-2 text-sm transition ${
        isActive
          ? 'border-[#63d2ff]/70 bg-[#63d2ff]/15 text-[#63d2ff]'
          : 'border-transparent text-[#9fb2d8] hover:border-[#27385d] hover:bg-[#11182d]'
      }`
    }
  >
    {label}
  </NavLink>
)

export const Sidebar = () => {
  const user = useAuthStore((state) => state.user)
  const org = useAuthStore((state) => state.org)

  return (
    <aside className="flex h-full w-64 shrink-0 flex-col border-r border-[#1a243c] bg-[#0d1120] p-4">
      <div className="rounded-lg border border-[#1e2a49] bg-[#10162a] px-3 py-4">
        <p className="font-['Syne',sans-serif] text-lg font-bold text-[#63d2ff]">ReconcileOS</p>
        <p className="mt-1 text-xs text-[#7f8fb4]">Control shell</p>
      </div>

      <nav className="mt-6 flex flex-1 flex-col gap-2">
        {navItems.map((item) => (
          <NavItemLink key={item.to} to={item.to} label={item.label} />
        ))}
      </nav>

      <div className="mt-4 rounded-lg border border-[#1e2a49] bg-[#10162a] p-3">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-full border border-[#63d2ff]/40 bg-[#63d2ff]/10 text-sm font-semibold text-[#63d2ff]">
            {user?.email?.slice(0, 1).toUpperCase() ?? 'U'}
          </div>
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-[#d5e6ff]">{org.name}</p>
            <p className="truncate text-xs text-[#8598bf]">{user?.email ?? 'No user signed in'}</p>
          </div>
        </div>
      </div>
    </aside>
  )
}
