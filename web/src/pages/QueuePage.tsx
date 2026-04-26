import { useCallback, useEffect, useMemo, useState } from 'react'
import { useAuthStore } from '../stores/authStore'

type QueueItem = {
  id: string
  status: string
  diff_content: string
  claude_summary: string
  created_at: string
  execution: {
    id: string
    status: string
  }
  bot: {
    name: string
    version: string
  }
  repo: {
    github_repo_full_name: string
  }
}

type QueueResponse = {
  items?: QueueItem[]
}

const getApiBaseURL = (): string => {
  const raw = import.meta.env.VITE_API_BASE_URL
  if (typeof raw === 'string' && raw.trim().length >= 8) {
    return raw.trim().replace(/\/+$/, '')
  }
  return ''
}

const formatRelativeTime = (isoTime: string): string => {
  const parsed = new Date(isoTime)
  if (Number.isNaN(parsed.getTime())) {
    return 'just now'
  }
  const seconds = Math.max(0, Math.floor((Date.now() - parsed.getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

const buildAPIURL = (path: string): string => {
  const baseURL = getApiBaseURL()
  const target = new URL(`${baseURL}${path}`, window.location.origin)
  return target.toString()
}

type DiffViewerProps = {
  diff: string
}

const DiffViewer = ({ diff }: DiffViewerProps) => {
  const lines = diff.split('\n')
  return (
    <pre className="max-h-80 overflow-auto rounded-xl border border-slate-700/60 bg-[#0b1220] p-4 text-xs leading-5 text-slate-100">
      {lines.map((line, index) => {
        const lineClass = line.startsWith('+')
          ? 'text-emerald-300'
          : line.startsWith('-')
            ? 'text-rose-300'
            : line.startsWith('@@')
              ? 'text-cyan-300'
              : 'text-slate-300'
        return (
          <span className={`${lineClass} block`} key={`${line}-${index}`}>
            {line}
          </span>
        )
      })}
    </pre>
  )
}

type ToastState = {
  kind: 'success' | 'error'
  message: string
} | null

export const QueuePage = () => {
  const session = useAuthStore((state) => state.session)
  const [items, setItems] = useState<QueueItem[]>([])
  const [loading, setLoading] = useState<boolean>(true)
  const [errorMessage, setErrorMessage] = useState<string>('')
  const [toast, setToast] = useState<ToastState>(null)
  const [pendingActionByID, setPendingActionByID] = useState<Record<string, boolean>>({})

  const accessToken = useMemo(() => {
    const raw = session?.access_token
    if (typeof raw !== 'string') return ''
    const token = raw.trim()
    if (token.length < 20 || token.length > 4096) return ''
    return token
  }, [session?.access_token])

  const fetchQueue = useCallback(async () => {
    if (!accessToken) {
      setItems([])
      setLoading(false)
      return
    }
    setLoading(true)
    setErrorMessage('')
    try {
      const response = await fetch(buildAPIURL('/api/v1/queue'), {
        method: 'GET',
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      })
      if (!response.ok) {
        throw new Error('Unable to load review queue')
      }
      const payload = (await response.json()) as QueueResponse
      const nextItems = Array.isArray(payload.items) ? payload.items : []
      setItems(nextItems.filter((item) => typeof item.id === 'string' && item.id.trim().length > 0))
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unable to load review queue'
      setErrorMessage(message)
    } finally {
      setLoading(false)
    }
  }, [accessToken])

  useEffect(() => {
    void fetchQueue()
  }, [fetchQueue])

  useEffect(() => {
    const handleFocus = () => {
      void fetchQueue()
    }
    window.addEventListener('focus', handleFocus)
    return () => window.removeEventListener('focus', handleFocus)
  }, [fetchQueue])

  useEffect(() => {
    if (!toast) {
      return
    }
    const timeout = window.setTimeout(() => setToast(null), 5000)
    return () => window.clearTimeout(timeout)
  }, [toast])

  const updatePendingAction = (id: string, pending: boolean) => {
    setPendingActionByID((previous) => ({ ...previous, [id]: pending }))
  }

  const handleApprove = async (item: QueueItem) => {
    if (!accessToken) {
      setToast({ kind: 'error', message: 'Session expired. Please sign in again.' })
      return
    }
    updatePendingAction(item.id, true)
    try {
      const response = await fetch(buildAPIURL(`/api/v1/queue/${item.id}/approve`), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      })
      if (!response.ok) {
        throw new Error('Approve action failed')
      }
      const payload = (await response.json()) as { pr_url?: string }
      const prURL = typeof payload.pr_url === 'string' ? payload.pr_url.trim() : ''
      setItems((previous) => previous.filter((current) => current.id !== item.id))
      if (prURL.length > 0) {
        setToast({ kind: 'success', message: `PR created: ${prURL}` })
      } else {
        setToast({ kind: 'success', message: 'Review approved.' })
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Approve action failed'
      setToast({ kind: 'error', message })
    } finally {
      updatePendingAction(item.id, false)
    }
  }

  const handleReject = async (item: QueueItem) => {
    if (!accessToken) {
      setToast({ kind: 'error', message: 'Session expired. Please sign in again.' })
      return
    }
    updatePendingAction(item.id, true)
    try {
      const response = await fetch(buildAPIURL(`/api/v1/queue/${item.id}/reject`), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      })
      if (!response.ok) {
        throw new Error('Reject action failed')
      }
      setItems((previous) => previous.filter((current) => current.id !== item.id))
      setToast({ kind: 'success', message: 'Review rejected.' })
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Reject action failed'
      setToast({ kind: 'error', message })
    } finally {
      updatePendingAction(item.id, false)
    }
  }

  return (
    <section className="min-h-[calc(100vh-80px)] p-6">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-4">
        <header className="flex items-center justify-between">
          <div>
            <h1 className="font-['Syne',sans-serif] text-2xl font-bold text-[#e6f6ff]">Review Queue</h1>
            <p className="mt-1 text-sm text-slate-400">Approve or reject pending AI remediation changes.</p>
          </div>
          <button
            className="rounded-lg border border-cyan-400/40 px-3 py-2 text-sm font-medium text-cyan-200 hover:bg-cyan-400/10"
            onClick={() => void fetchQueue()}
            type="button"
          >
            Refresh
          </button>
        </header>

        {toast ? (
          <div
            className={`rounded-lg p-3 text-sm ${
              toast.kind === 'success'
                ? 'border border-emerald-400/40 bg-emerald-500/10 text-emerald-100'
                : 'border border-rose-400/40 bg-rose-500/10 text-rose-100'
            }`}
          >
            {toast.message}
          </div>
        ) : null}

        {errorMessage ? (
          <div className="rounded-lg border border-rose-400/40 bg-rose-500/10 p-3 text-sm text-rose-100">
            {errorMessage}
          </div>
        ) : null}

        {loading ? (
          <p className="rounded-xl border border-slate-700/60 bg-slate-900/40 p-4 text-sm text-slate-300">
            Loading pending reviews...
          </p>
        ) : null}

        {!loading && items.length === 0 ? (
          <p className="rounded-xl border border-emerald-400/40 bg-emerald-500/10 p-5 text-sm text-emerald-100">
            No pending reviews — the network is clean ✓
          </p>
        ) : null}

        {!loading &&
          items.map((item) => {
            const pending = pendingActionByID[item.id] === true
            return (
              <article
                className="rounded-2xl border border-slate-700/60 bg-slate-950/70 p-5 shadow-sm"
                key={item.id}
              >
                <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <p className="text-sm font-semibold text-slate-100">{item.bot.name}</p>
                    <p className="text-xs text-slate-400">
                      {item.repo.github_repo_full_name} · {item.bot.version}
                    </p>
                  </div>
                  <span className="rounded-full bg-amber-500/20 px-2 py-1 text-xs font-medium text-amber-100 ring-1 ring-amber-300/40">
                    {item.status}
                  </span>
                </div>

                <p className="mb-3 text-xs text-slate-400">Queued {formatRelativeTime(item.created_at)}</p>
                <p className="mb-4 rounded-xl border border-slate-700/60 bg-slate-900/50 p-4 text-sm text-slate-200">
                  {item.claude_summary}
                </p>
                <DiffViewer diff={item.diff_content} />

                <div className="mt-4 flex flex-wrap gap-2">
                  <button
                    className="rounded-lg bg-emerald-500/20 px-4 py-2 text-sm font-semibold text-emerald-100 ring-1 ring-emerald-300/40 hover:bg-emerald-500/30 disabled:opacity-60"
                    disabled={pending}
                    onClick={() => void handleApprove(item)}
                    type="button"
                  >
                    Approve
                  </button>
                  <button
                    className="rounded-lg bg-rose-500/20 px-4 py-2 text-sm font-semibold text-rose-100 ring-1 ring-rose-300/40 hover:bg-rose-500/30 disabled:opacity-60"
                    disabled={pending}
                    onClick={() => void handleReject(item)}
                    type="button"
                  >
                    Reject
                  </button>
                </div>
              </article>
            )
          })}
      </div>
    </section>
  )
}
