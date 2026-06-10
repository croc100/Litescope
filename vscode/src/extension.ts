import * as vscode from "vscode";
import * as path from "path";
import * as fs from "fs";
import * as cp from "child_process";
import * as os from "os";

// ── Binary resolution ─────────────────────────────────────────────────────────

function getBinaryPath(context: vscode.ExtensionContext): string {
  const platform = os.platform(); // 'darwin' | 'linux' | 'win32'
  const arch = os.arch();         // 'x64' | 'arm64'

  const name =
    platform === "win32" ? "litescope.exe" : "litescope";

  const triple =
    platform === "darwin"
      ? arch === "arm64"
        ? "darwin-arm64"
        : "darwin-amd64"
      : platform === "linux"
      ? arch === "arm64"
        ? "linux-arm64"
        : "linux-amd64"
      : arch === "arm64"
      ? "windows-arm64"
      : "windows-amd64";

  return context.asAbsolutePath(path.join("bin", triple, name));
}

function runBinary(
  binPath: string,
  args: string[]
): Promise<string> {
  return new Promise((resolve, reject) => {
    cp.execFile(binPath, args, { maxBuffer: 50 * 1024 * 1024 }, (err, stdout, stderr) => {
      if (err) {
        reject(new Error(stderr || err.message));
      } else {
        resolve(stdout);
      }
    });
  });
}

// ── Custom Editor Provider ────────────────────────────────────────────────────

class LitescopeEditorProvider implements vscode.CustomReadonlyEditorProvider {
  public static readonly viewType = "litescope.dbViewer";

  constructor(private readonly context: vscode.ExtensionContext) {}

  async openCustomDocument(
    uri: vscode.Uri
  ): Promise<vscode.CustomDocument> {
    return { uri, dispose: () => {} };
  }

  async resolveCustomEditor(
    document: vscode.CustomDocument,
    webviewPanel: vscode.WebviewPanel
  ): Promise<void> {
    webviewPanel.webview.options = {
      enableScripts: true,
      localResourceRoots: [
        vscode.Uri.joinPath(this.context.extensionUri, "webview-ui", "dist"),
      ],
    };

    webviewPanel.webview.html = this.getHtml(
      webviewPanel.webview,
      document.uri.fsPath
    );

    // Handle messages from the WebView
    webviewPanel.webview.onDidReceiveMessage(
      async (msg) => {
        await this.handleMessage(msg, webviewPanel.webview, document.uri.fsPath);
      },
      undefined,
      this.context.subscriptions
    );
  }

  private async handleMessage(
    msg: { type: string; payload?: unknown },
    webview: vscode.Webview,
    currentPath: string
  ) {
    const binPath = getBinaryPath(this.context);

    switch (msg.type) {
      case "getSchema": {
        // payload.path overrides currentPath (e.g. after user picks a different file)
        const targetPath =
          (msg.payload as { path?: string } | undefined)?.path ?? currentPath;
        try {
          const raw = await runBinary(binPath, [
            "schema", targetPath, "--format", "json",
          ]);
          webview.postMessage({ type: "schema", payload: JSON.parse(raw) });
        } catch (e) {
          webview.postMessage({ type: "error", payload: String(e) });
        }
        break;
      }

      case "diff": {
        const { oldPath, newPath } = msg.payload as { oldPath: string; newPath: string };
        try {
          const raw = await runBinary(binPath, [
            "diff", oldPath, newPath, "--format", "json",
          ]);
          webview.postMessage({ type: "diff", payload: JSON.parse(raw) });
        } catch (e) {
          webview.postMessage({ type: "error", payload: String(e) });
        }
        break;
      }

      case "pickFile": {
        const uris = await vscode.window.showOpenDialog({
          canSelectMany: false,
          filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
          title: "Select SQLite database",
        });
        if (uris && uris[0]) {
          webview.postMessage({ type: "pickedFile", payload: uris[0].fsPath });
        }
        break;
      }
    }
  }

