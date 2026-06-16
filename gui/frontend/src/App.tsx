import { useState, useEffect, useCallback, useRef } from 'react'
import {
  GitCompare, Table2, ShieldCheck, GitMerge, Activity,
  FolderOpen, RefreshCw, Hash, AlertCircle, CheckCircle2,
  ChevronRight, ChevronLeft, Database, Clock, X, Plus,
  AlertTriangle, Play, Eye, Save, FileJson, Layers, Pencil, Check as CheckIcon,
  Settings, Key, ExternalLink
} from 'lucide-react'
import {
  Diff, OpenFile, SaveFile, Schema, QueryTable, TableDiffRows,
  Check, MigrateGenerate, MigrateApply, MonitorSnapshot, MonitorCheck, MonitorLoadHistory,
  MonitorWatchStart, MonitorWatchStop, MonitorWatchIsRunning,
  FleetDiscover, FleetSnapshot, FleetCheck
} from '../wailsjs/go/main/App'
import { OnFileDrop, OnFileDropOff, EventsOn, EventsOff } from '../wailsjs/runtime/runtime'

// ── Types ─────────────────────────────────────────────────────────────────────

type Tool = 'diff' | 'explorer' | 'check' | 'migrate' | 'monitor' | 'fleet' | 'settings'

type ConnType = 'local' | 'turso' | 'd1'

interface Connection {
  id: string
  name: string      // user-given alias
  path: string      // file path or turso:// / d1:// URL
  type: ConnType
  addedAt: number
  lastUsed?: number
}

// ── Connections store ─────────────────────────────────────────────────────────

const CONN_KEY = 'litescope_connections_v1'
const RECENT_KEY = 'litescope_recent_v2'
const MAX_RECENT = 12

function connType(path: string): ConnType {
  if (path.startsWith('turso://')) return 'turso'
  if (path.startsWith('d1://')) return 'd1'
  return 'local'
}

function useConnections() {
  const [conns, setConns] = useState<Connection[]>(() => {
    try { return JSON.parse(localStorage.getItem(CONN_KEY) ?? '[]') } catch { return [] }
  })

  const save = (next: Connection[]) => {
    setConns(next)
    localStorage.setItem(CONN_KEY, JSON.stringify(next))
  }

  const add = useCallback((path: string, name?: string) => {
    setConns(prev => {
      if (prev.find(c => c.path === path)) {
        // bump lastUsed
        const next = prev.map(c => c.path === path ? { ...c, lastUsed: Date.now() } : c)
        localStorage.setItem(CONN_KEY, JSON.stringify(next))
        return next
      }
      const next = [...prev, {
        id: crypto.randomUUID(),
        name: name ?? path.split('/').pop() ?? path,
        path,
        type: connType(path),
        addedAt: Date.now(),
        lastUsed: Date.now(),
      }]
      localStorage.setItem(CONN_KEY, JSON.stringify(next))
      return next
    })
  }, [])

  const remove = useCallback((id: string) => {
    setConns(prev => {
      const next = prev.filter(c => c.id !== id)
      localStorage.setItem(CONN_KEY, JSON.stringify(next))
      return next
    })
  }, [])

  const rename = useCallback((id: string, name: string) => {
    setConns(prev => {
      const next = prev.map(c => c.id === id ? { ...c, name } : c)
      localStorage.setItem(CONN_KEY, JSON.stringify(next))
      return next
    })
  }, [])

  const touch = useCallback((path: string) => {
    setConns(prev => {
      const next = prev.map(c => c.path === path ? { ...c, lastUsed: Date.now() } : c)
      localStorage.setItem(CONN_KEY, JSON.stringify(next))
      return next
    })
  }, [])

  return { conns, add, remove, rename, touch }
}

// ── Pro gate ──────────────────────────────────────────────────────────────────

const FREE_CONN_LIMIT = 1
const LICENSE_KEY = 'litescope_license'

function getLicenseKey(): string {
  return localStorage.getItem(LICENSE_KEY) ?? ''
}

function isPro(key?: string): boolean {
  return (key ?? getLicenseKey()).startsWith('lsc_pro_')
}

function useIsPro(): boolean {
  const [pro, setPro] = useState(() => isPro())
  useEffect(() => {
    const handler = () => setPro(isPro())
    window.addEventListener('litescope:license-changed', handler)
    return () => window.removeEventListener('litescope:license-changed', handler)
  }, [])
  return pro
}

function ProGate({ children, feature, onOpenSettings }: {
  children: React.ReactNode
  feature?: string
  onOpenSettings?: () => void
}) {
  const pro = useIsPro()
  if (pro) return <>{children}</>
  return (
    <div className="flex-1 flex flex-col items-center justify-center gap-4 text-center p-8">
      <div className="w-14 h-14 rounded-full bg-[#2d2d2d] flex items-center justify-center mb-1">
        <Key size={24} className="text-[#dcdcaa]" strokeWidth={1.5} />
      </div>
      <div className="text-[15px] text-[#cccccc] font-semibold">{feature ?? 'Pro Feature'}</div>
      <div className="text-[13px] text-[#858585] max-w-[300px] leading-relaxed">
        This feature requires Litescope Pro — $89/year.
      </div>
      <div className="flex gap-2 mt-1">
        <a
          href="https://litescope-site.pages.dev/#pricing"
          target="_blank"
          className="flex items-center gap-1.5 px-4 py-2 bg-[#007acc] hover:bg-[#1b8ae4] text-white text-[12px] rounded-sm transition-colors"
        >
          Get Pro <ExternalLink size={11} />
        </a>
        {onOpenSettings && (
          <button
            onClick={onOpenSettings}
            className="flex items-center gap-1.5 px-4 py-2 bg-[#3c3c3c] hover:bg-[#4a4a4a] text-[#cccccc] text-[12px] rounded-sm transition-colors"
          >
            <Key size={12} />Enter key
          </button>
        )}
      </div>
    </div>
  )
}

// ── Settings Panel ────────────────────────────────────────────────────────────

function SettingsView() {
  const [key, setKey] = useState(() => getLicenseKey())
  const [saved, setSaved] = useState(false)
  const [err, setErr] = useState('')

  function save() {
    const trimmed = key.trim()
    if (trimmed && !trimmed.startsWith('lsc_pro_')) {
      setErr('Invalid key format. Keys start with lsc_pro_')
      return
    }
    localStorage.setItem(LICENSE_KEY, trimmed)
    window.dispatchEvent(new Event('litescope:license-changed'))
    setSaved(true)
    setErr('')
    setTimeout(() => setSaved(false), 2000)
  }

  function remove() {
    setKey('')
    localStorage.removeItem(LICENSE_KEY)
    window.dispatchEvent(new Event('litescope:license-changed'))
  }

  const currentKey = getLicenseKey()
  const active = isPro(currentKey)

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="max-w-lg">
        <div className="text-[15px] font-semibold text-[#cccccc] mb-1">Settings</div>
        <div className="text-[12px] text-[#585858] mb-6">License and preferences</div>

        {/* License section */}
        <div className="bg-[#252526] border border-[#333] rounded-sm p-4 mb-4">
          <div className="flex items-center gap-2 mb-3">
            <Key size={14} className="text-[#dcdcaa]" strokeWidth={1.5} />
            <span className="text-[13px] font-medium text-[#cccccc]">License</span>
            <span className={`ml-auto text-[10px] px-2 py-0.5 rounded-full font-medium ${active ? 'bg-[#1a3a2a] text-[#4ec9b0]' : 'bg-[#2d2d2d] text-[#858585]'}`}>
              {active ? 'Pro' : 'Free'}
            </span>
          </div>

          {active && (
            <div className="text-[11px] text-[#585858] font-mono bg-[#1e1e1e] px-3 py-2 rounded-sm border border-[#333] mb-3 truncate">
              {currentKey}
            </div>
          )}

          <div className="flex gap-2">
            <input
              value={key}
              onChange={e => { setKey(e.target.value); setErr('') }}
              onKeyDown={e => e.key === 'Enter' && save()}
              placeholder="lsc_pro_…"
              className="flex-1 bg-[#1e1e1e] border border-[#555] text-[#cccccc] text-[12px] px-3 py-1.5 rounded-sm outline-none focus:border-[#007acc] font-mono placeholder-[#484848]"
            />
            <button
              onClick={save}
              className="px-3 py-1.5 bg-[#007acc] hover:bg-[#1b8ae4] text-white text-[12px] rounded-sm transition-colors"
            >
              {saved ? <CheckIcon size={13} /> : 'Activate'}
            </button>
            {active && (
              <button
                onClick={remove}
                className="px-3 py-1.5 bg-[#3c3c3c] hover:bg-[#4a4a4a] text-[#858585] text-[12px] rounded-sm transition-colors"
              >
                Remove
              </button>
            )}
          </div>
          {err && <div className="text-[11px] text-[#f44747] mt-2">{err}</div>}
          {saved && <div className="text-[11px] text-[#4ec9b0] mt-2">License activated.</div>}
        </div>

        {!active && (
          <div className="bg-[#1a1a2a] border border-[#007acc]/30 rounded-sm p-4">
            <div className="text-[12px] text-[#cccccc] font-medium mb-1">Litescope Pro — $89/year</div>
            <div className="text-[11px] text-[#858585] mb-3 leading-relaxed">
              Continuous drift monitoring, fleet operations across Turso & D1, unlimited connections.
            </div>
            <a
              href="https://litescope-site.pages.dev/#pricing"
              target="_blank"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-[#007acc] hover:bg-[#1b8ae4] text-white text-[12px] rounded-sm transition-colors"
            >
              Buy Pro <ExternalLink size={11} />
            </a>
          </div>
        )}
      </div>
    </div>
  )
}

