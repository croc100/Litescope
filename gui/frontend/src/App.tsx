import { useState } from 'react'
import {
  GitCompare, Database, Table2, FolderOpen,
  RefreshCw, Hash, AlertCircle, CheckCircle2, ChevronRight
} from 'lucide-react'
import { Diff, OpenFile, Schema } from '../wailsjs/go/main/App'

type View = 'diff' | 'explorer'

export default function App() {
  const [view, setView] = useState<View>('diff')

  return (
    <div className="flex flex-col h-screen bg-[#1e1e1e] text-[#cccccc] text-[13px] font-sans overflow-hidden select-none">
      <div className="flex flex-1 overflow-hidden">
        <ActivityBar view={view} setView={setView} />
        <main className="flex-1 flex flex-col overflow-hidden">
          {view === 'diff' ? <DiffView /> : <ExplorerView />}
        </main>
      </div>
      <StatusBar view={view} />
    </div>
  )
}

/* ─── Activity Bar ──────────────────────────────────────────── */
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
    <button
      onClick={onClick}
      title={label}
      className={`relative w-full h-[48px] flex items-center justify-center transition-colors group
        ${active ? 'text-[#ffffff]' : 'text-[#858585] hover:text-[#cccccc]'}`}
    >
      {active && <span className="absolute left-0 top-[8px] bottom-[8px] w-[2px] bg-[#007acc] rounded-r-full" />}
      {icon}
    </button>
  )
}

/* ─── Status Bar ────────────────────────────────────────────── */
function StatusBar({ view }: { view: View }) {
  return (
    <div className="h-[22px] bg-[#007acc] flex items-center px-3 gap-4 text-white text-[11px] shrink-0">
      <span className="flex items-center gap-1.5 opacity-90">
        <Database size={11} />
        Litescope
      </span>
      <span className="opacity-60">|</span>
      <span className="opacity-80">{view === 'diff' ? 'Diff' : 'Explorer'}</span>
    </div>
  )
}

/* ─── Diff View ─────────────────────────────────────────────── */
function DiffView() {
  const [oldPath, setOldPath] = useState('')
  const [newPath, setNewPath] = useState('')
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function pickFile(setter: (p: string) => void) {
    const path = await OpenFile()
    if (path) setter(path)
  }

  async function runDiff() {
    if (!oldPath || !newPath) return
    setLoading(true)
    setError('')
    setResult(null)
    try {
      const res = await Diff(oldPath, newPath)
      setResult(res)
    } catch (e: any) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }

  const canCompare = !!oldPath && !!newPath && !loading

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Tab bar */}
      <div className="flex items-center h-[35px] bg-[#2d2d2d] border-b border-[#252525] px-0 shrink-0">
        <div className="flex items-center h-full px-4 border-r border-[#252525] bg-[#1e1e1e] text-[#cccccc] text-[12px] gap-2">
          <GitCompare size={13} className="text-[#007acc]" />
          <span>Diff</span>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex items-center gap-0 h-[30px] bg-[#3c3c3c] border-b border-[#252525] px-2 shrink-0">
        <PathInput label="Before" path={oldPath} onPick={() => pickFile(setOldPath)} />
        <span className="px-2 text-[#858585]">→</span>
        <PathInput label="After" path={newPath} onPick={() => pickFile(setNewPath)} />
        <div className="flex-1" />
        <button
          onClick={runDiff}
          disabled={!canCompare}
          className={`h-[22px] px-3 text-[12px] font-medium rounded-sm flex items-center gap-1.5 transition-colors
            ${canCompare
              ? 'bg-[#0e639c] hover:bg-[#1177bb] text-white'
              : 'bg-[#3c3c3c] text-[#585858] cursor-not-allowed'}`}
        >
          {loading ? <><RefreshCw size={11} className="animate-spin" /> Running…</> : <>Compare</>}
        </button>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        {error && <ErrorPanel message={error} />}
        {!result && !error && !loading && <DiffEmptyState oldSet={!!oldPath} newSet={!!newPath} />}
        {loading && <LoadingPanel />}
        {result && <DiffResult result={result} oldPath={oldPath} newPath={newPath} />}
      </div>
    </div>
  )
}

