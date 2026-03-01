import { useState, useEffect } from 'react'

const DEFAULT = {
  xtream: { url: '', username: '', password: '' },
  tmdb: { api_key: '' },
  output: { path: '/data/strm', movies_dir: 'movies', series_dir: 'tv' },
  sync: { interval: '6h', on_startup: true, parallelism: 10, title_cleanup_patterns: [] },
  server: { newznab_port: 9091, qbit_port: 9092, web_port: 9090 },
  logging: { level: 'info' },
}

function Field({ label, hint, children }) {
  return (
    <div>
      <label className="block font-mono text-[11px] text-steel-400 uppercase tracking-widest mb-1.5">
        {label}
      </label>
      {children}
      {hint && <p className="mt-1 font-mono text-[10px] text-steel-500">{hint}</p>}
    </div>
  )
}

function TextInput({ value, onChange, type = 'text', placeholder, monospace }) {
  return (
    <input
      type={type}
      value={value}
      onChange={e => onChange(e.target.value)}
      placeholder={placeholder}
      className={[
        'w-full px-3 py-2 bg-void-800 border border-void-600 rounded text-steel-300 text-[13px]',
        monospace ? 'font-mono' : 'font-display',
      ].join(' ')}
    />
  )
}

function Toggle({ value, onChange }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!value)}
      className={[
        'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
        value ? 'bg-lime-400/40 border border-lime-400/40' : 'bg-void-600 border border-void-500',
      ].join(' ')}
    >
      <span
        className={[
          'inline-block h-3.5 w-3.5 transform rounded-full transition-transform',
          value ? 'translate-x-4 bg-lime-400' : 'translate-x-1 bg-steel-500',
        ].join(' ')}
      />
    </button>
  )
}

function Section({ title, children }) {
  return (
    <div className="border border-void-600 rounded-lg overflow-hidden bg-void-800/60">
      <div className="px-5 py-3 border-b border-void-600 bg-void-700/40">
        <h2 className="font-display font-600 text-sm text-steel-300 uppercase tracking-widest">
          {title}
        </h2>
      </div>
      <div className="p-5 space-y-5">
        {children}
      </div>
    </div>
  )
}

function TestButton({ onClick, loading, success, error }) {
  return (
    <div className="flex items-center gap-3 pt-1">
      <button
        type="button"
        onClick={onClick}
        disabled={loading}
        className="px-4 py-1.5 bg-void-600 border border-void-500 text-steel-400 rounded font-mono text-[12px] hover:bg-void-500 hover:text-steel-300 transition-all disabled:opacity-40"
      >
        {loading ? 'Testing…' : 'Test Connection'}
      </button>
      {success && (
        <span className="font-mono text-[12px] text-lime-400 animate-fade-up">✓ Connected</span>
      )}
      {error && (
        <span className="font-mono text-[12px] text-red-400 animate-fade-up">{error}</span>
      )}
    </div>
  )
}

