import { Link } from 'react-router-dom'

export const NotFoundPage = () => (
  <main className="flex min-h-screen items-center justify-center bg-[#060810] px-6 text-[#e6f6ff]">
    <section className="rounded-xl border border-[#1a243c] bg-[#0d1120] px-8 py-10 text-center">
      <h1 className="font-['Syne',sans-serif] text-4xl font-bold">404</h1>
      <p className="mt-3 text-[#9fb2d8]">Page not found</p>
      <Link
        to="/"
        className="mt-6 inline-flex rounded-md border border-[#63d2ff]/60 bg-[#63d2ff]/10 px-4 py-2 text-sm font-semibold text-[#63d2ff] transition hover:bg-[#63d2ff]/20"
      >
        Back home
      </Link>
    </section>
  </main>
)
