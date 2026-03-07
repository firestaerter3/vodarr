import { useState, useEffect } from 'react'

const DEFAULT = {
  xtream: { url: '', username: '', password: '' },
  tmdb: { api_key: '', tvdb_api_key: '' },
  output: { path: '/data/strm', movies_dir: 'movies', series_dir: 'tv' },
  sync: { interval: '6h', on_startup: true, parallelism: 10, title_cleanup_patterns: [] },
  server: { newznab_port: 9091, qbit_port: 9092, web_port: 9090 },
  logging: { level: 'info' },
  arr: { instances: [] },
}

const BLANK_ARR_INSTANCE = { name: '', type: 'sonarr', url: '', api_key: '' }

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
  const [restarting, setRestarting] = useState(false)

  const [xtreamTest, setXtreamTest] = useState({ loading: false, success: false, error: null })
  const [tmdbTest, setTmdbTest] = useState({ loading: false, success: false, error: null })
  const [tvdbTest, setTvdbTest] = useState({ loading: false, success: false, error: null })

  const [arrStatus, setArrStatus] = useState(null)
  const [arrSetupState, setArrSetupState] = useState({}) // keyed by instance name

  useEffect(() => {
    fetch('/api/config')
      .then(r => r.json())
      .then(data => {
        setCfg(prev => deepMerge(prev, data))
        setPatternsText((data.sync?.title_cleanup_patterns || []).join('\n'))
      })
      .catch(e => setLoadError(e.message))
    fetchArrStatus()
  }, [])

  const fetchArrStatus = () => {
    fetch('/api/arr/status')
      .then(r => r.json())
      .then(data => setArrStatus(data))
      .catch(() => {}) // non-fatal
  }

  const addArrInstance = () => {
    setCfg(prev => ({
      ...prev,
      arr: { instances: [...(prev.arr?.instances || []), { ...BLANK_ARR_INSTANCE }] },
    }))
  }

  const removeArrInstance = idx => {
    setCfg(prev => ({
      ...prev,
      arr: { instances: prev.arr.instances.filter((_, i) => i !== idx) },
    }))
  }

  const setArrInstance = (idx, field, value) => {
    setCfg(prev => {
      const instances = prev.arr.instances.map((inst, i) =>
        i === idx ? { ...inst, [field]: value } : inst
      )
      return { ...prev, arr: { instances } }
    })
  }

  const handleArrSetup = async name => {
    if (!window.confirm(`This will enable Import Extra Files (.strm) and register a webhook Connection in "${name}". Proceed?`)) return
    setArrSetupState(s => ({ ...s, [name]: { loading: true, error: null, success: false } }))
    try {
      const res = await fetch('/api/arr/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ instance: name }),
      })
      const data = await res.json()
      const allOk = Object.values(data).every(v => v?.success !== false)
      setArrSetupState(s => ({
        ...s,
        [name]: { loading: false, success: allOk, error: allOk ? null : JSON.stringify(data) },
      }))
      fetchArrStatus()
    } catch (e) {
      setArrSetupState(s => ({ ...s, [name]: { loading: false, success: false, error: e.message } }))
    }
  }

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

  const handleRestart = async () => {
    setRestarting(true)
    try {
      await fetch('/api/restart', { method: 'POST' })
    } catch (_) {
      // expected — server closes connection as it exits
    }
    // Poll /api/health until the server is back, then reload
    const poll = () => {
      fetch('/api/health')
        .then(r => r.ok ? window.location.reload() : setTimeout(poll, 500))
        .catch(() => setTimeout(poll, 500))
    }
    setTimeout(poll, 1000)
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
        body: JSON.stringify({ api_key: cfg.tmdb.api_key }),
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

  const testTVDB = async () => {
    setTvdbTest({ loading: true, success: false, error: null })
    try {
      const res = await fetch('/api/test-tvdb', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tvdb_api_key: cfg.tmdb.tvdb_api_key }),
      })
      const data = await res.json()
      if (data.success) {
        setTvdbTest({ loading: false, success: true, error: null })
        setTimeout(() => setTvdbTest(s => ({ ...s, success: false })), 5000)
        await saveConfig(buildPayload())
      } else {
        setTvdbTest({ loading: false, success: false, error: data.error || 'Connection failed' })
        setTimeout(() => setTvdbTest(s => ({ ...s, error: null })), 5000)
      }
    } catch (e) {
      setTvdbTest({ loading: false, success: false, error: e.message })
      setTimeout(() => setTvdbTest(s => ({ ...s, error: null })), 5000)
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
        {restartRequired && !restarting && (
          <div className="mt-3 px-4 py-2.5 bg-yellow-400/10 border border-yellow-400/30 rounded font-mono text-[12px] text-yellow-400 flex items-center justify-between gap-4">
            <span>Restart required — changes take effect after restarting VODarr.</span>
            <button
              type="button"
              onClick={handleRestart}
              className="px-3 py-1 bg-yellow-400/20 border border-yellow-400/40 rounded hover:bg-yellow-400/30 transition-all whitespace-nowrap"
            >
              Restart Now
            </button>
          </div>
        )}
        {restarting && (
          <div className="mt-3 px-4 py-2.5 bg-void-700 border border-void-500 rounded font-mono text-[12px] text-steel-400 flex items-center gap-2">
            <span className="inline-block w-2 h-2 rounded-full bg-blue-400 animate-pulse" />
            Restarting… page will reload automatically.
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

        {/* TVDB */}
        <div className="animate-fade-up animate-fade-up-2">
          <Section title="TVDB">
            <Field label="API Key" hint="Optional — enables direct TVDB search for series TMDB can't cross-link. Get a free key at thetvdb.com/api-information">
              <TextInput
                value={cfg.tmdb.tvdb_api_key}
                onChange={v => set('tmdb.tvdb_api_key', v)}
                type="password"
                placeholder="••••••••••••••••"
                monospace
              />
            </Field>
            <TestButton
              onClick={testTVDB}
              loading={tvdbTest.loading}
              success={tvdbTest.success}
              error={tvdbTest.error}
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

        {/* Arr Integration */}
        <div className="animate-fade-up animate-fade-up-4">
          <Section title="Arr Integration">
            <p className="font-mono text-[11px] text-steel-500">
              Connect Sonarr/Radarr instances. VODarr can auto-configure Import Extra Files and register a webhook to clean up .mkv stubs after import.
            </p>
            {(cfg.arr?.instances || []).map((inst, idx) => {
              const statusInst = arrStatus?.instances?.find(s => s.name === inst.name)
              const setupSt = arrSetupState[inst.name] || {}
              return (
                <div key={idx} className="border border-void-600 rounded p-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      {statusInst && (
                        statusInst.issues.length === 0
                          ? <span className="font-mono text-[11px] text-lime-400">✓ OK</span>
                          : <span className="font-mono text-[11px] text-yellow-400" title={statusInst.issues.join(', ')}>⚠ {statusInst.issues.length} issue{statusInst.issues.length > 1 ? 's' : ''}</span>
                      )}
                    </div>
                    <button
                      type="button"
                      onClick={() => removeArrInstance(idx)}
                      className="font-mono text-[11px] text-steel-500 hover:text-red-400 transition-colors"
                    >
                      Remove
                    </button>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <Field label="Name">
                      <TextInput value={inst.name} onChange={v => setArrInstance(idx, 'name', v)} monospace placeholder="Sonarr Dutch" />
                    </Field>
                    <Field label="Type">
                      <select
                        value={inst.type}
                        onChange={e => setArrInstance(idx, 'type', e.target.value)}
                        className="w-full px-3 py-2 bg-void-800 border border-void-600 rounded font-mono text-[13px] text-steel-300"
                      >
                        <option value="sonarr">sonarr</option>
                        <option value="radarr">radarr</option>
                      </select>
                    </Field>
                  </div>
                  <Field label="URL">
                    <TextInput value={inst.url} onChange={v => setArrInstance(idx, 'url', v)} monospace placeholder="http://sonarr:8989" />
                  </Field>
                  <Field label="API Key">
                    <TextInput value={inst.api_key} onChange={v => setArrInstance(idx, 'api_key', v)} type="password" monospace placeholder="••••••••••••••••" />
                  </Field>
                  <div className="flex items-center gap-3 pt-1">
                    <button
                      type="button"
                      onClick={() => handleArrSetup(inst.name)}
                      disabled={setupSt.loading || !inst.name || !inst.url}
                      className="px-4 py-1.5 bg-void-600 border border-void-500 text-steel-400 rounded font-mono text-[12px] hover:bg-void-500 hover:text-steel-300 transition-all disabled:opacity-40"
                    >
                      {setupSt.loading ? 'Configuring…' : 'Auto-Configure'}
                    </button>
                    {setupSt.success && <span className="font-mono text-[12px] text-lime-400">✓ Configured</span>}
                    {setupSt.error && <span className="font-mono text-[12px] text-red-400 truncate max-w-xs" title={setupSt.error}>Failed</span>}
                    {statusInst?.issues?.length > 0 && (
                      <span className="font-mono text-[10px] text-steel-500">{statusInst.issues.join(' · ')}</span>
                    )}
                  </div>
                </div>
              )
            })}
            <button
              type="button"
              onClick={addArrInstance}
              className="w-full py-2 border border-dashed border-void-500 text-steel-500 rounded font-mono text-[12px] hover:border-void-400 hover:text-steel-400 transition-all"
            >
              + Add Instance
            </button>
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