  private getHtml(webview: vscode.Webview, dbPath: string): string {
    const distUri = vscode.Uri.joinPath(
      this.context.extensionUri,
      "webview-ui",
      "dist"
    );

    // Read the built index.html and rewrite asset paths to vscode-resource URIs
    const indexPath = path.join(
      this.context.extensionPath,
      "webview-ui",
      "dist",
      "index.html"
    );

    let html: string;
    try {
      html = fs.readFileSync(indexPath, "utf-8");
    } catch {
      // Fallback if not yet built
      return `<!DOCTYPE html><html><body>
        <p style="color:#ccc;font-family:sans-serif;padding:2rem">
          Webview assets not found. Run <code>npm run build:webview</code> inside
          <code>vscode/</code> first.
        </p></body></html>`;
    }

    const nonce = getNonce();
    const baseUri = webview.asWebviewUri(distUri);

    // Replace asset paths
    html = html.replace(/(src|href)="(\/[^"]+)"/g, (_m, attr, p) => {
      return `${attr}="${baseUri}${p}"`;
    });

    // Inject CSP + initial state
    html = html.replace(
      "</head>",
      `<meta http-equiv="Content-Security-Policy"
         content="default-src 'none';
                  script-src 'nonce-${nonce}' ${webview.cspSource};
                  style-src ${webview.cspSource} 'unsafe-inline';
                  img-src ${webview.cspSource} data:;
                  font-src ${webview.cspSource};">
       <script nonce="${nonce}">
         window.__LITESCOPE__ = {
           initialPath: ${JSON.stringify(dbPath)},
           vscodeApi: true
         };
       </script>
       </head>`
    );

    // Add nonce to inline scripts generated by Vite
    html = html.replace(/<script /g, `<script nonce="${nonce}" `);

    return html;
  }
}

// ── Diff command (two-file picker) ────────────────────────────────────────────

async function cmdDiffDatabases(context: vscode.ExtensionContext) {
  const binPath = getBinaryPath(context);

  const old = await vscode.window.showOpenDialog({
    canSelectMany: false,
    filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
    title: "Select OLD database",
  });
  if (!old) return;

  const nw = await vscode.window.showOpenDialog({
    canSelectMany: false,
    filters: { "SQLite databases": ["db", "sqlite", "sqlite3"] },
    title: "Select NEW database",
  });
  if (!nw) return;

  // Open a WebView panel for diff result
  const panel = vscode.window.createWebviewPanel(
    "litescope.diff",
    `Diff: ${path.basename(old[0].fsPath)} → ${path.basename(nw[0].fsPath)}`,
    vscode.ViewColumn.One,
    {
      enableScripts: true,
      localResourceRoots: [
        vscode.Uri.joinPath(context.extensionUri, "webview-ui", "dist"),
      ],
    }
  );

  const provider = new LitescopeEditorProvider(context);
  panel.webview.html = provider["getHtml"](panel.webview, old[0].fsPath);

  panel.webview.onDidReceiveMessage(async (msg) => {
    await provider["handleMessage"](msg, panel.webview, old[0].fsPath);
  });

  // Immediately trigger diff
  try {
    const raw = await runBinary(binPath, [
      "diff", old[0].fsPath, nw[0].fsPath, "--format", "json",
    ]);
    panel.webview.postMessage({ type: "diff", payload: JSON.parse(raw) });
    panel.webview.postMessage({ type: "setMode", payload: "diff" });
  } catch (e) {
    vscode.window.showErrorMessage(`Litescope diff failed: ${e}`);
  }
}

// ── Activate ──────────────────────────────────────────────────────────────────

export function activate(context: vscode.ExtensionContext) {
  // Register custom editor for .db / .sqlite / .sqlite3
  context.subscriptions.push(
    vscode.window.registerCustomEditorProvider(
      LitescopeEditorProvider.viewType,
      new LitescopeEditorProvider(context),
      { supportsMultipleEditorsPerDocument: false }
    )
  );

  // Register diff command
  context.subscriptions.push(
    vscode.commands.registerCommand("litescope.diffDatabases", () =>
      cmdDiffDatabases(context)
    )
  );
}

export function deactivate() {}

// ── Helpers ───────────────────────────────────────────────────────────────────

function getNonce(): string {
  let text = "";
  const possible =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 32; i++) {
    text += possible.charAt(Math.floor(Math.random() * possible.length));
  }
  return text;
}