export default function Settings() {
  const [cfg, setCfg] = useState(DEFAULT)
  const [patternsText, setPatternsText] = useState('')
  const [saved, setSaved] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState(null)
  const [loadError, setLoadError] = useState(null)
  const [restartRequired, setRestartRequired] = useState(false)

  const [xtreamTest, setXtreamTest] = useState({ loading: false, success: false, error: null })
  const [tmdbTest, setTmdbTest] = useState({ loading: false, success: false, error: null })

  useEffect(() => {
    fetch('/api/config')
      .then(r => r.json())
      .then(data => {
        setCfg(prev => deepMerge(prev, data))
        setPatternsText((data.sync?.title_cleanup_patterns || []).join('\n'))
      })
      .catch(e => setLoadError(e.message))
  }, [])

  // Build the full config payload, parsing patternsText into an array.
  const buildPayload = () => ({
    ...cfg,
    sync: {
      ...cfg.sync,
      title_cleanup_patterns: patternsText.split('\n').filter(l => l.trim()),
    },
  })

  const set = (path, value) => {
    setCfg(prev => {
      const next = structuredClone(prev)
      const parts = path.split('.')
      let obj = next
      for (let i = 0; i < parts.length - 1; i++) obj = obj[parts[i]]
      obj[parts[parts.length - 1]] = value
      return next
    })
  }

  const saveConfig = async (currentCfg) => {
    setSaving(true)
    setSaveError(null)
    try {
      const res = await fetch('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(currentCfg),
      })
      const data = await res.json()
      if (data.error) {
        setSaveError(data.error)
      } else {
        setSaved(true)
        setTimeout(() => setSaved(false), 3000)
        if (data.restart_required) {
          setRestartRequired(true)
        }
      }
    } catch (e) {
      setSaveError(e.message)
    }
    setSaving(false)
  }

  const handleSave = async e => {
    e.preventDefault()
    await saveConfig(buildPayload())
  }

  const testXtream = async () => {
    setXtreamTest({ loading: true, success: false, error: null })
    try {
      const res = await fetch('/api/test-xtream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg.xtream),
      })
      const data = await res.json()
      if (data.success) {
        setXtreamTest({ loading: false, success: true, error: null })
        setTimeout(() => setXtreamTest(s => ({ ...s, success: false })), 5000)
        await saveConfig(buildPayload())
      } else {
        setXtreamTest({ loading: false, success: false, error: data.error || 'Connection failed' })
        setTimeout(() => setXtreamTest(s => ({ ...s, error: null })), 5000)
      }
    } catch (e) {
      setXtreamTest({ loading: false, success: false, error: e.message })
      setTimeout(() => setXtreamTest(s => ({ ...s, error: null })), 5000)
    }
  }

  const testTMDB = async () => {
    setTmdbTest({ loading: true, success: false, error: null })
    try {
      const res = await fetch('/api/test-tmdb', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg.tmdb),
      })
      const data = await res.json()
      if (data.success) {
        setTmdbTest({ loading: false, success: true, error: null })
        setTimeout(() => setTmdbTest(s => ({ ...s, success: false })), 5000)
        await saveConfig(buildPayload())
      } else {
        setTmdbTest({ loading: false, success: false, error: data.error || 'Connection failed' })
        setTimeout(() => setTmdbTest(s => ({ ...s, error: null })), 5000)
      }
    } catch (e) {
      setTmdbTest({ loading: false, success: false, error: e.message })
      setTimeout(() => setTmdbTest(s => ({ ...s, error: null })), 5000)
    }
  }

  return (
    <div className="p-8">
      <div className="mb-8 animate-fade-up animate-fade-up-1">
        <h1 className="font-display font-700 text-2xl text-steel-300 tracking-tight">Settings</h1>
        <p className="mt-1 font-mono text-[12px] text-steel-500">
          Configuration is applied on save — credentials are stored server-side only
        </p>
        {loadError && (
          <p className="mt-2 font-mono text-[11px] text-yellow-400/80">
            Note: Could not load current config ({loadError}). Showing defaults.
          </p>
        )}
        {restartRequired && (
          <div className="mt-3 px-4 py-2.5 bg-yellow-400/10 border border-yellow-400/30 rounded font-mono text-[12px] text-yellow-400">
            Restart required — changes take effect after restarting VODarr.
          </div>
        )}
      </div>

      <form onSubmit={handleSave} className="space-y-5 max-w-2xl">

        {/* Xtream */}
        <div className="animate-fade-up animate-fade-up-1">
          <Section title="Xtream Provider">
            <Field label="Server URL" hint="Base URL of your Xtream provider, no trailing slash">
              <TextInput
                value={cfg.xtream.url}
                onChange={v => set('xtream.url', v)}
                placeholder="http://provider.example.com"
                monospace
              />
            </Field>
            <div className="grid grid-cols-2 gap-4">
              <Field label="Username">
                <TextInput value={cfg.xtream.username} onChange={v => set('xtream.username', v)} monospace />
              </Field>
              <Field label="Password">
                <TextInput value={cfg.xtream.password} onChange={v => set('xtream.password', v)} type="password" monospace />
              </Field>
            </div>
            <TestButton
              onClick={testXtream}
              loading={xtreamTest.loading}
              success={xtreamTest.success}
              error={xtreamTest.error}
            />
          </Section>
        </div>

        {/* TMDB */}
        <div className="animate-fade-up animate-fade-up-2">
          <Section title="TMDB">
            <Field label="API Key" hint="Get your free key at themoviedb.org/settings/api">
              <TextInput
                value={cfg.tmdb.api_key}
                onChange={v => set('tmdb.api_key', v)}
                type="password"
                placeholder="••••••••••••••••"
                monospace
              />
            </Field>
            <TestButton
              onClick={testTMDB}
              loading={tmdbTest.loading}
              success={tmdbTest.success}
              error={tmdbTest.error}
            />
          </Section>
        </div>

        {/* Output */}
        <div className="animate-fade-up animate-fade-up-3">
          <Section title="Output Paths">
            <Field label="Base Path" hint="Root directory where .strm files will be written">
              <TextInput value={cfg.output.path} onChange={v => set('output.path', v)} monospace />
            </Field>
            <div className="grid grid-cols-2 gap-4">
              <Field label="Movies Subdirectory">
                <TextInput value={cfg.output.movies_dir} onChange={v => set('output.movies_dir', v)} monospace />
              </Field>
              <Field label="Series Subdirectory">
                <TextInput value={cfg.output.series_dir} onChange={v => set('output.series_dir', v)} monospace />
              </Field>
            </div>
          </Section>
        </div>

        {/* Sync */}
        <div className="animate-fade-up animate-fade-up-4">
          <Section title="Sync Schedule">
            <div className="grid grid-cols-2 gap-4">
              <Field label="Interval" hint="Go duration format: 6h, 12h, 24h, 1h30m">
                <TextInput value={cfg.sync.interval} onChange={v => set('sync.interval', v)} monospace />
              </Field>
              <Field label="Parallelism" hint="Concurrent workers for series fetch and TMDB enrichment (1–20)">
                <TextInput
                  value={String(cfg.sync.parallelism)}
                  onChange={v => set('sync.parallelism', Math.min(20, Math.max(1, parseInt(v) || 1)))}
                  monospace
                />
              </Field>
            </div>
            <div className="flex items-center justify-between">
              <Field label="Sync on Startup">
                <span className="font-mono text-[11px] text-steel-500">
                  Run a full sync when VODarr starts
                </span>
              </Field>
              <Toggle value={cfg.sync.on_startup} onChange={v => set('sync.on_startup', v)} />
            </div>
            <Field
              label="Title Cleanup Patterns"
              hint="One regex per line. Matched text is removed from stream names before TMDB search."
            >
              <textarea
                value={patternsText}
                onChange={e => setPatternsText(e.target.value)}
                placeholder={'\\s*\\(NL GESPROKEN\\)\n\\s*\\[DUBBED\\]'}
                rows={4}
                className="w-full px-3 py-2 bg-void-800 border border-void-600 rounded text-steel-300 text-[13px] font-mono resize-y"
              />
            </Field>
          </Section>
        </div>

        {/* Ports */}
        <div className="animate-fade-up animate-fade-up-4">
          <Section title="Server Ports">
            <div className="grid grid-cols-3 gap-4">
              <Field label="Newznab Port" hint="Indexer API">
                <TextInput
                  value={String(cfg.server.newznab_port)}
                  onChange={v => set('server.newznab_port', parseInt(v) || 9091)}
                  monospace
                />
              </Field>
              <Field label="qBit Port" hint="Download client">
                <TextInput
                  value={String(cfg.server.qbit_port)}
                  onChange={v => set('server.qbit_port', parseInt(v) || 9092)}
                  monospace
                />
              </Field>
              <Field label="Web Port" hint="This UI">
                <TextInput
                  value={String(cfg.server.web_port)}
                  onChange={v => set('server.web_port', parseInt(v) || 9090)}
                  monospace
                />
              </Field>
            </div>
          </Section>
        </div>

        {/* Logging */}
        <div className="animate-fade-up animate-fade-up-4">
          <Section title="Logging">
            <Field label="Log Level">
              <select
                value={cfg.logging.level}
                onChange={e => set('logging.level', e.target.value)}
                className="w-full px-3 py-2 bg-void-800 border border-void-600 rounded font-mono text-[13px] text-steel-300"
              >
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </Field>
          </Section>
        </div>

        {/* Save */}
        <div className="flex items-center gap-4 pt-2">
          <button
            type="submit"
            disabled={saving}
            className="px-6 py-2.5 bg-lime-400/10 border border-lime-400/30 text-lime-400 rounded font-mono text-[13px] hover:bg-lime-400/20 hover:border-lime-400/50 transition-all disabled:opacity-40"
          >
            {saving ? 'Saving…' : 'Save Configuration'}
          </button>
          {saved && (
            <span className="font-mono text-[12px] text-lime-400 animate-fade-up">
              ✓ Saved
            </span>
          )}
          {saveError && (
            <span className="font-mono text-[12px] text-red-400 animate-fade-up">
              {saveError}
            </span>
          )}
        </div>
      </form>
    </div>
  )
}

function deepMerge(target, source) {
  const out = { ...target }
  for (const key of Object.keys(source || {})) {
    if (source[key] && typeof source[key] === 'object' && !Array.isArray(source[key])) {
      out[key] = deepMerge(target[key] || {}, source[key])
    } else if (source[key] !== undefined && source[key] !== null && source[key] !== '') {
      out[key] = source[key]
    }
  }
  return out
}
