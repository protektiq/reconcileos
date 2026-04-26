import { useEffect, useMemo, useState } from 'react'
import { useAuthStore } from '../stores/authStore'

type StreamEventType = 'execution_update' | 'review_required' | 'attestation_issued'

type StreamPayload = {
  id?: string
  status?: string
  bot?: { name?: string; version?: string }
  repo?: { github_repo_full_name?: string }
  created_at?: string
  signed_at?: string
}

type FeedEvent = {
  id: string
  eventType: StreamEventType
  status: string
  botName: string
  repoName: string
  occurredAt: string
  actionText: string
}

const kpiItems = [
  { label: 'Bots Running', value: '12' },
  { label: 'CVEs Closed (30d)', value: '147' },
  { label: 'Attestations Signed', value: '1,982' },
  { label: 'Drift Events (24h)', value: '9' },
  { label: 'Open Work Items', value: '34' },
]

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

const toFeedEvent = (eventType: StreamEventType, payload: StreamPayload): FeedEvent | null => {
  const cleanID = typeof payload.id === 'string' ? payload.id.trim() : ''
  if (cleanID.length === 0 || cleanID.length > 128) {
    return null
  }
  const botName =
    typeof payload.bot?.name === 'string' && payload.bot.name.trim().length > 0
      ? payload.bot.name.trim()
      : 'Unknown bot'
  const repoName =
    typeof payload.repo?.github_repo_full_name === 'string' && payload.repo.github_repo_full_name.trim().length > 0
      ? payload.repo.github_repo_full_name.trim()
      : 'Unknown repo'
  const status =
    typeof payload.status === 'string' && payload.status.trim().length > 0 ? payload.status.trim() : 'updated'
  const occurredAtRaw =
    typeof payload.created_at === 'string'
      ? payload.created_at
      : typeof payload.signed_at === 'string'
        ? payload.signed_at
        : new Date().toISOString()
  const actionTextByType: Record<StreamEventType, string> = {
    execution_update: 'Execution status changed',
    review_required: 'Manual review required',
    attestation_issued: 'New attestation issued',
  }

  return {
    id: `${eventType}-${cleanID}-${occurredAtRaw}`,
    eventType,
    status,
    botName,
    repoName,
    occurredAt: occurredAtRaw,
    actionText: actionTextByType[eventType],
  }
}

const statusBadgeClass = (status: string): string => {
  const normalized = status.toLowerCase()
  if (normalized === 'completed' || normalized === 'approved') {
    return 'bg-emerald-500/20 text-emerald-200 ring-1 ring-emerald-300/40'
  }
  if (normalized === 'failed' || normalized === 'rejected') {
    return 'bg-rose-500/20 text-rose-200 ring-1 ring-rose-300/40'
  }
  if (normalized === 'pending') {
    return 'bg-amber-500/20 text-amber-100 ring-1 ring-amber-300/40'
  }
  return 'bg-slate-500/20 text-slate-100 ring-1 ring-slate-300/40'
}

const eventIcon = (eventType: StreamEventType): string => {
  if (eventType === 'execution_update') return '⚙'
  if (eventType === 'review_required') return '🛡'
  return '✍'
}

type FeedItemProps = {
  event: FeedEvent
}

const FeedItem = ({ event }: FeedItemProps) => (
  <article className="flex items-start gap-3 rounded-xl border border-slate-700/60 bg-slate-900/60 p-4 shadow-sm">
    <div className="mt-0.5 rounded-lg bg-slate-800 px-2 py-1 text-sm">{eventIcon(event.eventType)}</div>
    <div className="min-w-0 flex-1">
      <div className="flex flex-wrap items-center gap-2">
        <p className="font-semibold text-slate-100">{event.actionText}</p>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusBadgeClass(event.status)}`}>
          {event.status}
        </span>
        <span className="text-xs text-slate-400">{formatRelativeTime(event.occurredAt)}</span>
      </div>
      <p className="mt-1 text-sm text-slate-300">
        <span className="font-medium text-slate-200">{event.botName}</span> on{' '}
        <span className="font-medium text-slate-200">{event.repoName}</span>
      </p>
    </div>
  </article>
)

export const DashboardPage = () => {
  const session = useAuthStore((state) => state.session)
  const [feedEvents, setFeedEvents] = useState<FeedEvent[]>([])

  const streamURL = useMemo(() => {
    const token = typeof session?.access_token === 'string' ? session.access_token.trim() : ''
    if (token.length < 20 || token.length > 4096) {
      return null
    }
    const baseURL = getApiBaseURL()
    const targetPath = `${baseURL}/api/v1/stream`
    const target = new URL(targetPath, window.location.origin)
    target.searchParams.set('token', token)
    return target.toString()
  }, [session?.access_token])

  useEffect(() => {
    if (!streamURL) {
      return
    }
    const source = new EventSource(streamURL)
    const handleEvent = (eventType: StreamEventType) => (event: MessageEvent<string>) => {
      if (typeof event.data !== 'string' || event.data.length === 0 || event.data.length > 300_000) {
        return
      }
      let parsed: StreamPayload
      try {
        parsed = JSON.parse(event.data) as StreamPayload
      } catch {
        return
      }
      const mapped = toFeedEvent(eventType, parsed)
      if (!mapped) {
        return
      }
      setFeedEvents((previous) => [mapped, ...previous].slice(0, 100))
    }

    const executionListener = handleEvent('execution_update')
    const reviewListener = handleEvent('review_required')
    const attestationListener = handleEvent('attestation_issued')
    source.addEventListener('execution_update', executionListener)
    source.addEventListener('review_required', reviewListener)
    source.addEventListener('attestation_issued', attestationListener)

    return () => {
      source.removeEventListener('execution_update', executionListener)
      source.removeEventListener('review_required', reviewListener)
      source.removeEventListener('attestation_issued', attestationListener)
      source.close()
    }
  }, [streamURL])

  return (
    <section className="min-h-[calc(100vh-80px)] p-6">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
        <header className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
          {kpiItems.map((item) => (
            <article
              key={item.label}
              className="rounded-xl border border-cyan-500/20 bg-slate-900/70 p-4 shadow-[0_0_0_1px_rgba(6,182,212,0.05)]"
            >
              <p className="text-xs uppercase tracking-wide text-slate-400">{item.label}</p>
              <p className="mt-2 text-2xl font-bold text-slate-100">{item.value}</p>
            </article>
          ))}
        </header>

        <div className="rounded-2xl border border-slate-700/60 bg-slate-950/70 p-5">
          <h1 className="font-['Syne',sans-serif] text-2xl font-bold text-[#e6f6ff]">Live Feed</h1>
          <p className="mt-1 text-sm text-slate-400">Realtime execution, review, and attestation updates.</p>
          <div className="mt-5 flex flex-col gap-3">
            {feedEvents.length === 0 ? (
              <p className="rounded-xl border border-slate-700/60 bg-slate-900/40 p-4 text-sm text-slate-400">
                Waiting for live events...
              </p>
            ) : (
              feedEvents.map((event) => <FeedItem key={event.id} event={event} />)
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
