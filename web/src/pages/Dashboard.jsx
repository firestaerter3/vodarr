import { useState, useEffect } from 'react'

function useSyncStatus() {
  const [status, setStatus] = useState(null)
  const [error, setError] = useState(null)

  const fetch_ = () =>
    fetch('/api/status')
      .then(r => r.json())
      .then(setStatus)
      .catch(e => setError(e.message))

  useEffect(() => {
    fetch_()
    const id = setInterval(fetch_, 5000)
    return () => clearInterval(id)
  }, [])

  return { status, error, refetch: fetch_ }
}

function triggerSync() {
  return fetch('/api/sync', { method: 'POST' }).then(r => r.json())
}

function StatCard({ label, value, sub, accent, delay }) {
  return (
    <div
      className={`card-hover animate-fade-up animate-fade-up-${delay} border border-void-600 rounded-lg p-5 bg-void-800/60`}
    >
      <p className="font-mono text-[10px] text-steel-500 uppercase tracking-widest mb-3">{label}</p>
      <p
        className="font-mono text-3xl font-600 leading-none"
        style={{ color: accent ? 'var(--lime-400)' : 'var(--steel-300)' }}
      >
        {value ?? '—'}
      </p>
      {sub && <p className="mt-2 font-mono text-[11px] text-steel-500">{sub}</p>}
    </div>
  )
}

function SyncProgress({ progress }) {
  if (!progress || !progress.total) return null
  const pct = progress.total > 0 ? Math.round((progress.current / progress.total) * 100) : 0
  return (
    <div className="mt-4">
      <div className="flex justify-between mb-1.5">
        <span className="font-mono text-[11px] text-steel-400">{progress.stage}</span>
        <span className="font-mono text-[11px] text-lime-400">{pct}%</span>
      </div>
      <div className="h-1 bg-void-600 rounded-full overflow-hidden">
        <div
          className="progress-bar h-full rounded-full transition-all duration-300"
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1 font-mono text-[10px] text-steel-500">
        {progress.current.toLocaleString()} / {progress.total.toLocaleString()}
      </p>
    </div>
  )
}

function StatusBadge({ running, error }) {
  if (running) return (
    <span className="badge-running inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full font-mono text-[11px]">
      <span className="w-1.5 h-1.5 rounded-full bg-blue-400 pulse-dot" />
      SYNCING
    </span>
  )
  if (error) return (
    <span className="badge-error inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full font-mono text-[11px]">
      <span className="w-1.5 h-1.5 rounded-full bg-red-400" />
      ERROR
    </span>
  )
  return (
    <span className="badge-ok inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full font-mono text-[11px]">
      <span className="w-1.5 h-1.5 rounded-full bg-lime-400 pulse-dot" />
      READY
    </span>
  )
}

function formatTime(isoStr) {
  if (!isoStr || isoStr === '0001-01-01T00:00:00Z') return 'Never'
  try {
    return new Date(isoStr).toLocaleString(undefined, {
      month: 'short', day: 'numeric',
      hour: '2-digit', minute: '2-digit',
    })
  } catch { return isoStr }
}

export default function Dashboard() {
  const { status, error } = useSyncStatus()
  const [syncing, setSyncing] = useState(false)

  const handleSync = async () => {
    setSyncing(true)
    await triggerSync()
    setTimeout(() => setSyncing(false), 2000)
  }

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-start justify-between mb-8 animate-fade-up animate-fade-up-1">
        <div>
          <h1 className="font-display font-700 text-2xl text-steel-300 tracking-tight">
            System Status
          </h1>
          <p className="mt-1 font-mono text-[12px] text-steel-500">
            Xtream catalog sync &amp; indexer health
          </p>
        </div>
        <div className="flex items-center gap-3">
          {status && (
            <StatusBadge running={status.running} error={status.error} />
          )}
          <button
            onClick={handleSync}
            disabled={syncing || status?.running}
            className="flex items-center gap-2 px-4 py-2 rounded border border-lime-400/30 bg-lime-400/8 text-lime-400 font-mono text-[12px] hover:bg-lime-400/15 hover:border-lime-400/50 transition-all disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <SyncIcon spinning={syncing || status?.running} />
            Sync Now
          </button>
        </div>
      </div>

      {/* Stats grid */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        <StatCard
          label="Movies Indexed"
          value={status?.total_movies?.toLocaleString()}
          accent
          delay={1}
        />
        <StatCard
          label="Series Indexed"
          value={status?.total_series?.toLocaleString()}
          accent
          delay={2}
        />
        <StatCard
          label="Last Sync"
          value={status ? formatTime(status.last_sync) : '—'}
          delay={3}
        />
        <StatCard
          label="Next Sync"
          value={status ? formatTime(status.next_sync) : '—'}
          delay={4}
        />
      </div>

      {/* Sync progress */}
      {status?.running && (
        <div className="mb-8 border border-void-600 rounded-lg p-5 bg-void-800/60 animate-fade-up">
          <p className="font-mono text-[11px] text-steel-500 uppercase tracking-widest mb-1">
            Sync Progress
          </p>
          <SyncProgress progress={status.progress} />
        </div>
      )}

      {/* Error panel */}
      {status?.error && (
        <div className="mb-8 border border-red-500/30 rounded-lg p-5 bg-red-500/5 animate-fade-up">
          <p className="font-mono text-[11px] text-red-400/70 uppercase tracking-widest mb-2">
            Last Error
          </p>
          <p className="font-mono text-[13px] text-red-400">{status.error}</p>
        </div>
      )}

      {/* Setup guide */}
      <div className="animate-fade-up animate-fade-up-4">
        <h2 className="font-display font-600 text-sm text-steel-400 uppercase tracking-widest mb-4">
          Sonarr / Radarr Setup
        </h2>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <SetupCard
            title="Add as Indexer"
            steps={[
              'Settings → Indexers → Add',
              'Choose: Newznab (Custom)',
              'URL: http://<host>:9091',
              'Categories: 2000 (Movies), 5000 (TV)',
            ]}
          />
          <SetupCard
            title="Add as Download Client"
            steps={[
              'Settings → Download Clients → Add',
              'Choose: qBittorrent',
              'Host: <host>  Port: 9092',
              'No credentials required',
            ]}
          />
        </div>
      </div>
    </div>
  )
}

function SetupCard({ title, steps }) {
  return (
    <div className="card-hover border border-void-600 rounded-lg p-5 bg-void-800/60">
      <p className="font-display font-600 text-sm text-steel-300 mb-4">{title}</p>
      <ol className="space-y-2">
        {steps.map((step, i) => (
          <li key={i} className="flex items-start gap-3">
            <span className="font-mono text-[10px] text-lime-400/60 mt-0.5 flex-shrink-0 w-4">
              {String(i + 1).padStart(2, '0')}
            </span>
            <span className="font-mono text-[12px] text-steel-400">{step}</span>
          </li>
        ))}
      </ol>
    </div>
  )
}

function SyncIcon({ spinning }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 12 12"
      fill="none"
      style={{ animation: spinning ? 'spin 1s linear infinite' : 'none' }}
    >
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
      <path
        d="M10 6A4 4 0 112 6"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
      <path d="M10 3v3h-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  )
}
