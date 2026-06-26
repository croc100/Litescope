# Litescope Roadmap

**Identity:** The MCP-first operations tool for Cloudflare D1 and SQLite.

Litescope gives AI agents (Claude, Cursor, any MCP client) and developers a
complete operations layer for D1 — inspect, diff, migrate, and monitor D1
databases the same way you would a local SQLite file. Every operation is also
available as a CLI command for humans and CI pipelines.

**Why D1?** D1 is production SQLite on Cloudflare's edge. Its management surface
is thin: the Workers dashboard and wrangler. Litescope is the missing ops layer —
and because D1 *is* SQLite, every SQLite-specific capability (schema diffing,
migration safety analysis, per-tenant fleet ops) applies directly.

**Why MCP?** AI agents need to query, inspect, and migrate databases autonomously.
Litescope exposes everything as MCP tools — read-only by default, with explicit
opt-in for writes — so Claude and other agents can reason about your D1 databases
without you writing glue code.

---

## Current status

### Done

- `mcp` — MCP server; all tools below callable from Claude / any MCP client
- `health` — fault detection: corruption, WAL bloat, fragmentation
- `schema` — table/column/index inspection; Mermaid ERD (`--erd`)
- `diff` — schema + row-count comparison across any two sources (local, D1, Turso)
- `migrate` — generate + apply migrations; blast-radius analysis
- `advise` — index recommendations, FK-without-index detection
- `lint` — schema anti-pattern linter (CI-native)
- `doctor` — single-command checkup (integrity + health + advise + lint)
- `check` — backup integrity verification
- `validate` — snapshot-based migration locking for CI
- `monitor` — continuous drift detection; webhook alerts
- `fleet` — parallel ops across hundreds of databases (health, fingerprint, migrate canary)
- `serve` — local web dashboard (fleet topology + health)
- `metrics` — Prometheus/OpenMetrics exporter
- `import` — CSV/TSV/JSON/XLSX → SQLite
- `export` — table/query → CSV/TSV/JSON
- `dump` — portable SQL export
- D1 connector — `d1://TOKEN@ACCOUNT_ID/DB_ID` and env-var short form

---

## Phase 1 — D1+MCP foundation (now)

Make D1 a first-class citizen in MCP tools. Today all MCP tools take a local
`path`; D1 and Turso DSNs are blocked. This phase closes that gap.

- **MCP tools accept any source** — `source` param replaces `path`; any DSN
  works: `./local.db`, `d1://ACCOUNT/DB_ID`, `turso://TOKEN@ORG/DB`
- **D1 env-var auth** — `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` → short
  form `d1://DB_ID` (no token in the string). Safe for AI agent configs.
- **`litescope_d1_list`** — new MCP tool: list all D1 databases in the account.
  Claude can call this first to discover DB IDs before operating on them.
- **`litescope_query`** — new MCP tool: run any read-only SQL on any source.
  The most-requested AI-agent primitive.

---

## Phase 2 — D1-native operations ✅ shipped

Features that only make sense because D1 is the primary target.

- ✅ **D1 Time Travel integration** — `litescope rewind d1://DB_ID --to "2h ago"`.
  Restore to any point in the last 30 days via Cloudflare's Time Travel API.
  Accepts relative durations (`2h ago`, `3d ago`, `yesterday`) or RFC 3339.
- ✅ **D1 fleet from account** — `litescope fleet discover d1` auto-generates a
  fleet config from all databases in the account. Supports `--merge` to update
  an existing config without losing baselines or tags.
- ✅ **MCP write tools (opt-in)** — `litescope_migrate_apply`, `litescope_query_write`
  behind an explicit `--allow-writes` flag on `litescope mcp`. Off by default.
- ✅ **`litescope_d1_create` / `litescope_d1_delete`** — lifecycle management from
  MCP (create a D1 database, drop it). Useful for AI-driven test-fixture setup.

---

## Phase 3 — SQLite moats (D1-flavored)

The three capabilities that generic DB tools cannot replicate — applied to D1.

- **Lock doctor** — diagnose `SQLITE_BUSY` / writer starvation across a D1 fleet;
  prescribe WAL mode, `busy_timeout`, pool config in plain language.
  (`litescope locks d1://DB_ID`)
- **Bisect** — binary-search across Time Travel snapshots to find the exact
  migration that broke a D1 database. (`litescope bisect --bad d1://DB_ID`)
- **Autopilot** — auto-apply `PRAGMA optimize`, ANALYZE, index recommendations
  across a D1 fleet; explain every action in plain language.
  (`litescope autopilot --fleet litescope.fleet.yaml`)

---

## Phase 4 — platform & distribution

- **Cloudflare Workers Launchpad** — apply for CF's startup support program;
  D1 integration is the pitch.
- **`wrangler` plugin** — ship Litescope as a wrangler plugin so `wrangler d1`
  users get ops tools without a separate install.
- **GitHub Action** — `litescope-action` for migration CI on D1: diff, lint,
  blast-radius comment on PRs.
- **AGPL dual-license** — commercial license for teams that can't ship AGPL
  source. Enables enterprise self-host without open-sourcing their product.

---

*Priority shifts based on D1 ecosystem feedback. File issues or start a
discussion to influence the order.*
