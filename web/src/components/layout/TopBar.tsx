import { useNavigate } from 'react-router-dom'
import { supabase } from '../../lib/supabase'
import { useAuthStore } from '../../stores/authStore'
import { useUiStore } from '../../stores/uiStore'

export const TopBar = () => {
  const navigate = useNavigate()
  const org = useAuthStore((state) => state.org)
  const clearSession = useAuthStore((state) => state.clearSession)
  const sidebarOpen = useUiStore((state) => state.sidebarOpen)
  const setSidebarOpen = useUiStore((state) => state.setSidebarOpen)

  const handleSignOut = async () => {
    await supabase.auth.signOut()
    clearSession()
    navigate('/login', { replace: true })
  }

  return (
    <header className="flex h-20 items-center justify-between border-b border-[#1a243c] bg-[#0d1120] px-6">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => setSidebarOpen(!sidebarOpen)}
          className="rounded-md border border-[#2c4068] bg-[#10162a] px-3 py-2 text-xs font-semibold text-[#9fb2d8] transition hover:border-[#63d2ff]/60 hover:text-[#63d2ff]"
        >
          {sidebarOpen ? 'Hide menu' : 'Show menu'}
        </button>
        <div className="space-y-1">
        <p className="font-['Syne',sans-serif] text-xl font-semibold text-[#e6f6ff]">{org.name}</p>
        <p className="text-xs text-[#8598bf]">Organization context</p>
        </div>
      </div>

      <div className="flex items-center gap-3">
        <span className="rounded-full border border-[#2c4068] bg-[#10162a] px-3 py-1 text-xs text-[#8ca0c8]">
          2,841 bots
        </span>
        <span className="rounded-full border border-emerald-400/40 bg-emerald-500/10 px-3 py-1 text-xs text-emerald-300">
          Sigstore verified
        </span>
        <button
          type="button"
          onClick={handleSignOut}
          className="rounded-md border border-[#63d2ff]/60 bg-[#63d2ff]/10 px-3 py-2 text-xs font-semibold text-[#63d2ff] transition hover:bg-[#63d2ff]/20"
        >
          Sign out
        </button>
      </div>
    </header>
  )
}
