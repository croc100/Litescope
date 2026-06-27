# litescope

Operations tooling for **SQLite**, **Cloudflare D1**, and **Turso** — an MCP
server for AI agents, plus schema diff, migrations, lint, point-in-time backups,
and a self-driving optimizer. This package is a thin installer that fetches the
prebuilt [Litescope](https://github.com/croc100/Litescope) binary for your
platform, so JavaScript and `wrangler` users can run it without a separate
install.

```bash
# Run the latest without installing
npx litescope doctor app.db

# Diff your dev schema against live D1
npx litescope diff local.db d1://DB_ID

# Run it as an MCP server for Claude / Cursor
npx litescope mcp --allow-writes
```

Install globally if you prefer:

```bash
npm install -g litescope
litescope --version
```

The binary is downloaded from the matching GitHub release on install (or on
first run) into the package's `vendor/` directory — macOS, Linux, and Windows
(amd64/arm64). No data leaves your machine.

Full documentation: **https://litescope-site.pages.dev/docs** ·
Source & license (AGPL-3.0): **https://github.com/croc100/Litescope**