function PathInput({ label, path, onPick }: { label: string; path: string; onPick: () => void }) {
  const filename = path ? path.split('/').pop() : null
  return (
    <button
      onClick={onPick}
      className="flex items-center gap-1.5 h-[22px] px-2 rounded-sm text-[12px] hover:bg-[#505050] transition-colors max-w-[280px]"
    >
      <FolderOpen size={12} className="text-[#858585] shrink-0" />
      <span className="text-[#858585] shrink-0">{label}:</span>
      <span className={`truncate ${filename ? 'text-[#cccccc]' : 'text-[#585858]'}`}>
        {filename ?? 'select file…'}
      </span>
    </button>
  )
}

function DiffEmptyState({ oldSet, newSet }: { oldSet: boolean; newSet: boolean }) {
  const bothSet = oldSet && newSet
  return (
    <div className="flex-1 flex flex-col items-center justify-center gap-2 text-[12px] text-[#585858] h-full">
      {!oldSet && !newSet && <span>Select Before and After databases in the toolbar above</span>}
      {oldSet && !newSet && <span className="text-[#4ec9b0]">✓ Before selected — now select the After database</span>}
      {oldSet && newSet && <span className="text-[#4ec9b0]">✓ Both files selected — click Compare to run</span>}
    </div>
  )
}

function LoadingPanel() {
  return (
    <div className="flex items-center justify-center h-32 gap-2 text-[#858585] text-[12px]">
      <RefreshCw size={13} className="animate-spin text-[#007acc]" />
      Comparing databases…
    </div>
  )
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 m-3 px-3 py-2.5 bg-[#5a1d1d] border border-[#be1100] text-[#f48771] text-[12px] rounded-sm">
      <AlertCircle size={13} className="shrink-0 mt-0.5" />
      <span className="font-mono">{message}</span>
    </div>
  )
}

