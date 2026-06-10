import * as vscode from "vscode";
import * as path from "path";
import * as fs from "fs";
import * as cp from "child_process";
import * as os from "os";

// ── Binary resolution ─────────────────────────────────────────────────────────

function getBinaryPath(context: vscode.ExtensionContext): string {
  const platform = os.platform();
  const arch = os.arch();
  const name = platform === "win32" ? "litescope.exe" : "litescope";
  const triple =
    platform === "darwin"
      ? arch === "arm64" ? "darwin-arm64" : "darwin-amd64"
      : platform === "linux"
      ? arch === "arm64" ? "linux-arm64" : "linux-amd64"
      : arch === "arm64" ? "windows-arm64" : "windows-amd64";
  return context.asAbsolutePath(path.join("bin", triple, name));
}

function runBinary(binPath: string, args: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    cp.execFile(binPath, args, { maxBuffer: 50 * 1024 * 1024 }, (err, stdout, stderr) => {
      if (err) reject(new Error(stderr || err.message));
      else resolve(stdout);
    });
  });
}

// ── Tree Data Types ───────────────────────────────────────────────────────────

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

interface SchemaResult {
  Tables: TableInfo[];
}

// ── Tree Nodes ────────────────────────────────────────────────────────────────

class DatabaseItem extends vscode.TreeItem {
  constructor(
    public readonly dbPath: string,
    public readonly schema: SchemaResult
  ) {
    super(path.basename(dbPath), vscode.TreeItemCollapsibleState.Expanded);
    this.description = `${schema.Tables?.length ?? 0} tables`;
    this.iconPath = new vscode.ThemeIcon("database");
    this.contextValue = "database";
    this.tooltip = dbPath;
  }
}

class TableItem extends vscode.TreeItem {
  constructor(
    public readonly table: TableInfo,
    public readonly dbPath: string
  ) {
    super(table.Name, vscode.TreeItemCollapsibleState.None);
    this.description = `${table.Columns?.length ?? 0} cols`;
    this.iconPath = new vscode.ThemeIcon("table");
    this.contextValue = "table";
    this.tooltip = `${table.Columns?.length ?? 0} columns, ${table.Indexes?.length ?? 0} indexes`;
    this.command = {
      command: "litescope.openTable",
      title: "Open Table",
      arguments: [this],
    };
  }
}

type LitescopeTreeItem = DatabaseItem | TableItem;

// ── Tree Provider ─────────────────────────────────────────────────────────────

class LitescopeTreeProvider
  implements vscode.TreeDataProvider<LitescopeTreeItem>
{
  private _onDidChangeTreeData = new vscode.EventEmitter<LitescopeTreeItem | undefined | void>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private databases: DatabaseItem[] = [];

  constructor(private readonly binPath: string) {}

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  getTreeItem(element: LitescopeTreeItem): vscode.TreeItem {
    return element;
  }

  getChildren(element?: LitescopeTreeItem): LitescopeTreeItem[] {
    if (!element) {
      return this.databases;
    }
    if (element instanceof DatabaseItem) {
      return (element.schema.Tables ?? []).map(
        (t) => new TableItem(t, element.dbPath)
      );
    }
    return [];
  }

  async addDatabase(dbPath: string): Promise<void> {
    // Don't add duplicates
    if (this.databases.find((d) => d.dbPath === dbPath)) {
      this.refresh();
      return;
    }
    const raw = await runBinary(this.binPath, ["schema", dbPath, "--format", "json"]);
    const schema: SchemaResult = JSON.parse(raw);
    this.databases.push(new DatabaseItem(dbPath, schema));
    this.refresh();
  }

  removeDatabase(dbPath: string): void {
    this.databases = this.databases.filter((d) => d.dbPath !== dbPath);
    this.refresh();
  }

  async refreshDatabase(item: DatabaseItem): Promise<void> {
    const idx = this.databases.indexOf(item);
    if (idx === -1) return;
    const raw = await runBinary(this.binPath, ["schema", item.dbPath, "--format", "json"]);
    const schema: SchemaResult = JSON.parse(raw);
    this.databases[idx] = new DatabaseItem(item.dbPath, schema);
    this.refresh();
  }

  getDatabases(): DatabaseItem[] {
    return this.databases;
  }
}