// recent paths — lightweight, auto-populated for DbPicker dropdowns
function useRecent() {
  const [recent, setRecent] = useState<string[]>(() => {
    try { return JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]') } catch { return [] }
  })
  const addRecent = useCallback((path: string) => {
    setRecent(prev => {
      const next = [path, ...prev.filter(p => p !== path)].slice(0, MAX_RECENT)
      localStorage.setItem(RECENT_KEY, JSON.stringify(next))
      return next
    })
  }, [])
  const removeRecent = useCallback((path: string) => {
    setRecent(prev => {
      const next = prev.filter(p => p !== path)
      localStorage.setItem(RECENT_KEY, JSON.stringify(next))
      return next
    })
  }, [])
  return { recent, addRecent, removeRecent }
}

// ── Root ──────────────────────────────────────────────────────────────────────

export default function App() {
  const [tool, setTool] = useState<Tool>('diff')
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const { conns, add: addConn, remove: removeConn, rename: renameConn, touch } = useConnections()
  const { recent, addRecent, removeRecent } = useRecent()
  const [statusMsg, setStatusMsg] = useState<{ text: string; kind: 'ok' | 'warn' | 'err' | 'idle' }>({ text: 'Ready', kind: 'idle' })

  // inject: sidebarで connection をクリックすると active view に path が注入される
  const injectRef = useRef<((path: string) => void) | null>(null)

  const status = (text: string, kind: typeof statusMsg.kind) => setStatusMsg({ text, kind })

  function handleConnClick(conn: Connection) {
    touch(conn.path)
    addRecent(conn.path)
    injectRef?.current?.(conn.path)
  }

  const viewProps = { recent, addRecent, removeRecent, status }

  return (
    <div className="flex flex-col h-screen bg-[#1e1e1e] text-[#cccccc] text-[13px] font-sans overflow-hidden select-none">
      {/* macOS titlebar drag region — sits above activity bar + sidebar */}
      <div className="h-[28px] shrink-0 bg-[#252526]" style={{ WebkitAppRegion: 'drag' } as any} />
      <div className="flex flex-1 overflow-hidden">
        <ActivityBar tool={tool} setTool={setTool} sidebarOpen={sidebarOpen} toggleSidebar={() => setSidebarOpen(o => !o)} />
        {sidebarOpen && tool !== 'settings' && (
          <Sidebar
            conns={conns} onConnClick={handleConnClick}
            onAddConn={async () => {
              if (!isPro() && conns.length >= FREE_CONN_LIMIT) {
                setTool('settings')
                return
              }
              const p = await OpenFile(); if (p) { addConn(p); addRecent(p) }
            }}
            onRemoveConn={removeConn} onRenameConn={renameConn}
            activeTool={tool}
            onOpenSettings={() => setTool('settings')}
          />
        )}
        <main className="flex-1 flex flex-col overflow-hidden min-w-0">
          {tool === 'diff'     && <DiffView    {...viewProps} injectRef={injectRef} />}
          {tool === 'explorer' && <ExplorerView {...viewProps} injectRef={injectRef} />}
          {tool === 'check'    && <ProGate feature="Backup Check" onOpenSettings={() => setTool('settings')}><CheckView   {...viewProps} injectRef={injectRef} /></ProGate>}
          {tool === 'migrate'  && <ProGate feature="Migration Studio" onOpenSettings={() => setTool('settings')}><MigrateView  {...viewProps} injectRef={injectRef} /></ProGate>}
          {tool === 'monitor'  && <ProGate feature="Drift Monitor" onOpenSettings={() => setTool('settings')}><MonitorView  {...viewProps} injectRef={injectRef} /></ProGate>}
          {tool === 'fleet'    && <ProGate feature="Fleet Operations" onOpenSettings={() => setTool('settings')}><FleetView    {...viewProps} /></ProGate>}
          {tool === 'settings' && <SettingsView />}
        </main>
      </div>
      <StatusBar msg={statusMsg} tool={tool} />
    </div>
  )
}

// ── Activity Bar ──────────────────────────────────────────────────────────────

const TOOLS: { id: Tool; icon: React.ReactNode; label: string }[] = [
  { id: 'diff',     icon: <GitCompare size={20} strokeWidth={1.5} />,  label: 'Diff' },
  { id: 'explorer', icon: <Table2 size={20} strokeWidth={1.5} />,      label: 'Explorer' },
  { id: 'check',    icon: <ShieldCheck size={20} strokeWidth={1.5} />, label: 'Check' },
  { id: 'migrate',  icon: <GitMerge size={20} strokeWidth={1.5} />,    label: 'Migrate' },
  { id: 'monitor',  icon: <Activity size={20} strokeWidth={1.5} />,    label: 'Monitor' },
  { id: 'fleet',    icon: <Layers size={20} strokeWidth={1.5} />,      label: 'Fleet' },
]

function ActivityBar({ tool, setTool, sidebarOpen, toggleSidebar }: {
  tool: Tool; setTool: (t: Tool) => void
  sidebarOpen: boolean; toggleSidebar: () => void
}) {
  return (
    <div className="w-[48px] flex flex-col items-center bg-[#2a2a2a] border-r border-[#1e1e1e] shrink-0" style={{ paddingTop: '28px' }}>
      {TOOLS.map(t => (
        <button key={t.id} title={t.label} onClick={() => { setTool(t.id); if (!sidebarOpen) toggleSidebar() }}
          className={`relative w-full h-[48px] flex items-center justify-center transition-colors
            ${tool === t.id ? 'text-white' : 'text-[#858585] hover:text-[#cccccc]'}`}>
          {tool === t.id && <span className="absolute left-0 top-3 bottom-3 w-[2px] bg-[#007acc] rounded-r-full" />}
          {t.icon}
        </button>
      ))}
      <div className="flex-1" />
      <button title="Settings" onClick={() => setTool('settings')}
        className={`w-full h-[40px] flex items-center justify-center transition-colors ${tool === 'settings' ? 'text-white' : 'text-[#585858] hover:text-[#cccccc]'}`}>
        <Settings size={16} strokeWidth={1.5} />
      </button>
      <button title="Toggle sidebar" onClick={toggleSidebar}
        className="w-full h-[40px] flex items-center justify-center text-[#585858] hover:text-[#cccccc] mb-1">
        <Layers size={16} strokeWidth={1.5} />
      </button>
    </div>
  )
}

// ── Sidebar ───────────────────────────────────────────────────────────────────

const TYPE_BADGE: Record<ConnType, { label: string; cls: string }> = {
  local:  { label: 'local', cls: 'text-[#569cd6] border-[#569cd6]/30' },
  turso:  { label: 'turso', cls: 'text-[#4ec9b0] border-[#4ec9b0]/30' },
  d1:     { label: 'd1',    cls: 'text-[#dcdcaa] border-[#dcdcaa]/30' },
}

function Sidebar({ conns, onConnClick, onAddConn, onRemoveConn, onRenameConn, activeTool, onOpenSettings }: {
  conns: Connection[]
  onConnClick: (c: Connection) => void
  onAddConn: () => void
  onRemoveConn: (id: string) => void
  onRenameConn: (id: string, name: string) => void
  activeTool: Tool
  onOpenSettings: () => void
}) {
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editVal, setEditVal] = useState('')
  const editRef = useRef<HTMLInputElement>(null)

  function startRename(c: Connection) {
    setEditingId(c.id); setEditVal(c.name)
    setTimeout(() => editRef.current?.select(), 0)
  }
  function commitRename() {
    if (editingId && editVal.trim()) onRenameConn(editingId, editVal.trim())
    setEditingId(null)
  }

  return (
    <div className="w-[220px] flex flex-col bg-[#252526] border-r border-[#1a1a1a] shrink-0 overflow-hidden">
      {/* traffic lights spacer */}
      <div className="shrink-0" style={{ height: '28px' }} />
      <div className="flex items-center h-[35px] px-3 border-b border-[#1e1e1e] gap-2 shrink-0">
        <span className="text-[10px] uppercase tracking-wider text-[#858585] font-medium flex-1">Databases</span>
        <button onClick={onAddConn} title="Add connection" className="text-[#585858] hover:text-[#cccccc]">
          <Plus size={14} strokeWidth={1.5} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto py-1">
        {conns.length === 0 && (
          <div className="px-3 py-6 text-[11px] text-[#585858] text-center leading-relaxed">
            Click <span className="text-[#cccccc]">+</span> to add a database<br/>or drop a .db file
          </div>
        )}
        {conns.slice().sort((a, b) => (b.lastUsed ?? b.addedAt) - (a.lastUsed ?? a.addedAt)).map(c => {
          const badge = TYPE_BADGE[c.type]
          return (
            <div key={c.id}
              className="flex items-center gap-1.5 px-2 py-1.5 hover:bg-[#2a2d2e] group rounded-sm mx-1 cursor-pointer"
              onClick={() => onConnClick(c)}>
              <Database size={12} className="text-[#569cd6] shrink-0" strokeWidth={1.5} />
              <div className="flex-1 min-w-0">
                {editingId === c.id
                  ? <input ref={editRef} value={editVal} onChange={e => setEditVal(e.target.value)}
                      onBlur={commitRename}
                      onKeyDown={e => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setEditingId(null) }}
                      onClick={e => e.stopPropagation()}
                      className="w-full bg-[#3c3c3c] text-[#cccccc] text-[12px] px-1 rounded-sm outline-none border border-[#007acc]" />
                  : <div className="text-[12px] text-[#cccccc] truncate">{c.name}</div>
                }
                <div className="flex items-center gap-1.5 mt-0.5">
                  <span className={`text-[9px] border rounded-sm px-1 ${badge.cls}`}>{badge.label}</span>
                  <span className="text-[10px] text-[#585858] truncate">{c.path.split('/').slice(-2).join('/').slice(0, 28)}</span>
                </div>
              </div>
              <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 shrink-0" onClick={e => e.stopPropagation()}>
                <button title="Rename" onClick={() => startRename(c)} className="text-[#585858] hover:text-[#cccccc] p-0.5">
                  <Pencil size={10} />
                </button>
                <button title="Remove" onClick={() => onRemoveConn(c.id)} className="text-[#585858] hover:text-[#f44747] p-0.5">
                  <X size={10} />
                </button>
              </div>
            </div>
          )
        })}
      </div>

      <div className="border-t border-[#1e1e1e] px-3 py-2.5 shrink-0">
        <div className="text-[10px] text-[#585858] leading-relaxed">
          Click a DB to inject into the active panel
        </div>
        {!useIsPro() && (
          <div className="text-[10px] text-[#585858] mt-1">
            Free: {conns.length}/{FREE_CONN_LIMIT} connections ·{' '}
            <button onClick={onOpenSettings} className="text-[#007acc] hover:underline bg-transparent border-0 p-0 cursor-pointer text-[10px]">Upgrade</button>
          </div>
        )}
      </div>
    </div>
  )
}

