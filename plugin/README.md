# Litescope for Claude Code

The operations layer for SQLite, Cloudflare D1 and Turso, packaged as a Claude
Code plugin: the MCP server plus five skills that tell the agent how to use it
when something is actually wrong.

## Install

```
/plugin marketplace add croc100/Litescope
/plugin install litescope@litescope
```

Then ask for what you want — "why is my app getting database is locked?",
"review this migration", "check every tenant database".

## What you get

**An MCP server** (`npx -y litescope mcp`) with 24 tools for inspecting,
diffing, migrating, monitoring and repairing SQLite, D1 and Turso databases.
Read-only by default.

**Five skills**, loaded only when they apply:

| Skill | Loads when |
| --- | --- |
| `sqlite-lock-doctor` | `database is locked`, `SQLITE_BUSY`, writes blocking readers |
| `safe-db-writes` | you are about to change data in a database that matters |
| `migration-review` | a migration is being written, reviewed or applied |
| `fleet-health-sweep` | many databases: per tenant, per edge, per agent |
| `sqlite-corruption-recovery` | a corrupt file, a failed integrity check, no backup |

**Four in-conversation views** (MCP Apps), rendered by hosts that support the
extension: the lock doctor, a health panel, a write's blast radius, and the
fleet grid. Hosts without it get the same results as text.

## Enabling writes

The bundled server is read-only, so installing the plugin cannot give an agent
write access to a production database. To allow writes, add `--allow-writes` to
the server's args in `.mcp.json`:

```json
{ "mcpServers": { "litescope": { "command": "npx", "args": ["-y", "litescope", "mcp", "--allow-writes"] } } }
```

Every write is a dry run by default even then: it reports its blast radius
first, and captures an undo point before it commits.

## Using an installed binary instead of npx

If you already have litescope on your `PATH` (`brew install croc100/tap/litescope`,
`go install`, or a release binary), point the server at it directly — it starts
faster and works offline:

```json
{ "mcpServers": { "litescope": { "command": "litescope", "args": ["mcp"] } } }
```

## Cloudflare D1 and Turso

Set credentials in the server's `env` (or your shell) and address databases as
`d1://<database-id>` or `turso://<url>`:

```json
{ "mcpServers": { "litescope": { "command": "npx", "args": ["-y", "litescope", "mcp"],
  "env": { "CLOUDFLARE_API_TOKEN": "...", "CLOUDFLARE_ACCOUNT_ID": "..." } } } }
```
