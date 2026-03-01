import { useState, useEffect } from 'react'

const DEFAULT = {
  xtream: { url: '', username: '', password: '' },
  tmdb: { api_key: '' },
  output: { path: '/data/strm', movies_dir: 'movies', series_dir: 'tv' },
  sync: { interval: '6h', on_startup: true },
  server: { newznab_port: 7878, qbit_port: 8080, web_port: 3000 },
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

export default function Settings() {
  const [cfg, setCfg] = useState(DEFAULT)
  const [saved, setSaved] = useState(false)
  const [saving, setSaving] = useState(false)
  const [loadError, setLoadError] = useState(null)

  useEffect(() => {
    fetch('/api/config')
      .then(r => r.json())
      .then(data => setCfg(prev => deepMerge(prev, data)))
      .catch(e => setLoadError(e.message))
  }, [])

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

  const handleSave = async e => {
    e.preventDefault()
    setSaving(true)
    try {
      await fetch('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg),
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 3000)
    } catch { /* ignore */ }
    setSaving(false)
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
            <Field label="Interval" hint="Go duration format: 6h, 12h, 24h, 1h30m">
              <TextInput value={cfg.sync.interval} onChange={v => set('sync.interval', v)} monospace />
            </Field>
            <div className="flex items-center justify-between">
              <Field label="Sync on Startup">
                <span className="font-mono text-[11px] text-steel-500">
                  Run a full sync when Vodarr starts
                </span>
              </Field>
              <Toggle value={cfg.sync.on_startup} onChange={v => set('sync.on_startup', v)} />
            </div>
          </Section>
        </div>

        {/* Ports */}
        <div className="animate-fade-up animate-fade-up-4">
          <Section title="Server Ports">
            <div className="grid grid-cols-3 gap-4">
              <Field label="Newznab Port" hint="Indexer API">
                <TextInput
                  value={String(cfg.server.newznab_port)}
                  onChange={v => set('server.newznab_port', parseInt(v) || 7878)}
                  monospace
                />
              </Field>
              <Field label="qBit Port" hint="Download client">
                <TextInput
                  value={String(cfg.server.qbit_port)}
                  onChange={v => set('server.qbit_port', parseInt(v) || 8080)}
                  monospace
                />
              </Field>
              <Field label="Web Port" hint="This UI">
                <TextInput
                  value={String(cfg.server.web_port)}
                  onChange={v => set('server.web_port', parseInt(v) || 3000)}
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
