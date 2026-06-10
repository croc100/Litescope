import { useState } from "react";

// ── Init data injected by extension ──────────────────────────────────────────

declare global {
  interface Window {
    __LITESCOPE_INIT__?: InitData;
  }
}

type InitData =
  | { mode: "table"; table: TableInfo; dbPath: string }
  | { mode: "diff"; result: DiffResult; oldPath: string; newPath: string }
  | { mode: "diff-loading"; oldPath: string; newPath: string }
  | { mode: "welcome" };

// ── Types ─────────────────────────────────────────────────────────────────────

interface ColumnInfo {
  Name: string;
  Type: string;
  NotNull: boolean;
  Default: string;
  PK: number;
}

interface IndexInfo {
  Name: string;
  Unique: boolean;
}

interface TableInfo {
  Name: string;
  Columns: ColumnInfo[];
  Indexes: IndexInfo[];
}

interface TableDiff {
  Name: string;
  Added: boolean;
  Removed: boolean;
  AddedColumns: ColumnInfo[] | null;
  RemovedColumns: ColumnInfo[] | null;
  ChangedColumns: { Name: string; Old: ColumnInfo; New: ColumnInfo }[] | null;
  AddedIndexes: IndexInfo[] | null;
  RemovedIndexes: IndexInfo[] | null;
}

interface DataDiff {
  Table: string;
  Added: number;
  Removed: number;
  Changed: number;
}