// ── Status Bar ────────────────────────────────────────────────────────────────

const toolLabels: Record<Tool, string> = {
  diff: 'Schema Diff', explorer: 'DB Explorer', check: 'Integrity Check',
  migrate: 'Migration Studio', monitor: 'Drift Monitor', fleet: 'Fleet', settings: 'Settings',
}

function StatusBar({ msg, tool }: { msg: { text: string; kind: string }; tool: Tool }) {
  const kindCls = { ok: 'text-[#4ec9b0]', warn: 'text-[#dcdcaa]', err: 'text-[#f44747]', idle: 'text-white' }[msg.kind] ?? 'text-white'
  return (
    <div className="h-[22px] bg-[#007acc] flex items-center px-3 gap-3 text-white text-[11px] shrink-0">
      <span className="flex items-center gap-1.5 opacity-90"><Database size={11} />Litescope</span>
      <span className="opacity-40">|</span>
      <span className="opacity-75">{toolLabels[tool]}</span>
      <div className="flex-1" />
      <span className={`${kindCls} opacity-90`}>{msg.text}</span>
    </div>
  )
}

// ── Shared Components ─────────────────────────────────────────────────────────

function PanelHeader({ icon, title, meta }: { icon: React.ReactNode; title: string; meta?: React.ReactNode }) {
  return (
    <div className="flex items-center h-[35px] bg-[#2d2d2d] border-b border-[#252525] px-3 gap-2 shrink-0">
      <span className="text-[#007acc]">{icon}</span>
      <span className="text-[12px] font-medium">{title}</span>
      {meta && <div className="ml-auto">{meta}</div>}
    </div>
  )
}