// ── Detail WebView Panel ──────────────────────────────────────────────────────

let detailPanel: vscode.WebviewPanel | undefined;

function getDetailPanel(context: vscode.ExtensionContext): vscode.WebviewPanel {
  if (detailPanel) {
    detailPanel.reveal(vscode.ViewColumn.One);
    return detailPanel;
  }

  detailPanel = vscode.window.createWebviewPanel(
    "litescope.detail",
    "Litescope",
    vscode.ViewColumn.One,
    {
      enableScripts: true,
      retainContextWhenHidden: true,
      localResourceRoots: [
        vscode.Uri.joinPath(context.extensionUri, "webview-ui", "dist"),
      ],
    }
  );

  detailPanel.iconPath = vscode.Uri.joinPath(context.extensionUri, "media", "icon.svg");

  detailPanel.onDidDispose(() => {
    detailPanel = undefined;
  });

  return detailPanel;
}

function buildDetailHtml(
  webview: vscode.Webview,
  context: vscode.ExtensionContext,
  initialData: unknown
): string {
  const distUri = vscode.Uri.joinPath(context.extensionUri, "webview-ui", "dist");
  const indexPath = path.join(context.extensionPath, "webview-ui", "dist", "index.html");

  let html: string;
  try {
    html = fs.readFileSync(indexPath, "utf-8");
  } catch {
    return `<!DOCTYPE html><html><body style="color:var(--vscode-foreground);padding:2rem;font-family:var(--vscode-font-family)">
      <p>Build the webview first: <code>cd vscode && npm run build</code></p>
    </body></html>`;
  }

  const nonce = Array.from({ length: 32 }, () =>
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"[
      Math.floor(Math.random() * 62)
    ]
  ).join("");

  const baseUri = webview.asWebviewUri(distUri);

  // Rewrite asset paths
  html = html.replace(/(src|href)="(\/[^"]+)"/g, (_m, attr, p) => {
    return `${attr}="${baseUri}${p}"`;
  });

  // Inject CSP + initial data
  html = html.replace(
    "</head>",
    `<meta http-equiv="Content-Security-Policy"
       content="default-src 'none';
                script-src 'nonce-${nonce}' ${webview.cspSource};
                style-src ${webview.cspSource} 'unsafe-inline';
                img-src ${webview.cspSource} data:;
                font-src ${webview.cspSource};">
     <script nonce="${nonce}">
       window.__LITESCOPE_INIT__ = ${JSON.stringify(initialData)};
     </script>
     </head>`
  );

  html = html.replace(/<script /g, `<script nonce="${nonce}" `);
  return html;
}

// ── Activate ──────────────────────────────────────────────────────────────────

