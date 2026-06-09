import { useState, useEffect, useCallback } from 'react'
import {
  GitCompare, Table2, FolderOpen, RefreshCw,
  Hash, AlertCircle, CheckCircle2, ChevronRight,
  ChevronLeft, Database, Clock, X
} from 'lucide-react'
import { Diff, OpenFile, Schema, QueryTable, TableDiffRows } from '../wailsjs/go/main/App'
import { OnFileDrop, OnFileDropOff } from '../wailsjs/runtime/runtime'

const RECENT_KEY = 'litescope_recent'
const MAX_RECENT = 8

function useRecentFiles() {
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

type View = 'diff' | 'explorer'

export default function App() {
  const [view, setView] = useState<View>('diff')
  const { recent, addRecent, removeRecent } = useRecentFiles()

  return (
    <div className="flex flex-col h-screen bg-[#1e1e1e] text-[#cccccc] text-[13px] font-sans overflow-hidden select-none">
      <div className="flex flex-1 overflow-hidden">
        <ActivityBar view={view} setView={setView} />
        <main className="flex-1 flex flex-col overflow-hidden">
          {view === 'diff'
            ? <DiffView recent={recent} addRecent={addRecent} removeRecent={removeRecent} />
            : <ExplorerView recent={recent} addRecent={addRecent} removeRecent={removeRecent} />}
        </main>
      </div>
      <StatusBar view={view} />
    </div>
  )
}

/* ─── Activity Bar ─────────────────────────────── */
function ActivityBar({ view, setView }: { view: View; setView: (v: View) => void }) {
  return (
    <div className="w-[48px] flex flex-col items-center bg-[#333333] border-r border-[#252525] shrink-0 pt-2 gap-1">
      <ActivityItem icon={<GitCompare size={22} strokeWidth={1.5} />} label="Diff" active={view === 'diff'} onClick={() => setView('diff')} />
      <ActivityItem icon={<Table2 size={22} strokeWidth={1.5} />} label="Explorer" active={view === 'explorer'} onClick={() => setView('explorer')} />
    </div>
  )
}

function ActivityItem({ icon, label, active, onClick }: { icon: React.ReactNode; label: string; active: boolean; onClick: () => void }) {
  return (
    <button onClick={onClick} title={label}
      className={`relative w-full h-[48px] flex items-center justify-center transition-colors
        ${active ? 'text-white' : 'text-[#858585] hover:text-[#cccccc]'}`}>
      {active && <span className="absolute left-0 top-2 bottom-2 w-[2px] bg-[#007acc] rounded-r-full" />}
      {icon}
    </button>
  )
}

/* ─── Status Bar ───────────────────────────────── */
function StatusBar({ view }: { view: View }) {
  return (
    <div className="h-[22px] bg-[#007acc] flex items-center px-3 gap-3 text-white text-[11px] shrink-0">
      <span className="flex items-center gap-1.5 opacity-90"><Database size={11} />Litescope</span>
      <span className="opacity-50">|</span>
      <span className="opacity-80">{view === 'diff' ? 'Diff' : 'Explorer'}</span>
    </div>
  )
}

/* ─── Shared: Recent Dropdown ──────────────────── */
function RecentDropdown({ recent, onSelect, onRemove, onClose }: {
  recent: string[]; onSelect: (p: string) => void; onRemove: (p: string) => void; onClose: () => void
}) {
  if (recent.length === 0) return (
    <div className="absolute top-full left-0 mt-px w-[320px] bg-[#252526] border border-[#3c3c3c] shadow-xl z-50 text-[12px]">
      <div className="px-3 py-2 text-[#585858]">No recent files</div>
    </div>
  )
  return (
    <div className="absolute top-full left-0 mt-px w-[320px] bg-[#252526] border border-[#3c3c3c] shadow-xl z-50">
      <div className="px-3 py-1 text-[11px] text-[#858585] uppercase tracking-wider border-b border-[#3c3c3c]">Recent</div>
      {recent.map(p => (
        <div key={p} className="flex items-center gap-2 px-3 py-1.5 hover:bg-[#2a2d2e] group">
          <Clock size={11} className="text-[#585858] shrink-0" />
          <button className="flex-1 text-left text-[12px] text-[#cccccc] truncate" onClick={() => { onSelect(p); onClose() }}>
            <span className="text-[#858585] text-[11px]">{p.split('/').slice(-2, -1)[0]}/</span>{p.split('/').pop()}
          </button>
          <button onClick={() => onRemove(p)} className="opacity-0 group-hover:opacity-100 text-[#585858] hover:text-[#cccccc]">
            <X size={11} />
          </button>
        </div>
      ))}
    </div>
  )
}

/* ─── Diff View ────────────────────────────────── */
function DiffView({ recent, addRecent, removeRecent }: { recent: string[]; addRecent: (p: string) => void; removeRecent: (p: string) => void }) {
  const [oldPath, setOldPath] = useState('')
  const [newPath, setNewPath] = useState('')
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [dropTarget, setDropTarget] = useState<'old' | 'new' | null>(null)

  // Drag & drop
  useEffect(() => {
    OnFileDrop((_, __, paths) => {
      if (!paths.length) return
      const p = paths[0]
      if (!oldPath) { setOldPath(p); addRecent(p) }
      else if (!newPath) { setNewPath(p); addRecent(p) }
      else { setNewPath(p); addRecent(p) }
      setDropTarget(null)
    }, true)
    return () => OnFileDropOff()
  }, [oldPath, newPath])

  async function pickFile(setter: (p: string) => void) {
    const path = await OpenFile()
    if (path) { setter(path); addRecent(path) }
  }

  async function runDiff() {
    if (!oldPath || !newPath) return
    setLoading(true); setError(''); setResult(null)
    try {
      setResult(await Diff(oldPath, newPath))
    } catch (e: any) { setError(String(e)) }
    finally { setLoading(false) }
  }

  const canCompare = !!oldPath && !!newPath && !loading

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex items-center h-[35px] bg-[#2d2d2d] border-b border-[#252525] shrink-0">
        <div className="flex items-center h-full px-4 border-r border-[#252525] bg-[#1e1e1e] text-[12px] gap-2">
          <GitCompare size={13} className="text-[#007acc]" /><span>Diff</span>
        </div>
      </div>

      <div className="flex items-center gap-0 h-[30px] bg-[#3c3c3c] border-b border-[#252525] px-2 shrink-0">
        <PathInput label="Before" path={oldPath} onPick={() => pickFile(setOldPath)}
          recent={recent} onRecent={p => { setOldPath(p); addRecent(p) }} onRemoveRecent={removeRecent} />
        <span className="px-2 text-[#585858]">→</span>
        <PathInput label="After" path={newPath} onPick={() => pickFile(setNewPath)}
          recent={recent} onRecent={p => { setNewPath(p); addRecent(p) }} onRemoveRecent={removeRecent} />
        <div className="flex-1" />
        <button onClick={runDiff} disabled={!canCompare}
          className={`h-[22px] px-4 text-[12px] font-medium rounded-sm flex items-center gap-1.5 transition-colors
            ${canCompare ? 'bg-[#0e639c] hover:bg-[#1177bb] text-white' : 'bg-[#3c3c3c] text-[#585858] cursor-not-allowed'}`}>
          {loading ? <><RefreshCw size={11} className="animate-spin" />Running…</> : 'Compare'}
        </button>
      </div>

      <div className="flex-1 overflow-y-auto">
        {error && <ErrorPanel message={error} />}
        {!result && !error && !loading && <DiffEmptyState oldSet={!!oldPath} newSet={!!newPath} />}
        {loading && <LoadingPanel />}
        {result && <DiffResult result={result} oldPath={oldPath} newPath={newPath} />}
      </div>
    </div>
  )
}

function PathInput({ label, path, onPick, recent, onRecent, onRemoveRecent }: {
  label: string; path: string; onPick: () => void
  recent: string[]; onRecent: (p: string) => void; onRemoveRecent: (p: string) => void
}) {
  const [open, setOpen] = useState(false)
  const filename = path ? path.split('/').pop() : null

  return (
    <div className="relative">
      <button onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 h-[22px] px-2 rounded-sm text-[12px] hover:bg-[#505050] transition-colors max-w-[280px]">
        <FolderOpen size={12} className="text-[#858585] shrink-0" />
        <span className="text-[#858585] shrink-0">{label}:</span>
        <span className={`truncate ${filename ? 'text-[#cccccc]' : 'text-[#585858]'}`}>
          {filename ?? 'select file…'}
        </span>
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute top-full left-0 mt-px bg-[#252526] border border-[#3c3c3c] shadow-xl z-50 w-[280px]">
            <button onClick={() => { onPick(); setOpen(false) }}
              className="w-full flex items-center gap-2 px-3 py-2 hover:bg-[#2a2d2e] text-[12px] border-b border-[#3c3c3c]">
              <FolderOpen size={12} className="text-[#858585]" />Browse…
            </button>
            {recent.length > 0 && (
              <>
                <div className="px-3 py-1 text-[11px] text-[#585858] uppercase tracking-wider">Recent</div>
                {recent.map(p => (
                  <div key={p} className="flex items-center gap-2 px-3 py-1.5 hover:bg-[#2a2d2e] group">
                    <Clock size={11} className="text-[#585858] shrink-0" />
                    <button className="flex-1 text-left text-[12px] truncate text-[#cccccc]"
                      onClick={() => { onRecent(p); setOpen(false) }}>
                      {p.split('/').pop()}
                    </button>
                    <button onClick={() => onRemoveRecent(p)} className="opacity-0 group-hover:opacity-100 text-[#585858] hover:text-[#cccccc]">
                      <X size={11} />
                    </button>
                  </div>
                ))}
              </>
            )}
          </div>
        </>
      )}
    </div>
  )
}

function DiffEmptyState({ oldSet, newSet }: { oldSet: boolean; newSet: boolean }) {
  return (
    <div className="flex-1 flex flex-col items-center justify-center gap-2 text-[12px] text-[#585858] h-full">
      {!oldSet && <span>Drop .db files here or select in the toolbar — Before → After → Compare</span>}
      {oldSet && !newSet && <span className="text-[#4ec9b0]">✓ Before selected — select the After database</span>}
      {oldSet && newSet && <span className="text-[#4ec9b0]">✓ Both selected — click Compare</span>}
    </div>
  )
}

function LoadingPanel() {
  return (
    <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]">
      <RefreshCw size={13} className="animate-spin text-[#007acc]" />Comparing databases…
    </div>
  )
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 m-3 px-3 py-2.5 bg-[#5a1d1d] border border-[#be1100] text-[#f48771] text-[12px] rounded-sm">
      <AlertCircle size={13} className="shrink-0 mt-0.5" /><span className="font-mono">{message}</span>
    </div>
  )
}