function DiffResult({ result, oldPath, newPath }: { result: any; oldPath: string; newPath: string }) {
  const schema: any[] = result?.Schema ?? []
  const data: any[] = result?.Data ?? []
  const [activeTab, setActiveTab] = useState<'schema' | 'data'>('schema')

  const schemaAdded = schema.filter(t => t.Added).length
  const schemaRemoved = schema.filter(t => t.Removed).length
  const schemaChanged = schema.filter(t => !t.Added && !t.Removed).length
  const dataChanged = data.length

  if (schema.length === 0 && data.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 gap-2 text-[#4ec9b0] text-[12px]">
        <CheckCircle2 size={14} /> Databases are identical — no differences found
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      {/* Result tab bar */}
      <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-2 gap-1 shrink-0">
        <ResultTab label="Schema" count={schema.length} active={activeTab === 'schema'} onClick={() => setActiveTab('schema')} />
        <ResultTab label="Data" count={data.length} active={activeTab === 'data'} onClick={() => setActiveTab('data')} />
        <div className="flex-1" />
        <div className="flex items-center gap-3 text-[11px] pr-1">
          {schemaAdded > 0 && <span className="text-[#4ec9b0]">+{schemaAdded} tables</span>}
          {schemaRemoved > 0 && <span className="text-[#f44747]">-{schemaRemoved} tables</span>}
          {schemaChanged > 0 && <span className="text-[#dcdcaa]">~{schemaChanged} modified</span>}
          {dataChanged > 0 && <span className="text-[#9cdcfe]">{dataChanged} data changes</span>}
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        {activeTab === 'schema' && (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px]">
                <th className="text-left px-4 py-1.5 font-medium w-6"></th>
                <th className="text-left px-3 py-1.5 font-medium">Table</th>
                <th className="text-left px-3 py-1.5 font-medium">Change</th>
                <th className="text-left px-3 py-1.5 font-medium">Details</th>
              </tr>
            </thead>
            <tbody>
              {schema.map((td, i) => <SchemaRow key={i} td={td} />)}
            </tbody>
          </table>
        )}
        {activeTab === 'data' && (
          <table className="w-full text-[12px]">
            <thead>
              <tr className="bg-[#252526] border-b border-[#3c3c3c] text-[#858585] text-[11px]">
                <th className="text-left px-4 py-1.5 font-medium">Table</th>
                <th className="text-left px-4 py-1.5 font-medium">Added</th>
                <th className="text-left px-4 py-1.5 font-medium">Removed</th>
                <th className="text-left px-4 py-1.5 font-medium">Changed</th>
              </tr>
            </thead>
            <tbody>
              {data.map((dd, i) => <DataRow key={i} dd={dd} />)}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

function ResultTab({ label, count, active, onClick }: { label: string; count: number; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={`h-full px-3 text-[12px] flex items-center gap-1.5 border-t-2 transition-colors
        ${active
          ? 'border-[#007acc] text-[#cccccc] bg-[#1e1e1e]'
          : 'border-transparent text-[#858585] hover:text-[#cccccc]'}`}
    >
      {label}
      {count > 0 && (
        <span className="px-1.5 py-0.5 rounded-full bg-[#3c3c3c] text-[10px] text-[#858585]">{count}</span>
      )}
    </button>
  )
}

function SchemaRow({ td }: { td: any }) {
  const [expanded, setExpanded] = useState(true)
  const isAdded = td.Added
  const isRemoved = td.Removed

  const details = [
    ...(td.AddedColumns ?? []).map((c: any) => ({ sign: '+', text: `column  ${c.Name}  ${c.Type}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedColumns ?? []).map((c: any) => ({ sign: '-', text: `column  ${c.Name}`, cls: 'text-[#f44747]' })),
    ...(td.ChangedColumns ?? []).map((c: any) => ({ sign: '~', text: `column  ${c.Name}  ${c.Old.Type} → ${c.New.Type}`, cls: 'text-[#dcdcaa]' })),
    ...(td.AddedIndexes ?? []).map((idx: any) => ({ sign: '+', text: `index   ${idx.Name}${idx.Unique ? '  UNIQUE' : ''}`, cls: 'text-[#4ec9b0]' })),
    ...(td.RemovedIndexes ?? []).map((idx: any) => ({ sign: '-', text: `index   ${idx.Name}`, cls: 'text-[#f44747]' })),
  ]

  const rowBg = isAdded ? 'bg-[#4ec9b0]/5' : isRemoved ? 'bg-[#f44747]/5' : ''

  return (
    <>
      <tr
        className={`border-b border-[#2d2d2d] hover:bg-[#2a2d2e] cursor-pointer ${rowBg}`}
        onClick={() => details.length > 0 && setExpanded(e => !e)}
      >
        <td className="px-4 py-1.5 text-[#858585]">
          {details.length > 0 && (
            <ChevronRight size={12} className={`transition-transform ${expanded ? 'rotate-90' : ''}`} />
          )}
        </td>
        <td className="px-3 py-1.5 font-mono">
          <span className={isAdded ? 'text-[#4ec9b0]' : isRemoved ? 'text-[#f44747]' : 'text-[#9cdcfe]'}>
            {td.Name}
          </span>
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
          <td></td>
          <td colSpan={3} className="px-3 py-1 font-mono text-[11px]">
            <span className={`${d.cls} opacity-60 mr-3`}>{d.sign}</span>
            <span className={d.cls}>{d.text}</span>
          </td>
        </tr>
      ))}
    </>
  )
}

function DataRow({ dd }: { dd: any }) {
  return (
    <tr className="border-b border-[#2d2d2d] hover:bg-[#2a2d2e]">
      <td className="px-4 py-1.5 font-mono text-[#9cdcfe]">{dd.Table}</td>
      <td className="px-4 py-1.5 font-mono">{dd.Added > 0 ? <span className="text-[#4ec9b0]">+{dd.Added}</span> : <span className="text-[#585858]">—</span>}</td>
      <td className="px-4 py-1.5 font-mono">{dd.Removed > 0 ? <span className="text-[#f44747]">-{dd.Removed}</span> : <span className="text-[#585858]">—</span>}</td>
      <td className="px-4 py-1.5 font-mono">{dd.Changed > 0 ? <span className="text-[#dcdcaa]">~{dd.Changed}</span> : <span className="text-[#585858]">—</span>}</td>
    </tr>
  )
}

function Badge({ label, color }: { label: string; color: 'green' | 'red' | 'yellow' }) {
  const cls = {
    green: 'bg-[#4ec9b0]/15 text-[#4ec9b0] border-[#4ec9b0]/30',
    red: 'bg-[#f44747]/15 text-[#f44747] border-[#f44747]/30',
    yellow: 'bg-[#dcdcaa]/15 text-[#dcdcaa] border-[#dcdcaa]/30',
  }[color]
  return <span className={`px-1.5 py-0.5 text-[10px] font-mono border rounded-sm ${cls}`}>{label}</span>
}

/* ─── Explorer View ─────────────────────────────────────────── */
function ExplorerView() {
  const [path, setPath] = useState('')
  const [schema, setSchema] = useState<any>(null)
  const [error, setError] = useState('')
  const [selectedTable, setSelectedTable] = useState<string | null>(null)

  async function pickFile() {
    const p = await OpenFile()
    if (!p) return
    setPath(p)
    setSelectedTable(null)
    try {
      const s = await Schema(p)
      setSchema(s)
      setError('')
    } catch (e: any) {
      setError(String(e))
      setSchema(null)
    }
  }

  const tables = schema?.Tables ?? []
  const selectedTableData = tables.find((t: any) => t.Name === selectedTable)

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Tab bar */}
      <div className="flex items-center h-[35px] bg-[#2d2d2d] border-b border-[#252525] px-0 shrink-0">
        <div className="flex items-center h-full px-4 border-r border-[#252525] bg-[#1e1e1e] text-[#cccccc] text-[12px] gap-2">
          <Table2 size={13} className="text-[#007acc]" />
          <span>Explorer</span>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex items-center h-[30px] bg-[#3c3c3c] border-b border-[#252525] px-2 gap-2 shrink-0">
        <PathInput label="Database" path={path} onPick={pickFile} />
        {schema && <span className="text-[#858585] text-[11px]">{tables.length} tables</span>}
      </div>

      {error && <ErrorPanel message={error} />}

      {!schema && !error && (
        <div className="flex-1 flex items-center justify-center text-[#585858] text-[12px]">
          No database open — click Database above to select a .db file
        </div>
      )}

      {schema && (
        <div className="flex flex-1 overflow-hidden">
          {/* Sidebar: table list */}
          <div className="w-[200px] border-r border-[#252525] flex flex-col shrink-0 bg-[#252526]">
            <div className="px-3 py-1.5 text-[11px] text-[#858585] uppercase tracking-wider font-medium border-b border-[#252525]">
              Tables
            </div>
            <div className="flex-1 overflow-y-auto">
              {tables.map((t: any) => (
                <button
                  key={t.Name}
                  onClick={() => setSelectedTable(t.Name)}
                  className={`w-full flex items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors
                    ${selectedTable === t.Name
                      ? 'bg-[#094771] text-[#ffffff]'
                      : 'text-[#cccccc] hover:bg-[#2a2d2e]'}`}
                >
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
              <div className="flex-1 flex items-center justify-center text-[#585858] text-[12px]">
                Select a table from the sidebar
              </div>
            )}
            {selectedTableData && <TableInspector table={selectedTableData} />}
          </div>
        </div>
      )}
    </div>
  )
}

function TableInspector({ table }: { table: any }) {
  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Inspector header */}
      <div className="flex items-center h-[30px] bg-[#2d2d2d] border-b border-[#252525] px-4 gap-2 shrink-0">
        <Table2 size={12} className="text-[#007acc]" />
        <span className="font-mono text-[12px] text-[#cccccc]">{table.Name}</span>
        <span className="text-[#585858] text-[11px]">· {table.Columns?.length} columns</span>
        {(table.Indexes ?? []).length > 0 && (
          <span className="text-[#585858] text-[11px]">· {table.Indexes.length} indexes</span>
        )}
      </div>

      <div className="flex-1 overflow-y-auto">
        {/* Columns table */}
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
                <td className="px-3 py-1.5 font-mono text-[#9cdcfe] flex items-center gap-2">
                  {c.Name}
                  {c.PK === 1 && <span className="text-[10px] px-1 py-0 border border-[#dcdcaa]/40 text-[#dcdcaa] rounded-sm font-sans">PK</span>}
                </td>
                <td className="px-3 py-1.5 font-mono text-[#4ec9b0]">{c.Type || 'ANY'}</td>
                <td className="px-3 py-1.5 text-[#858585]">{c.NotNull ? 'NOT NULL' : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>

        {/* Indexes */}
        {(table.Indexes ?? []).length > 0 && (
          <div className="border-t border-[#252525] mt-2">
            <div className="px-4 py-1.5 text-[11px] text-[#858585] uppercase tracking-wider font-medium bg-[#252526] border-b border-[#252525]">
              Indexes
            </div>
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
    </div>
  )
}
