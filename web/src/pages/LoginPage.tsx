import { useState } from 'react'
import { supabase } from '../lib/supabase'

export const LoginPage = () => {
  const [isLoading, setIsLoading] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  const handleSignIn = async () => {
    setIsLoading(true)
    setErrorMessage('')

    const redirectTo = `${window.location.origin}/auth/callback`
    const { error } = await supabase.auth.signInWithOAuth({
      provider: 'github',
      options: { redirectTo },
    })

    if (error) {
      setErrorMessage(error.message)
      setIsLoading(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-[#060810] px-6 text-[#e6f6ff]">
      <section className="w-full max-w-md rounded-xl border border-[#1a243c] bg-[#0d1120] p-8 shadow-lg shadow-black/40">
        <h1 className="font-['Syne',sans-serif] text-3xl font-bold">ReconcileOS</h1>
        <p className="mt-3 text-sm text-[#9fb2d8]">Sign in to continue.</p>
        <button
          type="button"
          onClick={handleSignIn}
          disabled={isLoading}
          className="mt-8 w-full rounded-md border border-[#63d2ff]/60 bg-[#63d2ff]/10 px-4 py-3 font-semibold text-[#63d2ff] transition hover:bg-[#63d2ff]/20 focus:outline-none focus:ring-2 focus:ring-[#63d2ff] disabled:cursor-not-allowed disabled:opacity-70"
        >
          {isLoading ? 'Redirecting...' : 'Sign in with GitHub'}
        </button>
        {errorMessage.length > 0 ? (
          <p className="mt-4 text-sm text-red-300" role="alert">
            {errorMessage}
          </p>
        ) : null}
      </section>
    </main>
  )
}