/* ─── Diff Result ──────────────────────────────── */
function DiffResult({ result, oldPath, newPath }: { result: any; oldPath: string; newPath: string }) {
  const schema: any[] = result?.Schema ?? []
  const data: any[] = result?.Data ?? []
  const [activeTab, setActiveTab] = useState<'schema' | 'data'>('schema')

  if (schema.length === 0 && data.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 gap-2 text-[#4ec9b0] text-[12px]">
        <CheckCircle2 size={14} />Databases are identical — no differences found
      </div>
    )
  }

  const schemaAdded = schema.filter(t => t.Added).length
  const schemaRemoved = schema.filter(t => t.Removed).length
  const schemaChanged = schema.filter(t => !t.Added && !t.Removed).length

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
        <ResultTab label="Schema" count={schema.length} active={activeTab === 'schema'} onClick={() => setActiveTab('schema')} />
        <ResultTab label="Data" count={data.length} active={activeTab === 'data'} onClick={() => setActiveTab('data')} />
        <div className="flex-1" />
        <div className="flex items-center gap-3 text-[11px] pr-1">
          {schemaAdded > 0 && <span className="text-[#4ec9b0]">+{schemaAdded} tables</span>}
          {schemaRemoved > 0 && <span className="text-[#f44747]">-{schemaRemoved} tables</span>}
          {schemaChanged > 0 && <span className="text-[#dcdcaa]">~{schemaChanged} modified</span>}
          {data.length > 0 && <span className="text-[#9cdcfe]">{data.length} data changes</span>}
        </div>
      </div>
      <div className="flex-1 overflow-y-auto">
        {activeTab === 'schema' && (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
                <th className="text-left px-4 py-1.5 font-medium w-6"></th>
                <th className="text-left px-3 py-1.5 font-medium">Table</th>
                <th className="text-left px-3 py-1.5 font-medium">Change</th>
                <th className="text-left px-3 py-1.5 font-medium">Details</th>
              </tr>
            </thead>
            <tbody>{schema.map((td, i) => <SchemaRow key={i} td={td} />)}</tbody>
          </table>
        )}
        {activeTab === 'data' && (
          <div>
            {data.map((dd, i) => (
              <DataDiffSection key={i} dd={dd} oldPath={oldPath} newPath={newPath} result={result} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function ResultTab({ label, count, active, onClick }: { label: string; count: number; active: boolean; onClick: () => void }) {
  return (
    <button onClick={onClick}
      className={`h-full px-3 text-[12px] flex items-center gap-1.5 border-t-2 transition-colors
        ${active ? 'border-[#007acc] text-[#cccccc] bg-[#1e1e1e]' : 'border-transparent text-[#858585] hover:text-[#cccccc]'}`}>
      {label}
      {count > 0 && <span className="px-1.5 py-0.5 rounded-full bg-[#3c3c3c] text-[10px] text-[#858585]">{count}</span>}
    </button>
  )
}

function SchemaRow({ td }: { td: any }) {
  const [expanded, setExpanded] = useState(true)
  const isAdded = td.Added, isRemoved = td.Removed

  const details = [
    ...(td.AddedColumns ?? []).map((c: any) => ({ sign: '+', text: `column  ${c.Name}  ${c.Type}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedColumns ?? []).map((c: any) => ({ sign: '-', text: `column  ${c.Name}`, cls: 'text-[#f44747]' })),
    ...(td.ChangedColumns ?? []).map((c: any) => ({ sign: '~', text: `column  ${c.Name}  ${c.Old.Type} → ${c.New.Type}`, cls: 'text-[#dcdcaa]' })),
    ...(td.AddedIndexes ?? []).map((idx: any) => ({ sign: '+', text: `index   ${idx.Name}${idx.Unique ? '  UNIQUE' : ''}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedIndexes ?? []).map((idx: any) => ({ sign: '-', text: `index   ${idx.Name}`, cls: 'text-[#f44747]' })),
  ]

  return (
    <>
      <tr className={`border-b border-[#2d2d2d] hover:bg-[#2a2d2e] cursor-pointer ${isAdded ? 'bg-[#4ec9b0]/5' : isRemoved ? 'bg-[#f44747]/5' : ''}`}
        onClick={() => details.length > 0 && setExpanded(e => !e)}>
        <td className="px-4 py-1.5 text-[#858585]">
          {details.length > 0 && <ChevronRight size={12} className={`transition-transform ${expanded ? 'rotate-90' : ''}`} />}
        </td>
        <td className="px-3 py-1.5 font-mono">
          <span className={isAdded ? 'text-[#4ec9b0]' : isRemoved ? 'text-[#f44747]' : 'text-[#9cdcfe]'}>{td.Name}</span>
        </td>
        <td className="px-3 py-1.5">
          {isAdded && <Badge label="ADDED" color="green" />}
          {isRemoved && <Badge label="REMOVED" color="red" />}
          {!isAdded && !isRemoved && <Badge label="MODIFIED" color="yellow" />}
        </td>
        <td className="px-3 py-1.5 text-[#858585] text-[11px]">
          {isAdded && `${td.AddedColumns?.length ?? 0} columns`}
          {isRemoved && 'table removed'}
          {!isAdded && !isRemoved && `${details.length} changes`}
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
    try {
      const r = await TableDiffRows(oldPath, newPath, dd.Table, pkCol, 100)
      setRows(r ?? [])
      setLoaded(true)
    } catch { setRows([]) }
    finally { setLoading(false) }
  }

  const allCols = rows.length > 0
    ? Object.keys(rows[0].New ?? rows[0].Old ?? {})
    : []

  return (
    <div className="border-b border-[#252525]">
      <button onClick={load}
        className="w-full flex items-center gap-3 px-4 py-2 hover:bg-[#2a2d2e] text-left">
        <ChevronRight size={12} className={`text-[#858585] transition-transform shrink-0 ${expanded ? 'rotate-90' : ''}`} />
        <span className="font-mono text-[#9cdcfe]">{dd.Table}</span>
        <div className="flex gap-3 text-[11px] ml-2">
          {dd.Added > 0 && <span className="text-[#4ec9b0]">+{dd.Added} rows</span>}
          {dd.Removed > 0 && <span className="text-[#f44747]">-{dd.Removed} rows</span>}
          {dd.Changed > 0 && <span className="text-[#dcdcaa]">~{dd.Changed} rows</span>}
        </div>
      </button>

      {expanded && (
        <div className="border-t border-[#252525]">
          {loading && <div className="flex items-center gap-2 px-6 py-2 text-[11px] text-[#858585]"><RefreshCw size={11} className="animate-spin" />Loading rows…</div>}
          {!loading && rows.length === 0 && <div className="px-6 py-2 text-[11px] text-[#585858]">No row-level data available</div>}
          {!loading && rows.length > 0 && (
            <div className="overflow-x-auto">
              <table className="text-[11px] font-mono w-full">
                <thead>
                  <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585]">
                    <th className="px-3 py-1 text-left font-medium w-16">status</th>
                    {allCols.map(c => <th key={c} className="px-3 py-1 text-left font-medium">{c}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row, i) => (
                    <DiffedRowView key={i} row={row} cols={allCols} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function DiffedRowView({ row, cols }: { row: any; cols: string[] }) {
  const isAdded = row.Status === 'added'
  const isRemoved = row.Status === 'removed'
  const isChanged = row.Status === 'changed'

  const rowCls = isAdded ? 'bg-[#4ec9b0]/8' : isRemoved ? 'bg-[#f44747]/8' : ''
  const statusCls = isAdded ? 'text-[#4ec9b0]' : isRemoved ? 'text-[#f44747]' : 'text-[#dcdcaa]'

  return (
    <tr className={`border-b border-[#2d2d2d] hover:brightness-110 ${rowCls}`}>
      <td className={`px-3 py-1 ${statusCls}`}>{isAdded ? '+' : isRemoved ? '-' : '~'}</td>
      {cols.map(col => {
        const oldVal = row.Old?.[col]
        const newVal = row.New?.[col]
        const changed = isChanged && String(oldVal) !== String(newVal)
        return (
          <td key={col} className="px-3 py-1 text-[#cccccc] max-w-[200px] truncate">
            {changed
              ? <><span className="text-[#f44747] line-through mr-1">{String(oldVal ?? '')}</span><span className="text-[#4ec9b0]">{String(newVal ?? '')}</span></>
              : <span>{String((isRemoved ? oldVal : newVal) ?? '')}</span>}
          </td>
        )
      })}
    </tr>
  )
}

function Badge({ label, color }: { label: string; color: 'green' | 'red' | 'yellow' }) {
  const cls = { green: 'bg-[#4ec9b0]/15 text-[#4ec9b0] border-[#4ec9b0]/30', red: 'bg-[#f44747]/15 text-[#f44747] border-[#f44747]/30', yellow: 'bg-[#dcdcaa]/15 text-[#dcdcaa] border-[#dcdcaa]/30' }[color]
  return <span className={`px-1.5 py-0.5 text-[10px] font-mono border rounded-sm ${cls}`}>{label}</span>
}

/* ─── Explorer View ────────────────────────────── */
function ExplorerView({ recent, addRecent, removeRecent }: { recent: string[]; addRecent: (p: string) => void; removeRecent: (p: string) => void }) {
  const [path, setPath] = useState('')
  const [schemaData, setSchemaData] = useState<any>(null)
  const [error, setError] = useState('')
  const [selectedTable, setSelectedTable] = useState<string | null>(null)
  const [explorerTab, setExplorerTab] = useState<'schema' | 'data'>('schema')

  // Drag & drop
  useEffect(() => {
    OnFileDrop((_, __, paths) => {
      if (paths.length) openDb(paths[0])
    }, true)
    return () => OnFileDropOff()
  }, [])

  async function openDb(p: string) {
    setPath(p); setSelectedTable(null); setError('')
    addRecent(p)
    try { setSchemaData(await Schema(p)) }
    catch (e: any) { setError(String(e)); setSchemaData(null) }
  }

  async function pickFile() {
    const p = await OpenFile()
    if (p) openDb(p)
  }

  const tables = schemaData?.Tables ?? []
  const selectedTableData = tables.find((t: any) => t.Name === selectedTable)

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex items-center h-[35px] bg-[#2d2d2d] border-b border-[#252525] shrink-0">
        <div className="flex items-center h-full px-4 border-r border-[#252525] bg-[#1e1e1e] text-[12px] gap-2">
          <Table2 size={13} className="text-[#007acc]" /><span>Explorer</span>
        </div>
      </div>

      <div className="flex items-center h-[30px] bg-[#3c3c3c] border-b border-[#252525] px-2 gap-2 shrink-0">
        <PathInput label="Database" path={path} onPick={pickFile}
          recent={recent} onRecent={openDb} onRemoveRecent={removeRecent} />
        {schemaData && <span className="text-[#858585] text-[11px]">{tables.length} tables</span>}
      </div>

      {error && <ErrorPanel message={error} />}

      {!schemaData && !error && (
        <div className="flex-1 flex items-center justify-center text-[#585858] text-[12px]">
          Drop a .db file here or click Database to open
        </div>
      )}

      {schemaData && (
        <div className="flex flex-1 overflow-hidden">
          {/* Table list */}
          <div className="w-[200px] border-r border-[#252525] flex flex-col shrink-0 bg-[#252526]">
            <div className="px-3 py-1.5 text-[11px] text-[#858585] uppercase tracking-wider font-medium border-b border-[#252525]">Tables</div>
            <div className="flex-1 overflow-y-auto">
              {tables.map((t: any) => (
                <button key={t.Name} onClick={() => { setSelectedTable(t.Name); setExplorerTab('schema') }}
                  className={`w-full flex items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors
                    ${selectedTable === t.Name ? 'bg-[#094771] text-white' : 'text-[#cccccc] hover:bg-[#2a2d2e]'}`}>
                  <Table2 size={12} className="shrink-0 text-[#858585]" />
                  <span className="truncate font-mono">{t.Name}</span>
                  <span className="ml-auto text-[10px] text-[#585858]">{t.Columns?.length}</span>
                </button>
              ))}
            </div>
          </div>

          {/* Main panel */}
          <div className="flex-1 flex flex-col overflow-hidden">
            {!selectedTable && (
              <div className="flex-1 flex items-center justify-center text-[#585858] text-[12px]">Select a table</div>
            )}
            {selectedTableData && (
              <>
                {/* Sub-tabs */}
                <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
                  <ResultTab label="Schema" count={0} active={explorerTab === 'schema'} onClick={() => setExplorerTab('schema')} />
                  <ResultTab label="Data" count={0} active={explorerTab === 'data'} onClick={() => setExplorerTab('data')} />
                  <div className="flex-1" />
                  <span className="text-[11px] text-[#858585] pr-1 font-mono">{selectedTable}</span>
                </div>
                <div className="flex-1 overflow-auto">
                  {explorerTab === 'schema' && <TableInspector table={selectedTableData} />}
                  {explorerTab === 'data' && <TableDataView path={path} table={selectedTable!} />}
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
        <thead>
          <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
            <th className="text-left px-4 py-1.5 font-medium">#</th>
            <th className="text-left px-3 py-1.5 font-medium">Name</th>
            <th className="text-left px-3 py-1.5 font-medium">Type</th>
            <th className="text-left px-3 py-1.5 font-medium">Constraints</th>
          </tr>
        </thead>
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
        <div className="border-t border-[#252525] mt-1">
          <div className="px-4 py-1.5 text-[11px] text-[#858585] uppercase tracking-wider font-medium bg-[#252526] border-b border-[#252525]">Indexes</div>
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

/* ─── Table Data View ──────────────────────────── */
function TableDataView({ path, table }: { path: string; table: string }) {
  const PAGE = 100
  const [rows, setRows] = useState<any>(null)
  const [page, setPage] = useState(0)
  const [loading, setLoading] = useState(false)

  useEffect(() => { setPage(0); setRows(null) }, [path, table])

  useEffect(() => {
    setLoading(true)
    QueryTable(path, table, PAGE, page * PAGE)
      .then(setRows)
      .catch(() => setRows(null))
      .finally(() => setLoading(false))
  }, [path, table, page])

  if (loading) return <div className="flex items-center gap-2 px-4 py-3 text-[12px] text-[#858585]"><RefreshCw size={12} className="animate-spin" />Loading…</div>
  if (!rows) return <div className="px-4 py-3 text-[12px] text-[#585858]">Failed to load data</div>
  if (rows.Rows?.length === 0) return <div className="px-4 py-3 text-[12px] text-[#585858]">Table is empty</div>

  const totalPages = Math.ceil((rows.Total ?? 0) / PAGE)

  return (
    <div className="flex flex-col h-full">
      <div className="overflow-auto flex-1">
        <table className="text-[12px] font-mono w-full">
          <thead>
            <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px] sticky top-0">
              {(rows.Columns ?? []).map((c: string) => (
                <th key={c} className="text-left px-3 py-1.5 font-medium whitespace-nowrap">{c}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {(rows.Rows ?? []).map((row: any[], i: number) => (
              <tr key={i} className="border-b border-[#2d2d2d] hover:bg-[#2a2d2e]">
                {row.map((cell, j) => (
                  <td key={j} className="px-3 py-1 text-[#cccccc] max-w-[240px] truncate whitespace-nowrap">
                    {cell === null ? <span className="text-[#585858] italic">NULL</span> : String(cell)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {totalPages > 1 && (
        <div className="flex items-center gap-3 px-3 py-1.5 border-t border-[#252525] bg-[#252526] text-[11px] text-[#858585] shrink-0">
          <button onClick={() => setPage(p => Math.max(0, p - 1))} disabled={page === 0}
            className="disabled:opacity-30 hover:text-[#cccccc]"><ChevronLeft size={13} /></button>
          <span>Page {page + 1} / {totalPages} · {rows.Total} rows</span>
          <button onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))} disabled={page >= totalPages - 1}
            className="disabled:opacity-30 hover:text-[#cccccc]"><ChevronRight size={13} /></button>
        </div>
      )}
    </div>
  )
}