function Toolbar({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center h-[32px] bg-[#3c3c3c] border-b border-[#252525] px-2 gap-2 shrink-0">
      {children}
    </div>
  )
}

function DbPicker({ label, path, onPick, onRecent, recent, removeRecent }: {
  label: string; path: string; onPick: () => void
  onRecent: (p: string) => void; recent: string[]; removeRecent: (p: string) => void
}) {
  const [open, setOpen] = useState(false)
  const name = path ? path.split('/').pop() : null
  return (
    <div className="relative">
      <button onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 h-[22px] px-2 rounded-sm text-[12px] hover:bg-[#505050] transition-colors max-w-[260px]">
        <FolderOpen size={11} className="text-[#858585] shrink-0" />
        <span className="text-[#858585] shrink-0">{label}:</span>
        <span className={`truncate ${name ? 'text-[#cccccc]' : 'text-[#585858]'}`}>{name ?? 'select…'}</span>
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute top-full left-0 mt-px bg-[#252526] border border-[#3c3c3c] shadow-2xl z-50 w-[280px]">
            <button onClick={() => { onPick(); setOpen(false) }}
              className="w-full flex items-center gap-2 px-3 py-2 hover:bg-[#2a2d2e] text-[12px] border-b border-[#3c3c3c]">
              <FolderOpen size={12} className="text-[#858585]" />Browse…
            </button>
            {recent.length > 0 && <>
              <div className="px-3 py-1 text-[10px] text-[#585858] uppercase tracking-wider">Recent</div>
              {recent.map(p => (
                <div key={p} className="flex items-center gap-2 px-3 py-1.5 hover:bg-[#2a2d2e] group">
                  <Clock size={11} className="text-[#585858] shrink-0" />
                  <button className="flex-1 text-left text-[12px] truncate text-[#cccccc]"
                    onClick={() => { onRecent(p); setOpen(false) }}>{p.split('/').pop()}</button>
                  <button onClick={() => removeRecent(p)} className="opacity-0 group-hover:opacity-100 text-[#585858] hover:text-[#cccccc]">
                    <X size={11} /></button>
                </div>
              ))}
            </>}
          </div>
        </>
      )}
    </div>
  )
}

function Btn({ children, onClick, disabled, variant = 'primary', small }: {
  children: React.ReactNode; onClick?: () => void; disabled?: boolean
  variant?: 'primary' | 'ghost' | 'danger'; small?: boolean
}) {
  const base = `flex items-center gap-1.5 rounded-sm font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed`
  const size = small ? 'h-[20px] px-2 text-[11px]' : 'h-[22px] px-3 text-[12px]'
  const v = {
    primary: 'bg-[#0e639c] hover:bg-[#1177bb] text-white',
    ghost:   'bg-transparent hover:bg-[#3c3c3c] text-[#cccccc] border border-[#555]',
    danger:  'bg-[#6a1717] hover:bg-[#8a2020] text-[#f44747] border border-[#8a2020]',
  }[variant]
  return <button onClick={onClick} disabled={disabled} className={`${base} ${size} ${v}`}>{children}</button>
}

function Spinner() {
  return <RefreshCw size={12} className="animate-spin text-[#007acc]" />
}

function EmptyState({ icon, text, sub }: { icon: React.ReactNode; text: string; sub?: string }) {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-[#585858]">
      <div className="opacity-30">{icon}</div>
      <div className="text-[13px]">{text}</div>
      {sub && <div className="text-[11px] opacity-70">{sub}</div>}
    </div>
  )
}

function ErrPanel({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 m-3 px-3 py-2.5 bg-[#5a1d1d] border border-[#be1100] text-[#f48771] text-[12px] rounded-sm">
      <AlertCircle size={13} className="shrink-0 mt-0.5" /><span className="font-mono break-all">{message}</span>
    </div>
  )
}

function OkPanel({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 m-3 px-3 py-2.5 bg-[#1a3a1a] border border-[#4ec9b0]/40 text-[#4ec9b0] text-[12px] rounded-sm">
      <CheckCircle2 size={13} className="shrink-0 mt-0.5" />{message}
    </div>
  )
}

function WarnBanner({ messages }: { messages: string[] }) {
  if (!messages.length) return null
  return (
    <div className="m-3 px-3 py-2 bg-[#3a2d00] border border-[#febc2e]/40 text-[#dcdcaa] text-[12px] rounded-sm">
      <div className="flex items-center gap-1.5 mb-1 font-medium"><AlertTriangle size={12} />Warnings</div>
      {messages.map((m, i) => <div key={i} className="text-[11px] opacity-80">· {m}</div>)}
    </div>
  )
}

function Badge({ label, color }: { label: string; color: 'green' | 'red' | 'yellow' | 'blue' }) {
  const cls = {
    green:  'bg-[#4ec9b0]/15 text-[#4ec9b0] border-[#4ec9b0]/30',
    red:    'bg-[#f44747]/15 text-[#f44747] border-[#f44747]/30',
    yellow: 'bg-[#dcdcaa]/15 text-[#dcdcaa] border-[#dcdcaa]/30',
    blue:   'bg-[#569cd6]/15 text-[#569cd6] border-[#569cd6]/30',
  }[color]
  return <span className={`px-1.5 py-0.5 text-[10px] font-mono border rounded-sm ${cls}`}>{label}</span>
}

function SubTab({ label, active, onClick, count }: { label: string; active: boolean; onClick: () => void; count?: number }) {
  return (
    <button onClick={onClick}
      className={`h-full px-3 text-[12px] flex items-center gap-1.5 border-b-2 transition-colors
        ${active ? 'border-[#007acc] text-[#cccccc] bg-[#1e1e1e]' : 'border-transparent text-[#858585] hover:text-[#cccccc]'}`}>
      {label}
      {count != null && count > 0 && <span className="px-1.5 py-0.5 rounded-full bg-[#3c3c3c] text-[10px] text-[#858585]">{count}</span>}
    </button>
  )
}

// ── Diff View ─────────────────────────────────────────────────────────────────

function DiffView({ recent, addRecent, removeRecent, status, injectRef }: ViewProps) {
  const [oldPath, setOldPath] = useState('')
  const [newPath, setNewPath] = useState('')

  useEffect(() => {
    if (injectRef) injectRef.current = (p: string) => { setOldPath(p); addRecent(p) }
    return () => { if (injectRef) injectRef.current = null }
  }, [])
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState<'schema' | 'data'>('schema')

  useEffect(() => {
    OnFileDrop((_, __, paths) => {
      if (!paths.length) return
      const p = paths[0]; addRecent(p)
      if (!oldPath) setOldPath(p)
      else setNewPath(p)
    }, true)
    return () => OnFileDropOff()
  }, [oldPath, newPath])

  async function pick(setter: (p: string) => void) {
    const p = await OpenFile(); if (p) { setter(p); addRecent(p) }
  }

  async function run() {
    setLoading(true); setError(''); setResult(null); status('Comparing…', 'idle')
    try {
      const r = await Diff(oldPath, newPath)
      setResult(r)
      const changes = (r?.Schema?.length ?? 0) + (r?.Data?.length ?? 0)
      status(changes > 0 ? `${changes} difference(s) found` : 'Identical — no differences', changes > 0 ? 'warn' : 'ok')
    } catch (e: any) { setError(String(e)); status('Comparison failed', 'err') }
    finally { setLoading(false) }
  }

  const schema: any[] = result?.Schema ?? []
  const data: any[] = result?.Data ?? []

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<GitCompare size={14} />} title="Schema Diff" />
      <Toolbar>
        <DbPicker label="Before" path={oldPath} onPick={() => pick(setOldPath)}
          recent={recent} onRecent={p => { setOldPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <span className="text-[#585858] text-[11px]">→</span>
        <DbPicker label="After" path={newPath} onPick={() => pick(setNewPath)}
          recent={recent} onRecent={p => { setNewPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <div className="flex-1" />
        <Btn onClick={run} disabled={!oldPath || !newPath || loading}>
          {loading ? <><Spinner />Comparing…</> : 'Compare'}
        </Btn>
      </Toolbar>

      {error && <ErrPanel message={error} />}

      {!result && !error && !loading && (
        <EmptyState icon={<GitCompare size={48} />}
          text="Drop two .db files here, or select Before → After above"
          sub="Compare schemas, column changes, index changes, and row-level data diffs" />
      )}
      {loading && (
        <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]">
          <Spinner />Comparing databases…
        </div>
      )}

      {result && (
        <>
          {schema.length === 0 && data.length === 0
            ? <OkPanel message="Databases are identical — no differences found" />
            : (
              <>
                <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
                  <SubTab label="Schema" count={schema.length} active={activeTab === 'schema'} onClick={() => setActiveTab('schema')} />
                  <SubTab label="Data" count={data.length} active={activeTab === 'data'} onClick={() => setActiveTab('data')} />
                  <div className="flex-1" />
                  <div className="flex items-center gap-3 text-[11px] pr-1">
                    {schema.filter((t:any) => t.Added).length > 0 && <span className="text-[#4ec9b0]">+{schema.filter((t:any) => t.Added).length}</span>}
                    {schema.filter((t:any) => t.Removed).length > 0 && <span className="text-[#f44747]">-{schema.filter((t:any) => t.Removed).length}</span>}
                    {schema.filter((t:any) => !t.Added && !t.Removed).length > 0 && <span className="text-[#dcdcaa]">~{schema.filter((t:any) => !t.Added && !t.Removed).length}</span>}
                  </div>
                </div>
                <div className="flex-1 overflow-y-auto">
                  {activeTab === 'schema' && <SchemaDiffTable rows={schema} />}
                  {activeTab === 'data' && data.map((dd: any, i: number) =>
                    <DataDiffSection key={i} dd={dd} oldPath={oldPath} newPath={newPath} result={result} />
                  )}
                </div>
              </>
            )}
        </>
      )}
    </div>
  )
}

function SchemaDiffTable({ rows }: { rows: any[] }) {
  return (
    <table className="w-full text-[12px]">
      <thead>
        <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
          <th className="text-left px-4 py-1.5 w-6" />
          <th className="text-left px-3 py-1.5">Table</th>
          <th className="text-left px-3 py-1.5">Change</th>
          <th className="text-left px-3 py-1.5">Details</th>
        </tr>
      </thead>
      <tbody>{rows.map((td, i) => <SchemaRow key={i} td={td} />)}</tbody>
    </table>
  )
}

function SchemaRow({ td }: { td: any }) {
  const [expanded, setExpanded] = useState(true)
  const details = [
    ...(td.AddedColumns ?? []).map((c: any) => ({ sign: '+', text: `column  ${c.Name}  ${c.Type}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedColumns ?? []).map((c: any) => ({ sign: '-', text: `column  ${c.Name}`, cls: 'text-[#f44747]' })),
    ...(td.ChangedColumns ?? []).map((c: any) => ({ sign: '~', text: `column  ${c.Name}  ${c.Old.Type} → ${c.New.Type}`, cls: 'text-[#dcdcaa]' })),
    ...(td.AddedIndexes ?? []).map((idx: any) => ({ sign: '+', text: `index   ${idx.Name}${idx.Unique ? '  UNIQUE' : ''}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedIndexes ?? []).map((idx: any) => ({ sign: '-', text: `index   ${idx.Name}`, cls: 'text-[#f44747]' })),
  ]
  return (
    <>
      <tr className={`border-b border-[#2d2d2d] hover:bg-[#2a2d2e] cursor-pointer ${td.Added ? 'bg-[#4ec9b0]/5' : td.Removed ? 'bg-[#f44747]/5' : ''}`}
        onClick={() => details.length && setExpanded(e => !e)}>
        <td className="px-4 py-1.5 text-[#858585]">
          {details.length > 0 && <ChevronRight size={12} className={`transition-transform ${expanded ? 'rotate-90' : ''}`} />}
        </td>
        <td className="px-3 py-1.5 font-mono">
          <span className={td.Added ? 'text-[#4ec9b0]' : td.Removed ? 'text-[#f44747]' : 'text-[#9cdcfe]'}>{td.Name}</span>
        </td>
        <td className="px-3 py-1.5">
          {td.Added && <Badge label="ADDED" color="green" />}
          {td.Removed && <Badge label="REMOVED" color="red" />}
          {!td.Added && !td.Removed && <Badge label="MODIFIED" color="yellow" />}
        </td>
        <td className="px-3 py-1.5 text-[#858585] text-[11px]">
          {td.Added ? `${td.AddedColumns?.length ?? 0} columns` : td.Removed ? 'table removed' : `${details.length} changes`}
        </td>
      </tr>
      {expanded && details.map((d, i) => (
        <tr key={i} className="border-b border-[#252525] bg-[#252526]/50">
          <td /><td colSpan={3} className="px-3 py-1 font-mono text-[11px]">
            <span className={`${d.cls} opacity-60 mr-3`}>{d.sign}</span>
            <span className={d.cls}>{d.text}</span>
          </td>
        </tr>
      ))}
    </>
  )
}

function DataDiffSection({ dd, oldPath, newPath, result }: { dd: any; oldPath: string; newPath: string; result: any }) {
  const [expanded, setExpanded] = useState(false)
  const [rows, setRows] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const schemaTable = result?.Schema?.find((t: any) => t.Name === dd.Table)
  const pkCol = schemaTable
    ? (schemaTable.AddedColumns ?? schemaTable.RemovedColumns ?? []).find?.((c: any) => c.PK === 1)?.Name ?? 'id'
    : 'id'

  async function load() {
    if (loaded) { setExpanded(e => !e); return }
    setExpanded(true); setLoading(true)
    try { const r = await TableDiffRows(oldPath, newPath, dd.Table, pkCol, 100); setRows(r ?? []); setLoaded(true) }
    catch { setRows([]) }
    finally { setLoading(false) }
  }
  const allCols = rows.length > 0 ? Object.keys(rows[0].New ?? rows[0].Old ?? {}) : []
  return (
    <div className="border-b border-[#252525]">
      <button onClick={load} className="w-full flex items-center gap-3 px-4 py-2 hover:bg-[#2a2d2e] text-left">
        <ChevronRight size={12} className={`text-[#858585] shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />
        <span className="font-mono text-[#9cdcfe]">{dd.Table}</span>
        <div className="flex gap-3 text-[11px] ml-2">
          {dd.Added > 0 && <span className="text-[#4ec9b0]">+{dd.Added}</span>}
          {dd.Removed > 0 && <span className="text-[#f44747]">-{dd.Removed}</span>}
          {dd.Changed > 0 && <span className="text-[#dcdcaa]">~{dd.Changed}</span>}
        </div>
      </button>
      {expanded && (
        <div className="border-t border-[#252525]">
          {loading && <div className="flex items-center gap-2 px-6 py-2 text-[11px] text-[#858585]"><Spinner />Loading…</div>}
          {!loading && rows.length > 0 && (
            <div className="overflow-x-auto">
              <table className="text-[11px] font-mono w-full">
                <thead><tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585]">
                  <th className="px-3 py-1 text-left w-12">st</th>
                  {allCols.map(c => <th key={c} className="px-3 py-1 text-left">{c}</th>)}
                </tr></thead>
                <tbody>{rows.map((row, i) => {
                  const isAdded = row.Status === 'added', isRemoved = row.Status === 'removed'
                  return (
                    <tr key={i} className={`border-b border-[#2d2d2d] ${isAdded ? 'bg-[#4ec9b0]/8' : isRemoved ? 'bg-[#f44747]/8' : ''}`}>
                      <td className={`px-3 py-1 ${isAdded ? 'text-[#4ec9b0]' : isRemoved ? 'text-[#f44747]' : 'text-[#dcdcaa]'}`}>
                        {isAdded ? '+' : isRemoved ? '-' : '~'}
                      </td>
                      {allCols.map(col => {
                        const changed = row.Status === 'changed' && String(row.Old?.[col]) !== String(row.New?.[col])
                        return <td key={col} className="px-3 py-1 max-w-[180px] truncate text-[#cccccc]">
                          {changed
                            ? <><span className="text-[#f44747] line-through mr-1">{String(row.Old?.[col] ?? '')}</span><span className="text-[#4ec9b0]">{String(row.New?.[col] ?? '')}</span></>
                            : String((isRemoved ? row.Old : row.New)?.[col] ?? '')}
                        </td>
                      })}
                    </tr>
                  )
                })}</tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Explorer View ─────────────────────────────────────────────────────────────

type ViewProps = {
  recent: string[]
  addRecent: (p: string) => void
  removeRecent: (p: string) => void
  status: (t: string, k: 'ok'|'warn'|'err'|'idle') => void
  injectRef?: React.MutableRefObject<((path: string) => void) | null>
}

function ExplorerView({ recent, addRecent, removeRecent, status, injectRef }: ViewProps) {
  const [path, setPath] = useState('')
  const [schemaData, setSchemaData] = useState<any>(null)
  const [error, setError] = useState('')
  const [selectedTable, setSelectedTable] = useState<string | null>(null)
  const [tab, setTab] = useState<'schema' | 'data'>('schema')

  useEffect(() => {
    if (injectRef) injectRef.current = (p: string) => open(p)
    return () => { if (injectRef) injectRef.current = null }
  }, [])

  useEffect(() => {
    OnFileDrop((_, __, paths) => { if (paths.length) open(paths[0]) }, true)
    return () => OnFileDropOff()
  }, [])

  async function open(p: string) {
    setPath(p); setSelectedTable(null); setError(''); addRecent(p)
    try { const s = await Schema(p); setSchemaData(s); status(`${s.Tables?.length ?? 0} tables loaded`, 'ok') }
    catch (e: any) { setError(String(e)); setSchemaData(null); status('Failed to open database', 'err') }
  }

  const tables = schemaData?.Tables ?? []
  const selTable = tables.find((t: any) => t.Name === selectedTable)

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<Table2 size={14} />} title="DB Explorer" />
      <Toolbar>
        <DbPicker label="Database" path={path} onPick={async () => { const p = await OpenFile(); if (p) open(p) }}
          recent={recent} onRecent={open} removeRecent={removeRecent} />
        {schemaData && <span className="text-[#858585] text-[11px]">{tables.length} tables</span>}
      </Toolbar>
      {error && <ErrPanel message={error} />}
      {!schemaData && !error && <EmptyState icon={<Table2 size={48} />} text="Drop a .db file here to explore" sub="Browse schema, inspect columns, query table data" />}
      {schemaData && (
        <div className="flex flex-1 overflow-hidden">
          <div className="w-[180px] border-r border-[#252525] flex flex-col shrink-0 bg-[#252526]">
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wider text-[#858585] border-b border-[#252525]">Tables</div>
            <div className="flex-1 overflow-y-auto">
              {tables.map((t: any) => (
                <button key={t.Name} onClick={() => { setSelectedTable(t.Name); setTab('schema') }}
                  className={`w-full flex items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors
                    ${selectedTable === t.Name ? 'bg-[#094771] text-white' : 'text-[#cccccc] hover:bg-[#2a2d2e]'}`}>
                  <Table2 size={11} className="shrink-0 text-[#858585]" />
                  <span className="truncate font-mono">{t.Name}</span>
                  <span className="ml-auto text-[10px] text-[#585858]">{t.Columns?.length}</span>
                </button>
              ))}
            </div>
          </div>
          <div className="flex-1 flex flex-col overflow-hidden">
            {!selectedTable && <EmptyState icon={<Table2 size={32} />} text="Select a table" />}
            {selTable && (
              <>
                <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
                  <SubTab label="Schema" active={tab === 'schema'} onClick={() => setTab('schema')} />
                  <SubTab label="Data" active={tab === 'data'} onClick={() => setTab('data')} />
                  <div className="flex-1" />
                  <span className="text-[11px] text-[#585858] pr-1 font-mono">{selectedTable}</span>
                </div>
                <div className="flex-1 overflow-auto">
                  {tab === 'schema' && <TableInspector table={selTable} />}
                  {tab === 'data' && <TableDataView path={path} table={selectedTable!} />}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function TableInspector({ table }: { table: any }) {
  return (
    <div>
      <table className="w-full text-[12px]">
        <thead><tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
          <th className="text-left px-4 py-1.5">#</th>
          <th className="text-left px-3 py-1.5">Name</th>
          <th className="text-left px-3 py-1.5">Type</th>
          <th className="text-left px-3 py-1.5">Constraints</th>
        </tr></thead>
        <tbody>
          {(table.Columns ?? []).map((c: any, i: number) => (
            <tr key={c.Name} className="border-b border-[#2d2d2d] hover:bg-[#2a2d2e]">
              <td className="px-4 py-1.5 text-[#585858] font-mono">{i + 1}</td>
              <td className="px-3 py-1.5 font-mono text-[#9cdcfe]">
                {c.Name}{c.PK === 1 && <span className="ml-1.5 text-[10px] px-1 border border-[#dcdcaa]/40 text-[#dcdcaa] rounded-sm font-sans">PK</span>}
              </td>
              <td className="px-3 py-1.5 font-mono text-[#4ec9b0]">{c.Type || 'ANY'}</td>
              <td className="px-3 py-1.5 text-[#858585]">{c.NotNull ? 'NOT NULL' : ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {(table.Indexes ?? []).length > 0 && (
        <div className="border-t border-[#252525]">
          <div className="px-4 py-1.5 text-[10px] uppercase tracking-wider text-[#858585] bg-[#252526] border-b border-[#252525]">Indexes</div>
          {table.Indexes.map((idx: any) => (
            <div key={idx.Name} className="flex items-center gap-3 px-4 py-1.5 border-b border-[#2d2d2d] hover:bg-[#2a2d2e] font-mono text-[12px]">
              <Hash size={11} className={idx.Unique ? 'text-[#dcdcaa]' : 'text-[#585858]'} />
              <span className="text-[#cccccc]">{idx.Name}</span>
              {idx.Unique && <Badge label="UNIQUE" color="yellow" />}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function TableDataView({ path, table }: { path: string; table: string }) {
  const PAGE = 100
  const [rows, setRows] = useState<any>(null)
  const [page, setPage] = useState(0)
  const [loading, setLoading] = useState(false)
  useEffect(() => { setPage(0); setRows(null) }, [path, table])
  useEffect(() => {
    setLoading(true)
    QueryTable(path, table, PAGE, page * PAGE).then(setRows).catch(() => setRows(null)).finally(() => setLoading(false))
  }, [path, table, page])

  if (loading) return <div className="flex items-center gap-2 px-4 py-3 text-[12px] text-[#858585]"><Spinner />Loading…</div>
  if (!rows) return <div className="px-4 py-3 text-[12px] text-[#585858]">Failed to load</div>
  if (!rows.Rows?.length) return <div className="px-4 py-3 text-[12px] text-[#585858]">Table is empty</div>
  const totalPages = Math.ceil((rows.Total ?? 0) / PAGE)
  return (
    <div className="flex flex-col h-full">
      <div className="overflow-auto flex-1">
        <table className="text-[12px] font-mono w-full">
          <thead><tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
            {(rows.Columns ?? []).map((c: string) => <th key={c} className="text-left px-3 py-1.5 font-medium whitespace-nowrap">{c}</th>)}
          </tr></thead>
          <tbody>{(rows.Rows ?? []).map((row: any[], i: number) => (
            <tr key={i} className="border-b border-[#2d2d2d] hover:bg-[#2a2d2e]">
              {row.map((cell, j) => <td key={j} className="px-3 py-1 text-[#cccccc] max-w-[200px] truncate whitespace-nowrap">
                {cell === null ? <span className="text-[#585858] italic">NULL</span> : String(cell)}
              </td>)}
            </tr>
          ))}</tbody>
        </table>
      </div>
      {totalPages > 1 && (
        <div className="flex items-center gap-3 px-3 py-1.5 border-t border-[#252525] bg-[#252526] text-[11px] text-[#858585] shrink-0">
          <button onClick={() => setPage(p => Math.max(0, p - 1))} disabled={page === 0} className="disabled:opacity-30 hover:text-[#cccccc]"><ChevronLeft size={13} /></button>
          <span>Page {page + 1} / {totalPages} · {rows.Total} rows</span>
          <button onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))} disabled={page >= totalPages - 1} className="disabled:opacity-30 hover:text-[#cccccc]"><ChevronRight size={13} /></button>
        </div>
      )}
    </div>
  )
}

// ── Check View ────────────────────────────────────────────────────────────────

function CheckView({ recent, addRecent, removeRecent, status, injectRef }: ViewProps) {
  const [backupPath, setBackupPath] = useState('')
  const [refPath, setRefPath] = useState('')
  const [withData, setWithData] = useState(false)
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (injectRef) injectRef.current = (p: string) => { setBackupPath(p); addRecent(p) }
    return () => { if (injectRef) injectRef.current = null }
  }, [])

  useEffect(() => {
    OnFileDrop((_, __, paths) => {
      if (!paths.length) return
      const p = paths[0]; addRecent(p)
      if (!backupPath) setBackupPath(p); else setRefPath(p)
    }, true)
    return () => OnFileDropOff()
  }, [backupPath])

  async function pick(setter: (p: string) => void) {
    const p = await OpenFile(); if (p) { setter(p); addRecent(p) }
  }

  async function run() {
    setLoading(true); setError(''); setResult(null); status('Checking integrity…', 'idle')
    try {
      const r = await Check(backupPath, refPath, withData)
      setResult(r)
      status(r.Passed ? 'Check passed' : 'Check failed', r.Passed ? 'ok' : 'err')
    } catch (e: any) { setError(String(e)); status('Check error', 'err') }
    finally { setLoading(false) }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<ShieldCheck size={14} />} title="Integrity Check"
        meta={<span className="text-[10px] text-[#858585]">PRAGMA integrity_check · schema match · row counts</span>} />
      <Toolbar>
        <DbPicker label="Backup" path={backupPath} onPick={() => pick(setBackupPath)}
          recent={recent} onRecent={p => { setBackupPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <DbPicker label="Reference (Pro)" path={refPath} onPick={() => pick(setRefPath)}
          recent={recent} onRecent={p => { setRefPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <label className="flex items-center gap-1.5 text-[11px] text-[#858585] cursor-pointer ml-1">
          <input type="checkbox" checked={withData} onChange={e => setWithData(e.target.checked)} className="accent-[#007acc]" />
          Row counts (Pro)
        </label>
        <div className="flex-1" />
        <Btn onClick={run} disabled={!backupPath || loading}>
          {loading ? <><Spinner />Checking…</> : <><ShieldCheck size={12} />Check</>}
        </Btn>
      </Toolbar>

      {error && <ErrPanel message={error} />}

      {!result && !error && !loading && (
        <EmptyState icon={<ShieldCheck size={48} />}
          text="Select a backup database to verify its integrity"
          sub="Optionally add a reference DB to check schema + row count consistency" />
      )}

      {loading && (
        <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]"><Spinner />Running checks…</div>
      )}

      {result && (
        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          <div className="text-[11px] text-[#858585] font-mono mb-3">{result.Path}</div>

          <CheckRow
            icon={result.IntegrityOK ? <CheckCircle2 size={14} className="text-[#4ec9b0]" /> : <AlertCircle size={14} className="text-[#f44747]" />}
            label="File integrity"
            detail={result.IntegrityOK ? 'PRAGMA integrity_check passed' : 'CORRUPTED'}
            ok={result.IntegrityOK}
            errors={result.IntegrityErrors}
          />

          {result.SchemaOK != null && (
            <CheckRow
              icon={result.SchemaOK ? <CheckCircle2 size={14} className="text-[#4ec9b0]" /> : <AlertCircle size={14} className="text-[#f44747]" />}
              label="Schema match"
              detail={result.SchemaOK ? 'Identical to reference' : 'Schema drift detected'}
              ok={result.SchemaOK}
            >
              {!result.SchemaOK && result.SchemaDiff && <SchemaDiffTable rows={result.SchemaDiff.Schema ?? []} />}
            </CheckRow>
          )}

          {result.DataOK != null && (
            <CheckRow
              icon={result.DataOK ? <CheckCircle2 size={14} className="text-[#4ec9b0]" /> : <AlertTriangle size={14} className="text-[#dcdcaa]" />}
              label="Row counts"
              detail={result.DataOK ? 'All tables match' : 'Row count mismatch'}
              ok={result.DataOK}
            >
              {result.Tables?.length > 0 && (
                <table className="w-full text-[11px] font-mono mt-2">
                  <thead><tr className="text-[#858585]">
                    <th className="text-left py-1">Table</th>
                    <th className="text-right py-1">Backup</th>
                    <th className="text-right py-1">Reference</th>
                    <th className="text-right py-1">Match</th>
                  </tr></thead>
                  <tbody>{result.Tables.map((t: any, i: number) => (
                    <tr key={i} className="border-t border-[#2d2d2d]">
                      <td className="py-1 text-[#9cdcfe]">{t.Name}</td>
                      <td className="py-1 text-right text-[#cccccc]">{t.BackupRows}</td>
                      <td className="py-1 text-right text-[#cccccc]">{t.RefRows || '—'}</td>
                      <td className="py-1 text-right">
                        {t.RowsMatch == null ? '—' : t.RowsMatch ? <span className="text-[#4ec9b0]">✓</span> : <span className="text-[#dcdcaa]">!</span>}
                      </td>
                    </tr>
                  ))}</tbody>
                </table>
              )}
            </CheckRow>
          )}

          <div className={`px-4 py-3 rounded-sm border text-[13px] font-medium mt-4 flex items-center gap-2
            ${result.Passed ? 'bg-[#1a3a1a] border-[#4ec9b0]/40 text-[#4ec9b0]' : 'bg-[#5a1d1d] border-[#be1100]/40 text-[#f48771]'}`}>
            {result.Passed ? <CheckCircle2 size={14} /> : <AlertCircle size={14} />}
            {result.Passed ? 'All checks passed' : 'Check failed'}
          </div>
        </div>
      )}
    </div>
  )
}

function CheckRow({ icon, label, detail, ok, errors, children }: {
  icon: React.ReactNode; label: string; detail: string; ok: boolean
  errors?: string[]; children?: React.ReactNode
}) {
  const [open, setOpen] = useState(!ok)
  return (
    <div className={`rounded-sm border ${ok ? 'border-[#3c3c3c]' : 'border-[#f44747]/30'}`}>
      <button onClick={() => children && setOpen(o => !o)}
        className={`w-full flex items-center gap-3 px-4 py-2.5 text-left ${children ? 'cursor-pointer hover:bg-[#2a2d2e]' : ''}`}>
        {icon}
        <span className="text-[12px] font-medium text-[#cccccc]">{label}</span>
        <span className="text-[11px] text-[#858585] ml-1">{detail}</span>
        {children && <ChevronRight size={12} className={`ml-auto text-[#585858] transition-transform ${open ? 'rotate-90' : ''}`} />}
      </button>
      {open && errors?.map((e, i) => <div key={i} className="px-4 pb-2 text-[11px] text-[#f48771] font-mono">{e}</div>)}
      {open && children && <div className="px-4 pb-3 border-t border-[#3c3c3c]">{children}</div>}
    </div>
  )
}

// ── Migrate View ──────────────────────────────────────────────────────────────

function MigrateView({ recent, addRecent, removeRecent, status, injectRef }: ViewProps) {
  const [fromPath, setFromPath] = useState('')
  const [toPath, setToPath] = useState('')
  const [preview, setPreview] = useState<any>(null)
  const [applyResult, setApplyResult] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [applying, setApplying] = useState(false)
  const [confirmApply, setConfirmApply] = useState(false)

  useEffect(() => {
    if (injectRef) injectRef.current = (p: string) => { setFromPath(p); addRecent(p) }
    return () => { if (injectRef) injectRef.current = null }
  }, [])

  useEffect(() => {
    OnFileDrop((_, __, paths) => {
      if (!paths.length) return
      const p = paths[0]; addRecent(p)
      if (!fromPath) setFromPath(p); else setToPath(p)
    }, true)
    return () => OnFileDropOff()
  }, [fromPath])

  async function pick(setter: (p: string) => void) {
    const p = await OpenFile(); if (p) { setter(p); addRecent(p) }
  }

  async function generate() {
    setLoading(true); setError(''); setPreview(null); setApplyResult(null); setConfirmApply(false)
    status('Generating migration SQL…', 'idle')
    try {
      const r = await MigrateGenerate(fromPath, toPath)
      setPreview(r)
      status(r.Warnings?.length ? `${r.Warnings.length} warning(s)` : 'Migration generated', r.Warnings?.length ? 'warn' : 'ok')
    } catch (e: any) { setError(String(e)); status('Generation failed', 'err') }
    finally { setLoading(false) }
  }

  async function apply(dryRun: boolean) {
    setApplying(true); setApplyResult(null)
    status(dryRun ? 'Dry run…' : 'Applying migration…', 'idle')
    try {
      const r = await MigrateApply(fromPath, preview.SQL, dryRun)
      setApplyResult({ ...r, dryRun })
      setConfirmApply(false)
      status(dryRun ? 'Dry run complete' : `Applied ${r.Executed} statement(s)`, 'ok')
    } catch (e: any) { setError(String(e)); status('Apply failed', 'err') }
    finally { setApplying(false) }
  }

  async function saveSQL() {
    const p = await SaveFile('migration.sql')
    if (!p || !preview) return
    // Write via backend isn't available, copy to clipboard fallback
    try {
      await navigator.clipboard.writeText(preview.SQL)
      status('SQL copied to clipboard', 'ok')
    } catch { status('Copy failed', 'err') }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<GitMerge size={14} />} title="Migration Studio"
        meta={<span className="text-[10px] text-[#858585]">Generate · preview · apply with auto-backup</span>} />
      <Toolbar>
        <DbPicker label="From" path={fromPath} onPick={() => pick(setFromPath)}
          recent={recent} onRecent={p => { setFromPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <span className="text-[#585858] text-[11px]">→</span>
        <DbPicker label="Target schema" path={toPath} onPick={() => pick(setToPath)}
          recent={recent} onRecent={p => { setToPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <div className="flex-1" />
        <Btn onClick={generate} disabled={!fromPath || !toPath || loading}>
          {loading ? <><Spinner />Generating…</> : 'Generate SQL'}
        </Btn>
      </Toolbar>

      {error && <ErrPanel message={error} />}

      {!preview && !error && !loading && (
        <EmptyState icon={<GitMerge size={48} />}
          text="Select source and target schema databases"
          sub="Litescope generates safe SQLite migration SQL with VACUUM INTO backup" />
      )}

      {loading && <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]"><Spinner />Generating…</div>}

      {preview && (
        <div className="flex flex-col flex-1 overflow-hidden">
          <WarnBanner messages={preview.Warnings ?? []} />

          {applyResult && (
            <div className={`mx-4 mt-3 px-4 py-2.5 rounded-sm border text-[12px] flex items-center gap-2
              ${applyResult.DryRun ? 'bg-[#252526] border-[#3c3c3c] text-[#858585]' : 'bg-[#1a3a1a] border-[#4ec9b0]/40 text-[#4ec9b0]'}`}>
              <CheckCircle2 size={13} />
              {applyResult.DryRun
                ? `Dry run: ${applyResult.Executed} statement(s) validated — no changes made`
                : `Applied ${applyResult.Executed} statement(s) in ${applyResult.DurationMs}ms${applyResult.BackupPath ? ` · backup → ${applyResult.BackupPath.split('/').pop()}` : ''}`}
            </div>
          )}

          <div className="flex items-center gap-2 px-4 py-2 border-b border-[#252525] shrink-0">
            <span className="text-[11px] text-[#858585]">{preview.SQL.split('\n').filter((l: string) => l.trim() && !l.startsWith('--')).length} statements</span>
            <div className="flex-1" />
            <Btn variant="ghost" small onClick={saveSQL}><FileJson size={11} />Copy SQL</Btn>
            <Btn variant="ghost" small onClick={() => apply(true)} disabled={applying}>
              {applying ? <Spinner /> : <Eye size={11} />}Dry Run
            </Btn>
            {!confirmApply
              ? <Btn variant="danger" small onClick={() => setConfirmApply(true)} disabled={applying}>
                  <Play size={11} />Apply
                </Btn>
              : <div className="flex items-center gap-1.5">
                  <span className="text-[11px] text-[#dcdcaa]">Confirm?</span>
                  <Btn variant="danger" small onClick={() => apply(false)} disabled={applying}>
                    {applying ? <Spinner /> : null}Yes, apply
                  </Btn>
                  <Btn variant="ghost" small onClick={() => setConfirmApply(false)}>Cancel</Btn>
                </div>
            }
          </div>

          <div className="flex-1 overflow-y-auto p-4">
            <pre className="text-[12px] font-mono text-[#cccccc] whitespace-pre-wrap leading-relaxed">
              {preview.SQL.split('\n').map((line: string, i: number) => {
                const isComment = line.trim().startsWith('--')
                const isWarning = line.includes('WARNING')
                return (
                  <div key={i} className={isWarning ? 'text-[#dcdcaa]' : isComment ? 'text-[#6a9955]' : ''}>
                    {line || ' '}
                  </div>
                )
              })}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}

// ── Monitor View ──────────────────────────────────────────────────────────────

function MonitorView({ recent, addRecent, removeRecent, status, injectRef }: ViewProps) {
  const [dbPath, setDbPath] = useState('')
  const [baselinePath, setBaselinePath] = useState('')
  const [checkResult, setCheckResult] = useState<any>(null)
  const [snapInfo, setSnapInfo] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [tab, setTab] = useState<'check' | 'watch' | 'snapshot'>('check')
  const [watching, setWatching] = useState(false)
  const [watchInterval, setWatchInterval] = useState(30)
  const [watchWebhook, setWatchWebhook] = useState('')
  const [watchEvents, setWatchEvents] = useState<{at: string; kind: string; message: string; changes?: number}[]>([])

  useEffect(() => {
    if (injectRef) injectRef.current = (p: string) => { setDbPath(p); addRecent(p) }
    return () => { if (injectRef) injectRef.current = null }
  }, [])

  useEffect(() => {
    const unsub = EventsOn('monitor:event', (ev: any) => {
      setWatchEvents(prev => [ev, ...prev].slice(0, 100))
    })
    return () => { EventsOff('monitor:event') }
  }, [])

  async function pickDb() { const p = await OpenFile(); if (p) { setDbPath(p); addRecent(p) } }
  async function pickBaseline() { const p = await OpenFile(); if (p) setBaselinePath(p) }

  async function snapshot() {
    const out = await SaveFile('baseline.json')
    if (!out) return
    setLoading(true); setError(''); setSnapInfo(null); status('Capturing snapshot…', 'idle')
    try {
      const r = await MonitorSnapshot(dbPath, out)
      setSnapInfo(r); setBaselinePath(out)
      status(`Snapshot saved — ${r.TableCount} tables`, 'ok')
    } catch (e: any) { setError(String(e)); status('Snapshot failed', 'err') }
    finally { setLoading(false) }
  }

  async function runCheck() {
    setLoading(true); setError(''); setCheckResult(null); status('Checking for drift…', 'idle')
    try {
      const r = await MonitorCheck(dbPath, baselinePath)
      setCheckResult(r)
      status(r.has_drift ? `Drift detected — ${r.changes?.length} change(s)` : 'No drift detected', r.has_drift ? 'warn' : 'ok')
    } catch (e: any) { setError(String(e)); status('Check failed', 'err') }
    finally { setLoading(false) }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<Activity size={14} />} title="Drift Monitor"
        meta={<span className="text-[10px] text-[#858585]">snapshot · check · continuous watch</span>} />
      <Toolbar>
        <DbPicker label="Database" path={dbPath} onPick={pickDb}
          recent={recent} onRecent={p => { setDbPath(p); addRecent(p) }} removeRecent={removeRecent} />
        <div className="flex-1" />
        <Btn variant="ghost" onClick={snapshot} disabled={!dbPath || loading} small>
          {loading ? <Spinner /> : <Save size={11} />}Snapshot
        </Btn>
      </Toolbar>

      <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
        <SubTab label="Drift Check" active={tab === 'check'} onClick={() => setTab('check')} />
        <SubTab label="Watch" active={tab === 'watch'} onClick={() => setTab('watch')} />
        <SubTab label="Snapshot" active={tab === 'snapshot'} onClick={() => setTab('snapshot')} />
      </div>

      {error && <ErrPanel message={error} />}

      {tab === 'watch' && (
        <div className="flex flex-col flex-1 overflow-hidden">
          <div className="flex flex-col gap-2 px-4 py-3 bg-[#252526] border-b border-[#252525] shrink-0">
            <div className="flex items-center gap-3">
              <span className="text-[11px] text-[#858585]">Interval:</span>
              <select value={watchInterval} onChange={e => setWatchInterval(Number(e.target.value))}
                className="bg-[#3c3c3c] text-[#cccccc] text-[12px] px-2 py-1 rounded-sm border border-[#555] outline-none">
                <option value={10}>10s</option>
                <option value={30}>30s</option>
                <option value={60}>1m</option>
                <option value={300}>5m</option>
              </select>
              <div className="flex-1" />
              {watching
                ? <Btn variant="danger" small onClick={async () => { await MonitorWatchStop(); setWatching(false) }}>
                    <X size={11} />Stop Watch
                  </Btn>
                : <Btn onClick={async () => {
                    if (!dbPath || !baselinePath) return
                    await MonitorWatchStart(dbPath, baselinePath, watchInterval, watchWebhook)
                    setWatching(true)
                  }} disabled={!dbPath || !baselinePath}>
                    <Activity size={12} />Start Watch
                  </Btn>
              }
              {watching && <span className="flex items-center gap-1.5 text-[11px] text-[#4ec9b0]">
                <span className="w-2 h-2 rounded-full bg-[#4ec9b0] animate-pulse" />live
              </span>}
            </div>
            <div className="flex items-center gap-2">
              <span className="text-[11px] text-[#585858] shrink-0">Webhook:</span>
              <input
                value={watchWebhook}
                onChange={e => setWatchWebhook(e.target.value)}
                placeholder="Slack or Discord webhook URL (optional)"
                className="flex-1 bg-[#1e1e1e] border border-[#3c3c3c] text-[#cccccc] text-[11px] px-2 py-1 rounded-sm outline-none focus:border-[#007acc] placeholder-[#484848] font-mono"
              />
            </div>
          </div>
          {(!dbPath || !baselinePath) && (
            <div className="px-4 py-3 bg-[#2d2d00] border-b border-[#febc2e]/30 text-[11px] text-[#dcdcaa]">
              Select a database and baseline above, then click Start Watch.
            </div>
          )}
          <div className="flex-1 overflow-y-auto">
            {watchEvents.length === 0
              ? <EmptyState icon={<Activity size={36} />} text="No events yet" sub="Start watch to begin continuous drift monitoring" />
              : <div className="divide-y divide-[#252525]">
                  {watchEvents.map((ev, i) => (
                    <div key={i} className="flex items-start gap-3 px-4 py-2.5">
                      <span className={`text-[13px] mt-0.5 ${ev.kind === 'ok' ? 'text-[#4ec9b0]' : ev.kind === 'drift' ? 'text-[#dcdcaa]' : 'text-[#f44747]'}`}>
                        {ev.kind === 'ok' ? '●' : ev.kind === 'drift' ? '▲' : '✗'}
                      </span>
                      <div className="flex-1 min-w-0">
                        <div className={`text-[12px] font-medium ${ev.kind === 'ok' ? 'text-[#4ec9b0]' : ev.kind === 'drift' ? 'text-[#dcdcaa]' : 'text-[#f44747]'}`}>
                          {ev.message}
                        </div>
                        <div className="text-[10px] text-[#585858] mt-0.5 font-mono">{new Date(ev.at).toLocaleTimeString()}</div>
                      </div>
                    </div>
                  ))}
                </div>
            }
          </div>
        </div>
      )}

      {tab === 'snapshot' && (
        <div className="flex-1 overflow-y-auto p-4">
          <div className="max-w-lg">
            <p className="text-[12px] text-[#858585] mb-4 leading-relaxed">
              Capture the current schema as a baseline. Run this once after a confirmed-good deployment, then use Drift Check to detect unexpected changes.
            </p>
            {snapInfo && (
              <div className="bg-[#252526] border border-[#3c3c3c] rounded-sm p-4 mb-4 space-y-2 text-[12px]">
                <div className="flex gap-2"><span className="text-[#858585] w-20">Source</span><span className="text-[#cccccc] font-mono truncate">{snapInfo.Source}</span></div>
                <div className="flex gap-2"><span className="text-[#858585] w-20">Tables</span><span className="text-[#4ec9b0]">{snapInfo.TableCount}</span></div>
                <div className="flex gap-2"><span className="text-[#858585] w-20">Saved at</span><span className="text-[#cccccc]">{new Date(snapInfo.CapturedAt).toLocaleString()}</span></div>
                <div className="flex gap-2"><span className="text-[#858585] w-20">File</span><span className="text-[#569cd6] font-mono truncate">{baselinePath.split('/').pop()}</span></div>
              </div>
            )}
            {!snapInfo && <EmptyState icon={<Save size={32} />} text="Select a database above and click Snapshot" />}
          </div>
        </div>
      )}

      {tab === 'check' && (
        <div className="flex flex-col flex-1 overflow-hidden">
          <div className="flex items-center gap-2 px-3 py-2 bg-[#252526] border-b border-[#252525] shrink-0">
            <span className="text-[11px] text-[#858585]">Baseline:</span>
            <button onClick={pickBaseline}
              className="flex items-center gap-1.5 text-[12px] hover:text-[#cccccc] transition-colors truncate max-w-[300px]">
              <FileJson size={11} className="text-[#858585]" />
              <span className={baselinePath ? 'text-[#569cd6]' : 'text-[#585858]'}>
                {baselinePath ? baselinePath.split('/').pop() : 'select baseline.json…'}
              </span>
            </button>
            <div className="flex-1" />
            <Btn onClick={runCheck} disabled={!dbPath || !baselinePath || loading}>
              {loading ? <><Spinner />Checking…</> : <><Activity size={12} />Check Drift</>}
            </Btn>
          </div>

          <div className="flex-1 overflow-y-auto">
            {!checkResult && !error && !loading && (
              <EmptyState icon={<Activity size={48} />}
                text="Select database + baseline to check for drift"
                sub="Exit code 1 on drift — use in CI/CD pipelines" />
            )}
            {loading && <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]"><Spinner />Checking for drift…</div>}

            {checkResult && (
              <div className="p-4 space-y-3">
                <div className="text-[11px] text-[#858585] font-mono">
                  Baseline: {new Date(checkResult.baseline_at).toLocaleString()} · Checked: {new Date(checkResult.checked_at).toLocaleString()}
                </div>

                <div className={`px-4 py-3 rounded-sm border flex items-center gap-3 text-[13px] font-medium
                  ${checkResult.has_drift ? 'bg-[#3a2d00] border-[#febc2e]/40 text-[#dcdcaa]' : 'bg-[#1a3a1a] border-[#4ec9b0]/40 text-[#4ec9b0]'}`}>
                  {checkResult.has_drift ? <AlertTriangle size={15} /> : <CheckCircle2 size={15} />}
                  {checkResult.has_drift ? `Drift detected — ${checkResult.changes?.length ?? 0} change(s)` : 'No drift detected'}
                </div>

                {checkResult.has_drift && checkResult.changes?.length > 0 && (
                  <div className="bg-[#252526] border border-[#3c3c3c] rounded-sm overflow-hidden">
                    {checkResult.changes.map((td: any, i: number) => (
                      <div key={i} className="border-b border-[#2d2d2d] last:border-0 px-4 py-2.5">
                        <div className="flex items-center gap-2 mb-1">
                          {td.Added && <Badge label="ADDED" color="green" />}
                          {td.Removed && <Badge label="REMOVED" color="red" />}
                          {!td.Added && !td.Removed && <Badge label="MODIFIED" color="yellow" />}
                          <span className="font-mono text-[12px] text-[#9cdcfe]">{td.Name}</span>
                        </div>
                        {[...(td.AddedColumns ?? []).map((c: any) => `+ column ${c.Name} ${c.Type}`),
                          ...(td.RemovedColumns ?? []).map((c: any) => `- column ${c.Name}`),
                          ...(td.ChangedColumns ?? []).map((c: any) => `~ column ${c.Name}: ${c.Old.Type} → ${c.New.Type}`),
                        ].map((line, j) => <div key={j} className="text-[11px] font-mono text-[#858585] ml-2">{line}</div>)}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Fleet View ────────────────────────────────────────────────────────────────

interface FleetEntry { name: string; dsn: string; tags?: string[] }
interface FleetResult { database: string; state: string; error?: string; changes: number; duration_ms: number }
interface FleetSnapResult { database: string; tables: number; error?: string }

function FleetView({ status }: ViewProps) {
  const [provider, setProvider] = useState<'turso' | 'd1'>('turso')
  const [org, setOrg] = useState('')
  const [token, setToken] = useState('')
  const [dbToken, setDbToken] = useState('')
  const [databases, setDatabases] = useState<FleetEntry[]>([])
  const [results, setResults] = useState<FleetResult[]>([])
  const [snapResults, setSnapResults] = useState<FleetSnapResult[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [tab, setTab] = useState<'discover' | 'check'>('discover')

  async function discover() {
    if (!org || !token) return
    setLoading(true); setError(''); setDatabases([])
    try {
      const dbs = await FleetDiscover(provider, org, token, dbToken)
      setDatabases(dbs ?? [])
      status(`Discovered ${dbs?.length ?? 0} databases`, 'ok')
    } catch (e: any) { setError(String(e)); status('Discovery failed', 'err') }
    finally { setLoading(false) }
  }

  async function snapshotAll() {
    if (!databases.length) return
    setLoading(true); setError(''); setSnapResults([])
    try {
      const r = await FleetSnapshot(databases)
      setSnapResults(r ?? [])
      const ok = r?.filter((x: FleetSnapResult) => !x.error).length ?? 0
      status(`Snapshot: ${ok}/${r?.length} captured`, ok === r?.length ? 'ok' : 'warn')
    } catch (e: any) { setError(String(e)); status('Snapshot failed', 'err') }
    finally { setLoading(false) }
  }

  async function checkAll() {
    if (!databases.length) return
    setLoading(true); setError(''); setResults([]); setTab('check')
    try {
      const r = await FleetCheck(databases)
      setResults(r ?? [])
      const ok = r?.filter((x: FleetResult) => x.state === 'ok').length ?? 0
      const drift = r?.filter((x: FleetResult) => x.state === 'drift').length ?? 0
      status(`Fleet: ${ok} ok, ${drift} drift`, drift > 0 ? 'warn' : 'ok')
    } catch (e: any) { setError(String(e)); status('Check failed', 'err') }
    finally { setLoading(false) }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <PanelHeader icon={<Layers size={14} />} title="Fleet"
        meta={<span className="text-[10px] text-[#858585]">Turso &amp; Cloudflare D1 — parallel ops</span>} />

      <div className="shrink-0 bg-[#252526] border-b border-[#252525] px-4 py-3 space-y-2">
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-[#858585] w-16">Provider</span>
          <button onClick={() => setProvider('turso')}
            className={`px-3 py-1 text-[12px] rounded-sm border transition-colors ${provider === 'turso' ? 'border-[#4ec9b0] text-[#4ec9b0] bg-[#4ec9b0]/10' : 'border-[#3c3c3c] text-[#858585] hover:border-[#555]'}`}>
            Turso
          </button>
          <button onClick={() => setProvider('d1')}
            className={`px-3 py-1 text-[12px] rounded-sm border transition-colors ${provider === 'd1' ? 'border-[#dcdcaa] text-[#dcdcaa] bg-[#dcdcaa]/10' : 'border-[#3c3c3c] text-[#858585] hover:border-[#555]'}`}>
            D1
          </button>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-[#858585] w-16">{provider === 'turso' ? 'Org' : 'Account'}</span>
          <input value={org} onChange={e => setOrg(e.target.value)} placeholder={provider === 'turso' ? 'my-org' : 'cf-account-id'}
            className="flex-1 bg-[#3c3c3c] text-[#cccccc] text-[12px] px-2 py-1 rounded-sm border border-[#555] outline-none focus:border-[#007acc] font-mono" />
        </div>
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-[#858585] w-16">API Token</span>
          <input value={token} onChange={e => setToken(e.target.value)} type="password" placeholder="platform API token"
            className="flex-1 bg-[#3c3c3c] text-[#cccccc] text-[12px] px-2 py-1 rounded-sm border border-[#555] outline-none focus:border-[#007acc] font-mono" />
        </div>
        {provider === 'turso' && (
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-[#858585] w-16">DB Token</span>
            <input value={dbToken} onChange={e => setDbToken(e.target.value)} type="password" placeholder="group auth token"
              className="flex-1 bg-[#3c3c3c] text-[#cccccc] text-[12px] px-2 py-1 rounded-sm border border-[#555] outline-none focus:border-[#007acc] font-mono" />
          </div>
        )}
        <div className="flex gap-2 pt-1">
          <Btn onClick={discover} disabled={!org || !token || loading}>
            {loading ? <Spinner /> : <RefreshCw size={11} />}Discover
          </Btn>
          {databases.length > 0 && <>
            <Btn variant="ghost" onClick={snapshotAll} disabled={loading} small>
              <Save size={11} />Snapshot All
            </Btn>
            <Btn variant="ghost" onClick={checkAll} disabled={loading} small>
              <Activity size={11} />Check All
            </Btn>
          </>}
        </div>
      </div>

      {error && <ErrPanel message={error} />}

      {databases.length > 0 && (
        <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
          <SubTab label={`Databases (${databases.length})`} active={tab === 'discover'} onClick={() => setTab('discover')} />
          {results.length > 0 && <SubTab label="Check Results" active={tab === 'check'} onClick={() => setTab('check')} />}
        </div>
      )}

      <div className="flex-1 overflow-y-auto">
        {databases.length === 0 && !loading && (
          <EmptyState icon={<Layers size={36} />} text="No databases discovered yet"
            sub="Enter your Turso org or D1 account credentials above and click Discover" />
        )}
        {loading && <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]"><Spinner />Working…</div>}

        {tab === 'discover' && databases.length > 0 && !loading && (
          <div className="divide-y divide-[#252525]">
            {databases.map((db, i) => {
              const snap = snapResults.find(r => r.database === db.name)
              return (
                <div key={i} className="flex items-center gap-3 px-4 py-2.5 hover:bg-[#2a2d2e]">
                  <Database size={13} className="text-[#569cd6] shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="text-[12px] text-[#cccccc] font-medium">{db.name}</div>
                    <div className="text-[10px] text-[#585858] font-mono truncate">{db.dsn}</div>
                  </div>
                  {snap && (
                    <span className={`text-[10px] px-1.5 py-0.5 rounded-sm border ${snap.error ? 'border-[#f44747]/30 text-[#f44747]' : 'border-[#4ec9b0]/30 text-[#4ec9b0]'}`}>
                      {snap.error ? 'error' : `${snap.tables} tables`}
                    </span>
                  )}
                  {db.tags?.map(t => (
                    <span key={t} className="text-[9px] px-1 border border-[#3c3c3c] text-[#585858] rounded-sm">{t}</span>
                  ))}
                </div>
              )
            })}
          </div>
        )}

        {tab === 'check' && results.length > 0 && !loading && (
          <div>
            <div className="flex items-center gap-4 px-4 py-2 bg-[#252526] border-b border-[#252525] text-[11px]">
              {(['ok','drift','no-baseline','error'] as const).map(s => {
                const count = results.filter(r => r.state === s).length
                if (!count) return null
                const color = s === 'ok' ? 'text-[#4ec9b0]' : s === 'drift' ? 'text-[#dcdcaa]' : s === 'error' ? 'text-[#f44747]' : 'text-[#585858]'
                return <span key={s} className={color}>{count} {s}</span>
              })}
            </div>
            <div className="divide-y divide-[#252525]">
              {results.map((r, i) => (
                <div key={i} className="flex items-center gap-3 px-4 py-2.5 hover:bg-[#2a2d2e]">
                  <span className={`text-[14px] ${r.state === 'ok' ? 'text-[#4ec9b0]' : r.state === 'drift' ? 'text-[#dcdcaa]' : r.state === 'no-baseline' ? 'text-[#585858]' : 'text-[#f44747]'}`}>
                    {r.state === 'ok' ? '●' : r.state === 'drift' ? '▲' : r.state === 'no-baseline' ? '○' : '✗'}
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="text-[12px] text-[#cccccc]">{r.database}</div>
                    {r.error && <div className="text-[10px] text-[#f44747] truncate">{r.error}</div>}
                    {r.state === 'drift' && <div className="text-[10px] text-[#dcdcaa]">{r.changes} change(s) detected</div>}
                    {r.state === 'no-baseline' && <div className="text-[10px] text-[#585858]">run Snapshot All first</div>}
                  </div>
                  <span className={`text-[11px] font-medium ${r.state === 'ok' ? 'text-[#4ec9b0]' : r.state === 'drift' ? 'text-[#dcdcaa]' : r.state === 'no-baseline' ? 'text-[#585858]' : 'text-[#f44747]'}`}>
                    {r.state}
                  </span>
                  <span className="text-[10px] text-[#585858]">{r.duration_ms}ms</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
