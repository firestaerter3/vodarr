import { useState, useEffect, useMemo } from 'react'

function useContent(type) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    fetch(`/api/content/${type}`)
      .then(r => r.json())
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [type])

  return { items: data?.items ?? [], total: data?.total ?? 0, loading }
}

function MatchBadge({ item }) {
  const hasIMDB = !!item.IMDBId
  const hasTVDB = !!item.TVDBId
  const hasTMDB = !!item.TMDBId

  if (hasIMDB || hasTVDB) return (
    <span className="badge-ok inline-flex items-center gap-1 px-2 py-0.5 rounded font-mono text-[10px]">
      <span className="w-1 h-1 rounded-full bg-lime-400" />
      MATCHED
    </span>
  )
  if (hasTMDB) return (
    <span className="badge-running inline-flex items-center gap-1 px-2 py-0.5 rounded font-mono text-[10px]">
      <span className="w-1 h-1 rounded-full bg-blue-400" />
      TMDB
    </span>
  )
  return (
    <span className="badge-idle inline-flex items-center gap-1 px-2 py-0.5 rounded font-mono text-[10px]">
      <span className="w-1 h-1 rounded-full bg-steel-500" />
      UNMATCHED
    </span>
  )
}

function IDChip({ label, value }) {
  if (!value) return null
  return (
    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-void-600 font-mono text-[10px] text-steel-400">
      <span className="text-steel-500">{label}:</span>
      {value}
    </span>
  )
}