interface DiffResult {
  Schema: TableDiff[] | null;
  Data: DataDiff[] | null;
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function typeClass(t: string): string {
  const upper = t.toUpperCase();
  if (upper.includes("INT")) return "type-int";
  if (upper.includes("TEXT") || upper.includes("CHAR") || upper.includes("CLOB"))
    return "type-text";
  if (upper.includes("REAL") || upper.includes("FLOAT") || upper.includes("DOUBLE"))
    return "type-real";
  if (upper.includes("BLOB")) return "type-blob";
  return "type-default";
}

function basename(p: string) {
  return p.replace(/.*[\\/]/, "");
}

// ── Root ──────────────────────────────────────────────────────────────────────

export default function App() {
  const init: InitData = window.__LITESCOPE_INIT__ ?? { mode: "welcome" };

  if (init.mode === "table") return <TableView table={init.table} dbPath={init.dbPath} />;
  if (init.mode === "diff") return <DiffView result={init.result} oldPath={init.oldPath} newPath={init.newPath} />;
  if (init.mode === "diff-loading") return <DiffLoadingView oldPath={init.oldPath} newPath={init.newPath} />;
  return <WelcomeView />;
}

// ── Welcome ───────────────────────────────────────────────────────────────────

function WelcomeView() {
  return (
    <div className="empty-state">
      Select a table from the Litescope panel to inspect it.
    </div>
  );
}

// ── Diff Loading ──────────────────────────────────────────────────────────────

function DiffLoadingView({ oldPath, newPath }: { oldPath: string; newPath: string }) {
  return (
    <div>
      <div className="page-header">
        <h2>Comparing databases…</h2>
        <div className="meta">{basename(oldPath)} → {basename(newPath)}</div>
      </div>
      <div className="empty-state" style={{ opacity: 0.5 }}>Loading…</div>
    </div>
  );
}

// ── Table View ────────────────────────────────────────────────────────────────

function TableView({ table, dbPath }: { table: TableInfo; dbPath: string }) {
  const cols = table.Columns ?? [];
  const idxs = table.Indexes ?? [];

  return (
    <div>
      <div className="page-header">
        <h2>
          <span style={{ opacity: 0.5, fontSize: 12 }}>TABLE</span>
          {table.Name}
        </h2>
        <div className="meta">
          {basename(dbPath)} · {cols.length} columns · {idxs.length} indexes
        </div>
      </div>

      {/* Columns */}
      <table>
        <thead>
          <tr>
            <th style={{ width: 40 }}>#</th>
            <th>Name</th>
            <th>Type</th>
            <th>Constraints</th>
          </tr>
        </thead>
        <tbody>
          {cols.map((c, i) => (
            <tr key={c.Name}>
              <td style={{ opacity: 0.4, fontFamily: "var(--vscode-editor-font-family)" }}>{i + 1}</td>
              <td style={{ fontFamily: "var(--vscode-editor-font-family)" }}>
                {c.Name}
                {c.PK === 1 && <span className="badge badge-pk">PK</span>}
              </td>
              <td>
                <span className={typeClass(c.Type)} style={{ fontFamily: "var(--vscode-editor-font-family)", fontSize: 12 }}>
                  {c.Type || "ANY"}
                </span>
              </td>
              <td style={{ opacity: 0.6, fontSize: 12 }}>
                {c.NotNull && <span className="badge badge-notnull">NOT NULL</span>}
                {c.Default && (
                  <span style={{ marginLeft: 4, opacity: 0.7, fontFamily: "var(--vscode-editor-font-family)" }}>
                    DEFAULT {c.Default}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {/* Indexes */}
      {idxs.length > 0 && (
        <>
          <div className="section-header">Indexes</div>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
              </tr>
            </thead>
            <tbody>
              {idxs.map((idx) => (
                <tr key={idx.Name}>
                  <td style={{ fontFamily: "var(--vscode-editor-font-family)", fontSize: 12 }}>{idx.Name}</td>
                  <td>
                    {idx.Unique
                      ? <span className="tag diff-added">UNIQUE</span>
                      : <span style={{ opacity: 0.4, fontSize: 12 }}>INDEX</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

// ── Diff View ─────────────────────────────────────────────────────────────────

function DiffView({ result, oldPath, newPath }: { result: DiffResult; oldPath: string; newPath: string }) {
  const [tab, setTab] = useState<"schema" | "data">("schema");
  const schema = result.Schema ?? [];
  const data = result.Data ?? [];

  const isEmpty = schema.length === 0 && data.length === 0;

  const added    = schema.filter(t => t.Added).length;
  const removed  = schema.filter(t => t.Removed).length;
  const modified = schema.filter(t => !t.Added && !t.Removed).length;

  return (
    <div>
      <div className="page-header">
        <h2>
          <span style={{ opacity: 0.5, fontSize: 12 }}>DIFF</span>
          {basename(oldPath)}
          <span style={{ opacity: 0.4, fontSize: 13, fontWeight: 400 }}>→</span>
          {basename(newPath)}
        </h2>
        <div className="meta" style={{ display: "flex", gap: 12, marginTop: 4 }}>
          {added > 0    && <span className="diff-added">+{added} tables</span>}
          {removed > 0  && <span className="diff-removed">−{removed} tables</span>}
          {modified > 0 && <span className="diff-modified">~{modified} modified</span>}
          {data.length > 0 && <span style={{ opacity: 0.5 }}>{data.length} tables with data changes</span>}
        </div>
      </div>

      {isEmpty ? (
        <div className="empty-state diff-added">✓ Databases are identical</div>
      ) : (
        <>
          <div className="tabs">
            <button className={`tab ${tab === "schema" ? "active" : ""}`} onClick={() => setTab("schema")}>
              Schema
              {schema.length > 0 && <span className="count-badge">{schema.length}</span>}
            </button>
            <button className={`tab ${tab === "data" ? "active" : ""}`} onClick={() => setTab("data")}>
              Data
              {data.length > 0 && <span className="count-badge">{data.length}</span>}
            </button>
          </div>

          {tab === "schema" && <SchemaTab schema={schema} />}
          {tab === "data"   && <DataTab data={data} />}
        </>
      )}
    </div>
  );
}

function SchemaTab({ schema }: { schema: TableDiff[] }) {
  return (
    <table>
      <thead>
        <tr>
          <th>Table</th>
          <th>Change</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>
        {schema.map((td) => <SchemaRow key={td.Name} td={td} />)}
      </tbody>
    </table>
  );
}

function SchemaRow({ td }: { td: TableDiff }) {
  const [expanded, setExpanded] = useState(true);
  const details: { sign: string; text: string; cls: string }[] = [
    ...(td.AddedColumns   ?? []).map(c  => ({ sign: "+", text: `${c.Name}  ${c.Type}`, cls: "diff-added"    })),
    ...(td.RemovedColumns ?? []).map(c  => ({ sign: "−", text: `${c.Name}`,             cls: "diff-removed"  })),
    ...(td.ChangedColumns ?? []).map(c  => ({ sign: "~", text: `${c.Name}  ${c.Old.Type} → ${c.New.Type}`, cls: "diff-modified" })),
    ...(td.AddedIndexes   ?? []).map(ix => ({ sign: "+", text: `index ${ix.Name}${ix.Unique ? " UNIQUE" : ""}`, cls: "diff-added" })),
    ...(td.RemovedIndexes ?? []).map(ix => ({ sign: "−", text: `index ${ix.Name}`,     cls: "diff-removed"  })),
  ];

  const rowCls = td.Added ? "diff-row-added" : td.Removed ? "diff-row-removed" : "";

  return (
    <>
      <tr
        className={rowCls}
        style={{ cursor: details.length > 0 ? "pointer" : "default" }}
        onClick={() => details.length > 0 && setExpanded(e => !e)}
      >
        <td style={{ fontFamily: "var(--vscode-editor-font-family)", fontSize: 13 }}>
          <span className={td.Added ? "diff-added" : td.Removed ? "diff-removed" : ""}>
            {details.length > 0 && (
              <span style={{ opacity: 0.5, marginRight: 6, fontSize: 10 }}>
                {expanded ? "▾" : "▸"}
              </span>
            )}
            {td.Name}
          </span>
        </td>
        <td>
          {td.Added    && <span className="tag diff-added">ADDED</span>}
          {td.Removed  && <span className="tag diff-removed">REMOVED</span>}
          {!td.Added && !td.Removed && <span className="tag diff-modified">MODIFIED</span>}
        </td>
        <td style={{ opacity: 0.5, fontSize: 12 }}>
          {td.Added && `${td.AddedColumns?.length ?? 0} cols`}
          {!td.Added && !td.Removed && `${details.length} changes`}
        </td>
      </tr>
      {expanded && details.map((d, i) => (
        <tr key={i}>
          <td colSpan={3} style={{ paddingLeft: 32, fontFamily: "var(--vscode-editor-font-family)", fontSize: 12 }}>
            <span className={d.cls} style={{ marginRight: 8, fontWeight: 700 }}>{d.sign}</span>
            <span className={d.cls}>{d.text}</span>
          </td>
        </tr>
      ))}
    </>
  );
}

function DataTab({ data }: { data: DataDiff[] }) {
  return (
    <table>
      <thead>
        <tr>
          <th>Table</th>
          <th>Added</th>
          <th>Removed</th>
          <th>Changed</th>
        </tr>
      </thead>
      <tbody>
        {data.map((dd) => (
          <tr key={dd.Table}>
            <td style={{ fontFamily: "var(--vscode-editor-font-family)" }}>{dd.Table}</td>
            <td style={{ fontFamily: "var(--vscode-editor-font-family)" }}>
              {dd.Added > 0 ? <span className="diff-added">+{dd.Added}</span> : <span style={{ opacity: 0.3 }}>—</span>}
            </td>
            <td style={{ fontFamily: "var(--vscode-editor-font-family)" }}>
              {dd.Removed > 0 ? <span className="diff-removed">−{dd.Removed}</span> : <span style={{ opacity: 0.3 }}>—</span>}
            </td>
            <td style={{ fontFamily: "var(--vscode-editor-font-family)" }}>
              {dd.Changed > 0 ? <span className="diff-modified">~{dd.Changed}</span> : <span style={{ opacity: 0.3 }}>—</span>}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
