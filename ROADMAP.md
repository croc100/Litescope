# Litescope Roadmap

**Identity:** The MCP-first operations tool for SQLite and Cloudflare D1.

Litescope gives AI agents (Claude, Cursor, any MCP client) and developers a
complete operations layer for SQLite — inspect, diff, migrate, monitor, and
*safely repair* databases, whether they live as a local file, on Cloudflare D1,
or on Turso. Every operation is available as both an MCP tool and a CLI command.

**Why SQLite-first?** SQLite is the most-deployed database on earth and now runs
in production at the edge (D1, Turso). Its operational surface is thin — no DBA,
no ops console, just a file and a thin provider API. Litescope is the missing
operations layer, and it leans into three things generic DB tools cannot do:

1. **File superpowers** — a SQLite database is a single file, so we can rewind it,
   bisect its history, and snapshot it instantly.
2. **DBA autopilot** — index recommendations, ANALYZE, and `PRAGMA optimize`
   applied automatically, each action explained in plain language.
3. **Lock doctor** — diagnose and fix `database is locked` / `SQLITE_BUSY`, the
   single most common SQLite production failure.

**Why MCP?** AI agents need to query, inspect, migrate, and *repair* databases
autonomously. Litescope exposes everything as MCP tools — read-only by default,
with explicit, guarded opt-in for writes — so agents can operate on real
databases without hand-written glue and without footguns.

---

## Shipped

**Core inspection & diagnosis**
- `mcp` — MCP server; every tool below callable from Claude / any MCP client
- `health` — fault detection: corruption, WAL bloat, fragmentation, reachability
- `schema` — table/column/index inspection; Mermaid ERD (`--erd`)
- `diff` — schema + row-count comparison across any two sources (local / D1 / Turso)
- `advise` — index recommendations, FK-without-index detection
- `lint` — schema anti-pattern linter (CI-native)
- `doctor` — one-command checkup (integrity + health + advise + lint)
- `check` — backup integrity verification
- **`locks` — lock doctor: diagnose `database is locked` / `SQLITE_BUSY`,
  prescribe WAL / `busy_timeout` / locking-mode fixes (local, D1, Turso)** ✨ new
- **`locks --live` / `--watch` — live lock detection: probe whether a writer
  holds the lock *right now* and identify the holding process (via `lsof`).
  Also `live=true` on the `litescope_locks` MCP tool** ✨ new

**Change management**
- `migrate` — generate + apply migrations; blast-radius analysis
- `validate` — snapshot-based migration locking for CI
- `monitor` — continuous drift detection; webhook alerts

**File superpowers**
- `rewind` — D1 Time Travel restore to any point in the last 30 days
- `bisect` — binary-search Time Travel snapshots to find the breaking migration
- **`snapshot` / `restore` — point-in-time backups for local (and Turso) SQLite:
  consistent VACUUM INTO copies in a sibling `.litescope-snapshots/`, with
  `list`, `--label`, `--keep N` retention, integrity-checked restore + automatic
  pre-restore safety snapshot. `health` now flags databases with no backup.
  Also `litescope_snapshot` / `_restore` / `_snapshot_list` MCP tools** ✨ new

**Fleet & data**
- `fleet` — parallel ops across hundreds of databases (health, fingerprint, migrate canary)
- `serve` — local web dashboard (fleet topology + health)
- `metrics` — Prometheus/OpenMetrics exporter
- `import` / `export` / `dump` — CSV/TSV/JSON/XLSX ↔ SQLite, portable SQL

**D1 & MCP foundation**
- Any-source DSN — every tool takes `source`: `./local.db`, `d1://DB_ID`, `turso://…`
- D1 env-var auth — `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` → short `d1://DB_ID`
- MCP write tools (opt-in, `--allow-writes`): `litescope_query_write`, `litescope_migrate_apply`
- D1 lifecycle MCP tools: `litescope_d1_list` / `_create` / `_delete` / `_pull`
- D1 ↔ local sync: `litescope d1 pull` / `push`
- **MCP write safety (the agent moat): `litescope_query_write` /
  `litescope_migrate_apply` are dry-run by default — they measure exact
  rows-affected blast radius inside a rolled-back transaction, auto-snapshot
  before any real `apply=true` write, and return structured lock-doctor
  remediation on `database is locked` instead of a raw error** ✨ new

---

## Now — close the moats

Moat #3 (lock doctor) is complete, static and live. Phase B (MCP write safety)
and Phase C (local/Turso backup & restore) have shipped — every SQLite source
now answers "did you back up?" and every agent write is guarded. What remains is
the autopilot moat.

Phase C follow-up (not yet built): **scheduled snapshots** — a retention/cron
policy for local/Turso so backups happen unattended, full parity with D1.

### Phase D — Autopilot (DBA self-driving) ← next

The third moat. Auto-apply safe optimizations across one DB or a whole fleet,
explaining every action in plain language.

- **`litescope autopilot`** — `PRAGMA optimize`, ANALYZE, index creation from
  `advise`, and unused-index cleanup, applied with explanations.
- **EXPLAIN QUERY PLAN analysis** — detect full scans and missing indexes from
  real queries, not just schema shape.
- **Fleet autopilot** — `litescope autopilot --fleet litescope.fleet.yaml`.

---

## Next — depth & reach

### Phase E — MCP protocol depth

We expose Tools only. The rest of the MCP spec makes agents far more effective.

- **Resources** — expose schema and a data dictionary as readable MCP resources,
  so agents get context without spending a tool call.
- **Prompts** — ship canned workflows ("diagnose my locked database",
  "review this migration") as MCP prompts.
- **Token budgeting** — enforce row limits / column projection / summarization on
  `litescope_query` so large result sets don't blow the agent's context window.

### Phase F — Exploration UI

Reach the non-developer / CSV-wrangling user the CLI doesn't serve today.

- **Data browsing** — browse and filter tables in the local dashboard
  (Datasette-style), not just fleet topology.
- **Visual diff & migration review** — see schema/data changes before applying.

### Phase G — Platform & distribution

- **`wrangler` plugin** — `wrangler d1` users get Litescope ops without a separate install.
- **GitHub Action** — migration CI: diff, lint, blast-radius comment on PRs.
- **Cloudflare Workers Launchpad** — apply for CF's startup support program.
- **Commercial license** — enterprise self-host / hosting / support on top of the
  AGPL-3.0 open core.

---

*Priority shifts based on ecosystem feedback. File issues or start a discussion
to influence the order.*
