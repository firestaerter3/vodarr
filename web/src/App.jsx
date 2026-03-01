import { useState } from 'react'
import Dashboard from './pages/Dashboard.jsx'
import Content from './pages/Content.jsx'
import Settings from './pages/Settings.jsx'

const NAV = [
  { id: 'dashboard', label: 'Dashboard', icon: GridIcon },
  { id: 'content', label: 'Content', icon: FilmIcon },
  { id: 'settings', label: 'Settings', icon: GearIcon },
]

function GridIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <rect x="1" y="1" width="6" height="6" rx="1" stroke="currentColor" strokeWidth="1.5"/>
      <rect x="9" y="1" width="6" height="6" rx="1" stroke="currentColor" strokeWidth="1.5"/>
      <rect x="1" y="9" width="6" height="6" rx="1" stroke="currentColor" strokeWidth="1.5"/>
      <rect x="9" y="9" width="6" height="6" rx="1" stroke="currentColor" strokeWidth="1.5"/>
    </svg>
  )
}

function FilmIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <rect x="1" y="3" width="14" height="10" rx="1.5" stroke="currentColor" strokeWidth="1.5"/>
      <path d="M1 6h14M1 10h14M4 3v10M12 3v10" stroke="currentColor" strokeWidth="1.5"/>
    </svg>
  )
}

function GearIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <circle cx="8" cy="8" r="2.5" stroke="currentColor" strokeWidth="1.5"/>
      <path d="M8 1v2M8 13v2M1 8h2M13 8h2M2.93 2.93l1.41 1.41M11.66 11.66l1.41 1.41M2.93 13.07l1.41-1.41M11.66 4.34l1.41-1.41" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
  )
}

export default function App() {
  const [page, setPage] = useState('dashboard')

  return (
    <div className="flex h-full min-h-screen">
      {/* Sidebar */}
      <aside className="w-56 flex-shrink-0 flex flex-col border-r border-void-600 bg-void-800/80 backdrop-blur-sm">
        {/* Logo */}
        <div className="px-5 py-6 border-b border-void-600">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded bg-lime-400 lime-glow flex items-center justify-center flex-shrink-0">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path d="M2 7L6 11L12 3" stroke="#07080f" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
            </div>
            <span className="font-display font-800 text-xl tracking-tight text-steel-300">
              VOD<span className="text-lime-400">arr</span>
            </span>
          </div>
          <p className="mt-1.5 font-mono text-[10px] text-steel-500 tracking-widest uppercase">
            IPTV → arr bridge
          </p>
        </div>

        {/* Nav */}
        <nav className="flex-1 px-3 py-4 space-y-0.5">
          {NAV.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              onClick={() => setPage(id)}
              className={[
                'w-full flex items-center gap-3 px-3 py-2.5 rounded text-sm font-display font-500 transition-all duration-150',
                page === id
                  ? 'bg-lime-400/10 text-lime-400 border border-lime-400/20'
                  : 'text-steel-400 hover:text-steel-300 hover:bg-void-700 border border-transparent',
              ].join(' ')}
            >
              <Icon />
              {label}
            </button>
          ))}
        </nav>

        {/* Footer */}
        <div className="px-5 py-4 border-t border-void-600">
          <p className="font-mono text-[10px] text-steel-500">v0.1.0</p>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-auto">
        {page === 'dashboard' && <Dashboard />}
        {page === 'content' && <Content />}
        {page === 'settings' && <Settings />}
      </main>
    </div>
  )
}