export function activate(context: vscode.ExtensionContext) {
  const binPath = getBinaryPath(context);
  const treeProvider = new LitescopeTreeProvider(binPath);

  // Register tree view
  const treeView = vscode.window.createTreeView("litescope.databases", {
    treeDataProvider: treeProvider,
    showCollapseAll: true,
  });

  // Auto-open: if workspace has .db files, offer to load them
  autoDetectDatabases(treeProvider);

  // ── Commands ─────────────────────────────────────────────────────────────

  // Open a database file
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.openDatabase", async (uri?: vscode.Uri) => {
      let dbPath: string | undefined;

      if (uri) {
        dbPath = uri.fsPath;
      } else {
        const uris = await vscode.window.showOpenDialog({
          canSelectMany: false,
          filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
          title: "Open SQLite Database",
        });
        dbPath = uris?.[0]?.fsPath;
      }

      if (!dbPath) return;

      await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Window, title: "Litescope: Loading…" },
        () => treeProvider.addDatabase(dbPath!)
      );
    })
  );

  // Open table detail
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.openTable", async (item: TableItem) => {
      const panel = getDetailPanel(context);
      panel.title = `${item.table.Name} — ${path.basename(item.dbPath)}`;
      panel.webview.html = buildDetailHtml(panel.webview, context, {
        mode: "table",
        table: item.table,
        dbPath: item.dbPath,
      });
    })
  );

  // Diff two databases
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.diffDatabases", async () => {
      const databases = treeProvider.getDatabases();

      let oldPath: string | undefined;
      let newPath: string | undefined;

      if (databases.length >= 2) {
        // Let user pick from loaded databases
        const pick = await vscode.window.showQuickPick(
          databases.map((d) => ({ label: path.basename(d.dbPath), description: d.dbPath })),
          { title: "Select OLD (before) database", placeHolder: "Before" }
        );
        oldPath = pick?.description;

        const pick2 = await vscode.window.showQuickPick(
          databases
            .filter((d) => d.dbPath !== oldPath)
            .map((d) => ({ label: path.basename(d.dbPath), description: d.dbPath })),
          { title: "Select NEW (after) database", placeHolder: "After" }
        );
        newPath = pick2?.description;
      } else {
        // Fall back to file picker
        const old = await vscode.window.showOpenDialog({
          canSelectMany: false,
          filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
          title: "Select OLD (before) database",
        });
        oldPath = old?.[0]?.fsPath;

        const nw = await vscode.window.showOpenDialog({
          canSelectMany: false,
          filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
          title: "Select NEW (after) database",
        });
        newPath = nw?.[0]?.fsPath;
      }

      if (!oldPath || !newPath) return;

      const panel = getDetailPanel(context);
      panel.title = `Diff: ${path.basename(oldPath)} → ${path.basename(newPath)}`;
      panel.webview.html = buildDetailHtml(panel.webview, context, {
        mode: "diff-loading",
        oldPath,
        newPath,
      });

      try {
        const raw = await vscode.window.withProgress(
          { location: vscode.ProgressLocation.Window, title: "Litescope: Comparing…" },
          () => runBinary(binPath, ["diff", oldPath!, newPath!, "--format", "json"])
        );
        const result = JSON.parse(raw);
        panel.webview.html = buildDetailHtml(panel.webview, context, {
          mode: "diff",
          result,
          oldPath,
          newPath,
        });
      } catch (e) {
        vscode.window.showErrorMessage(`Litescope: ${e}`);
      }
    })
  );

  // Close database
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.closeDatabase", (item: DatabaseItem) => {
      treeProvider.removeDatabase(item.dbPath);
    })
  );

  // Refresh database
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.refreshDatabase", async (item: DatabaseItem) => {
      await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Window, title: "Litescope: Refreshing…" },
        () => treeProvider.refreshDatabase(item)
      );
    })
  );

  context.subscriptions.push(treeView);
}

export function deactivate() {
  detailPanel?.dispose();
}

// ── Auto-detect .db files in workspace ───────────────────────────────────────

async function autoDetectDatabases(provider: LitescopeTreeProvider) {
  const uris = await vscode.workspace.findFiles(
    "**/*.{db,sqlite,sqlite3}",
    "**/node_modules/**",
    5
  );
  // Don't auto-load — just show a notification if found
  if (uris.length > 0 && uris.length <= 5) {
    const names = uris.map((u) => path.basename(u.fsPath)).join(", ");
    const action = await vscode.window.showInformationMessage(
      `Litescope: Found ${uris.length} database file(s) — ${names}`,
      "Open All",
      "Dismiss"
    );
    if (action === "Open All") {
      for (const uri of uris) {
        await provider.addDatabase(uri.fsPath).catch(() => {});
      }
    }
  }
}