export default function Content() {
  const [tab, setTab] = useState('movies')
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [page, setPage] = useState(1)
  const PAGE_SIZE = 50

  const { items, total, loading } = useContent(tab)

  const filtered = useMemo(() => {
    let list = items
    if (query) {
      const q = query.toLowerCase()
      list = list.filter(i => i.Name?.toLowerCase().includes(q))
    }
    // Movies: TMDB alone is sufficient for Radarr to match.
    // Series: need IMDB or TVDB (Sonarr doesn't match on TMDB).
    const isMatched = i => tab === 'movies'
      ? (i.IMDBId || i.TVDBId || i.TMDBId)
      : (i.IMDBId || i.TVDBId)
    if (filter === 'matched') list = list.filter(isMatched)
    if (filter === 'unmatched') list = list.filter(i => !isMatched(i))
    return list
  }, [items, query, filter, tab])

  const pageItems = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)
  const totalPages = Math.ceil(filtered.length / PAGE_SIZE)

  const isMatched = i => tab === 'movies'
    ? (i.IMDBId || i.TVDBId || i.TMDBId)
    : (i.IMDBId || i.TVDBId)
  const matchedCount = items.filter(isMatched).length
  const matchRate = items.length > 0 ? Math.round((matchedCount / items.length) * 100) : 0

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-start justify-between mb-6 animate-fade-up animate-fade-up-1">
        <div>
          <h1 className="font-display font-700 text-2xl text-steel-300 tracking-tight">
            Content Browser
          </h1>
          <p className="mt-1 font-mono text-[12px] text-steel-500">
            Indexed Xtream catalog with ID match status
          </p>
        </div>
        {items.length > 0 && (
          <div className="text-right">
            <p className="font-mono text-[11px] text-steel-500">Match rate</p>
            <p className="font-mono text-xl font-600 text-lime-400">{matchRate}%</p>
          </div>
        )}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-6 animate-fade-up animate-fade-up-2">
        {[
          { id: 'movies', label: 'Movies' },
          { id: 'series', label: 'Series' },
        ].map(({ id, label }) => (
          <button
            key={id}
            onClick={() => { setTab(id); setPage(1); setQuery('') }}
            className={[
              'px-4 py-2 rounded font-display font-500 text-sm transition-all',
              tab === id
                ? 'bg-lime-400/10 text-lime-400 border border-lime-400/20'
                : 'text-steel-400 border border-transparent hover:text-steel-300 hover:bg-void-700',
            ].join(' ')}
          >
            {label}
            {!loading && (
              <span className="ml-2 font-mono text-[10px] opacity-60">
                ({tab === id ? filtered.length.toLocaleString() : '—'})
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Toolbar */}
      <div className="flex gap-3 mb-4 animate-fade-up animate-fade-up-3">
        <div className="relative flex-1 max-w-sm">
          <svg className="absolute left-3 top-1/2 -translate-y-1/2 text-steel-500" width="12" height="12" viewBox="0 0 12 12" fill="none">
            <circle cx="5" cy="5" r="4" stroke="currentColor" strokeWidth="1.5"/>
            <path d="M10 10L8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
          </svg>
          <input
            type="text"
            placeholder="Filter by title..."
            value={query}
            onChange={e => { setQuery(e.target.value); setPage(1) }}
            className="w-full pl-8 pr-4 py-2 bg-void-800 border border-void-600 rounded font-mono text-[12px] text-steel-300 placeholder-steel-500"
          />
        </div>
        <select
          value={filter}
          onChange={e => { setFilter(e.target.value); setPage(1) }}
          className="px-3 py-2 bg-void-800 border border-void-600 rounded font-mono text-[12px] text-steel-400"
        >
          <option value="all">All</option>
          <option value="matched">Matched</option>
          <option value="unmatched">Unmatched</option>
        </select>
      </div>

      {/* Table */}
      <div className="border border-void-600 rounded-lg overflow-hidden animate-fade-up animate-fade-up-4">
        {/* Table header */}
        <div className="grid grid-cols-[1fr_auto_auto_auto] gap-4 px-4 py-2.5 bg-void-700/50 border-b border-void-600">
          <p className="font-mono text-[10px] text-steel-500 uppercase tracking-widest">Title</p>
          <p className="font-mono text-[10px] text-steel-500 uppercase tracking-widest">Year</p>
          <p className="font-mono text-[10px] text-steel-500 uppercase tracking-widest">IDs</p>
          <p className="font-mono text-[10px] text-steel-500 uppercase tracking-widest">Match</p>
        </div>

        {/* Rows */}
        {loading ? (
          <div className="px-4 py-12 text-center">
            <p className="font-mono text-[12px] text-steel-500 animate-pulse">Loading catalog…</p>
          </div>
        ) : pageItems.length === 0 ? (
          <div className="px-4 py-12 text-center">
            <p className="font-mono text-[12px] text-steel-500">
              {query ? 'No results for that query.' : 'No items indexed yet — run a sync first.'}
            </p>
          </div>
        ) : (
          <div className="divide-y divide-void-600/50">
            {pageItems.map((item, i) => (
              <div
                key={`${item.XtreamID}-${i}`}
                className="table-row grid grid-cols-[1fr_auto_auto_auto] gap-4 px-4 py-2.5 items-center"
              >
                <p className="font-display font-500 text-[13px] text-steel-300 truncate">
                  {item.Name}
                </p>
                <p className="font-mono text-[11px] text-steel-500 w-10 text-right">
                  {item.Year || '—'}
                </p>
                <div className="flex gap-1.5 flex-wrap justify-end min-w-0">
                  <IDChip label="imdb" value={item.IMDBId} />
                  <IDChip label="tvdb" value={item.TVDBId} />
                  <IDChip label="tmdb" value={item.TMDBId} />
                </div>
                <div className="flex justify-end">
                  <MatchBadge item={item} />
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between mt-4">
          <p className="font-mono text-[11px] text-steel-500">
            {((page - 1) * PAGE_SIZE + 1).toLocaleString()}–
            {Math.min(page * PAGE_SIZE, filtered.length).toLocaleString()} of {filtered.length.toLocaleString()}
          </p>
          <div className="flex gap-1">
            <PageBtn disabled={page === 1} onClick={() => setPage(p => p - 1)}>←</PageBtn>
            <span className="px-3 py-1.5 font-mono text-[11px] text-steel-400">
              {page} / {totalPages}
            </span>
            <PageBtn disabled={page === totalPages} onClick={() => setPage(p => p + 1)}>→</PageBtn>
          </div>
        </div>
      )}
    </div>
  )
}

function PageBtn({ children, disabled, onClick }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="px-3 py-1.5 border border-void-600 rounded font-mono text-[12px] text-steel-400 hover:text-steel-300 hover:border-void-500 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
    >
      {children}
    </button>
  )
}
