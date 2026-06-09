import { useState } from 'react'
import { Diff, OpenFile, Schema } from '../wailsjs/go/main/App'

type View = 'diff' | 'explorer'

export default function App() {
  const [view, setView] = useState<View>('diff')

  return (
    <div className="flex h-screen bg-[#0d1117] text-[#c9d1d9] font-mono select-none">
      <Sidebar view={view} setView={setView} />
      <main className="flex-1 overflow-hidden">
        {view === 'diff' ? <DiffView /> : <ExplorerView />}
      </main>
    </div>
  )
}

function Sidebar({ view, setView }: { view: View; setView: (v: View) => void }) {
  const items: { id: View; label: string; icon: string }[] = [
    { id: 'diff', label: 'Diff', icon: '⇄' },
    { id: 'explorer', label: 'Explorer', icon: '⊞' },
  ]
  return (
    <aside className="w-14 flex flex-col items-center py-4 gap-2 border-r border-[#21262d] bg-[#010409]">
      <div className="text-[#58a6ff] text-xs font-bold mb-4">LS</div>
      {items.map(item => (
        <button
          key={item.id}
          onClick={() => setView(item.id)}
          title={item.label}
          className={`w-10 h-10 rounded-lg flex items-center justify-center text-lg transition-colors
            ${view === item.id
              ? 'bg-[#1f6feb] text-white'
              : 'text-[#8b949e] hover:text-[#c9d1d9] hover:bg-[#21262d]'}`}
        >
          {item.icon}
        </button>
      ))}
    </aside>
  )
}

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
    try {
      const res = await Diff(oldPath, newPath)
      setResult(res)
    } catch (e: any) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* toolbar */}
      <div className="flex items-center gap-3 px-4 py-3 border-b border-[#21262d] bg-[#010409]">
        <FileInput label="Old" value={oldPath} onPick={() => pickFile(setOldPath)} />
        <span className="text-[#8b949e]">→</span>
        <FileInput label="New" value={newPath} onPick={() => pickFile(setNewPath)} />
        <button
          onClick={runDiff}
          disabled={!oldPath || !newPath || loading}
          className="ml-auto px-4 py-1.5 rounded-md bg-[#1f6feb] text-white text-sm font-medium
            hover:bg-[#388bfd] disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          {loading ? 'Comparing…' : 'Compare'}
        </button>
      </div>

      {/* content */}
      <div className="flex-1 overflow-y-auto p-4">
        {error && <p className="text-[#f85149] text-sm">{error}</p>}
        {!result && !error && (
          <EmptyState message="Select two .db files and click Compare" />
        )}
        {result && <DiffResult result={result} />}
      </div>
    </div>
  )
}

