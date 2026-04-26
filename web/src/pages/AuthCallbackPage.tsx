import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { supabase } from '../lib/supabase'
import { useAuthStore } from '../stores/authStore'

type HashSessionTokens = {
  accessToken: string
  refreshToken: string
}

const parseHashSessionTokens = (hash: string): HashSessionTokens | null => {
  const fragment = hash.startsWith('#') ? hash.slice(1) : hash
  const params = new URLSearchParams(fragment)
  const accessToken = params.get('access_token')
  const refreshToken = params.get('refresh_token')

  if (!accessToken || !refreshToken) {
    return null
  }

  if (accessToken.length < 20 || refreshToken.length < 20) {
    return null
  }

  return {
    accessToken,
    refreshToken,
  }
}

export const AuthCallbackPage = () => {
  const navigate = useNavigate()
  const setSession = useAuthStore((state) => state.setSession)
  const clearSession = useAuthStore((state) => state.clearSession)

  useEffect(() => {
    const handleCallback = async () => {
      const hashTokens = parseHashSessionTokens(window.location.hash)

      if (hashTokens) {
        const { data, error } = await supabase.auth.setSession({
          access_token: hashTokens.accessToken,
          refresh_token: hashTokens.refreshToken,
        })

        if (!error && data.session) {
          setSession(data.session)
          navigate('/dashboard', { replace: true })
          return
        }
      }

      const { data, error } = await supabase.auth.exchangeCodeForSession(window.location.href)
      if (!error && data.session) {
        setSession(data.session)
        navigate('/dashboard', { replace: true })
        return
      }

      clearSession()
      navigate('/login', { replace: true })
    }

    void handleCallback()
  }, [clearSession, navigate, setSession])

  return (
    <main className="flex min-h-screen items-center justify-center bg-[#060810] px-6 text-[#e6f6ff]">
      <p className="text-sm text-[#9fb2d8]">Finalizing sign-in...</p>
    </main>
  )
}