function FileInput({ label, value, onPick }: { label: string; value: string; onPick: () => void }) {
  return (
    <button
      onClick={onPick}
      className="flex items-center gap-2 px-3 py-1.5 rounded-md border border-[#30363d]
        bg-[#0d1117] hover:border-[#58a6ff] text-sm transition-colors min-w-[180px] max-w-[280px]"
    >
      <span className="text-[#8b949e] shrink-0">{label}</span>
      <span className="text-[#58a6ff] truncate">{value ? value.split('/').pop() : 'Select file…'}</span>
    </button>
  )
}

function DiffResult({ result }: { result: any }) {
  const schema = result?.Schema ?? []
  const data = result?.Data ?? []

  return (
    <div className="space-y-6">
      {schema.length > 0 && (
        <section>
          <h2 className="text-xs uppercase tracking-widest text-[#8b949e] mb-3">Schema diff</h2>
          <div className="rounded-lg border border-[#21262d] overflow-hidden">
            {schema.map((td: any, i: number) => (
              <SchemaDiffRow key={i} td={td} />
            ))}
          </div>
        </section>
      )}
      {data.length > 0 && (
        <section>
          <h2 className="text-xs uppercase tracking-widest text-[#8b949e] mb-3">Data diff</h2>
          <div className="rounded-lg border border-[#21262d] overflow-hidden">
            {data.map((dd: any, i: number) => (
              <DataDiffRow key={i} dd={dd} />
            ))}
          </div>
        </section>
      )}
      {schema.length === 0 && data.length === 0 && (
        <EmptyState message="No differences found" />
      )}
    </div>
  )
}

function SchemaDiffRow({ td }: { td: any }) {
  const badge = td.Added ? { label: '+', cls: 'text-[#3fb950] bg-[#3fb95015]' }
    : td.Removed ? { label: '-', cls: 'text-[#f85149] bg-[#f8514915]' }
    : { label: '~', cls: 'text-[#d29922] bg-[#d2992215]' }

  const details = [
    ...(td.AddedColumns ?? []).map((c: any) => ({ sign: '+', text: `column ${c.Name} (${c.Type})`, cls: 'text-[#3fb950]' })),
    ...(td.RemovedColumns ?? []).map((c: any) => ({ sign: '-', text: `column ${c.Name}`, cls: 'text-[#f85149]' })),
    ...(td.ChangedColumns ?? []).map((c: any) => ({ sign: '~', text: `column ${c.Name} (${c.Old.Type} → ${c.New.Type})`, cls: 'text-[#d29922]' })),
    ...(td.AddedIndexes ?? []).map((idx: any) => ({ sign: '+', text: `index ${idx.Name}${idx.Unique ? ' UNIQUE' : ''}`, cls: 'text-[#3fb950]' })),
    ...(td.RemovedIndexes ?? []).map((idx: any) => ({ sign: '-', text: `index ${idx.Name}`, cls: 'text-[#f85149]' })),
  ]

  return (
    <div className="px-4 py-3 border-b border-[#21262d] last:border-0">
      <div className="flex items-center gap-3">
        <span className={`text-xs font-bold px-1.5 py-0.5 rounded ${badge.cls}`}>{badge.label}</span>
        <span className="font-semibold text-sm">{td.Name}</span>
        {td.Added && <span className="text-[#8b949e] text-xs">new table ({td.AddedColumns?.length ?? 0} columns)</span>}
        {td.Removed && <span className="text-[#8b949e] text-xs">table removed</span>}
      </div>
      {details.length > 0 && (
        <div className="mt-2 ml-8 space-y-1">
          {details.map((d, i) => (
            <div key={i} className={`text-xs ${d.cls}`}>{d.sign} {d.text}</div>
          ))}
        </div>
      )}
    </div>
  )
}

function DataDiffRow({ dd }: { dd: any }) {
  return (
    <div className="flex items-center gap-4 px-4 py-3 border-b border-[#21262d] last:border-0 text-sm">
      <span className="w-48 truncate font-semibold">{dd.Table}</span>
      {dd.Added > 0 && <span className="text-[#3fb950]">+{dd.Added} rows</span>}
      {dd.Removed > 0 && <span className="text-[#f85149]">-{dd.Removed} rows</span>}
      {dd.Changed > 0 && <span className="text-[#d29922]">~{dd.Changed} rows</span>}
    </div>
  )
}

function ExplorerView() {
  const [path, setPath] = useState('')
  const [schema, setSchema] = useState<any>(null)
  const [error, setError] = useState('')

  async function pickFile() {
    const p = await OpenFile()
    if (!p) return
    setPath(p)
    try {
      const s = await Schema(p)
      setSchema(s)
      setError('')
    } catch (e: any) {
      setError(String(e))
    }
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-3 px-4 py-3 border-b border-[#21262d] bg-[#010409]">
        <button
          onClick={pickFile}
          className="flex items-center gap-2 px-3 py-1.5 rounded-md border border-[#30363d]
            bg-[#0d1117] hover:border-[#58a6ff] text-sm transition-colors"
        >
          <span className="text-[#8b949e]">File</span>
          <span className="text-[#58a6ff]">{path ? path.split('/').pop() : 'Open database…'}</span>
        </button>
      </div>
      <div className="flex-1 overflow-y-auto p-4">
        {error && <p className="text-[#f85149] text-sm">{error}</p>}
        {!schema && !error && <EmptyState message="Open a .db file to explore its schema" />}
        {schema && <SchemaView schema={schema} />}
      </div>
    </div>
  )
}

function SchemaView({ schema }: { schema: any }) {
  return (
    <div className="space-y-3">
      {(schema.Tables ?? []).map((t: any) => (
        <div key={t.Name} className="rounded-lg border border-[#21262d] overflow-hidden">
          <div className="px-4 py-2 bg-[#161b22] text-sm font-semibold text-[#58a6ff]">{t.Name}</div>
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[#21262d]">
                <th className="text-left px-4 py-2 text-[#8b949e] font-normal">Column</th>
                <th className="text-left px-4 py-2 text-[#8b949e] font-normal">Type</th>
                <th className="text-left px-4 py-2 text-[#8b949e] font-normal">Flags</th>
              </tr>
            </thead>
            <tbody>
              {(t.Columns ?? []).map((c: any) => (
                <tr key={c.Name} className="border-b border-[#21262d] last:border-0 hover:bg-[#161b22]">
                  <td className="px-4 py-2 text-[#c9d1d9]">{c.Name}{c.PK === 1 && <span className="ml-1 text-[#d29922]">PK</span>}</td>
                  <td className="px-4 py-2 text-[#79c0ff]">{c.Type}</td>
                  <td className="px-4 py-2 text-[#8b949e]">{c.NotNull ? 'NOT NULL' : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {(t.Indexes ?? []).length > 0 && (
            <div className="px-4 py-2 border-t border-[#21262d] flex flex-wrap gap-2">
              {t.Indexes.map((idx: any) => (
                <span key={idx.Name} className="text-xs px-2 py-0.5 rounded bg-[#21262d] text-[#8b949e]">
                  {idx.Unique ? '⚡ ' : ''}{idx.Name}
                </span>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

function EmptyState({ message }: { message: string }) {
  return (
    <div className="flex items-center justify-center h-full text-[#8b949e] text-sm">
      {message}
    </div>
  )
}
