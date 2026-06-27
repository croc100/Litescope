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
- **`autopilot` — self-driving DBA: ANALYZE, PRAGMA optimize, auto-add missing
  foreign-key indexes, and (with `--aggressive`) VACUUM + redundant-index
  cleanup. Dry-run by default, auto-snapshot before applying, every action
  explained in plain language. `--fleet` runs it across a whole fleet; also the
  `litescope_autopilot` MCP tool** ✨ new
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
- **`serve` — local web dashboard: fleet topology + health, interactive ERD,
  read-only data browser (paginated/sortable + visual query builder), drag-drop
  import, and a visual schema/data diff panel — compare any two databases to
  review table/column/index changes and row-count deltas before applying** ✨ updated
- `metrics` — Prometheus/OpenMetrics exporter
- `import` / `export` / `dump` — CSV/TSV/JSON/XLSX ↔ SQLite, portable SQL

**MCP protocol depth**
- **Tools, Prompts, and Resources — the MCP server now exposes canned prompt
  workflows (`diagnose_locked_database`, `review_migration`, `safe_optimize`,
  `health_checkup`), and schema + data-dictionary as readable resources
  (`litescope mcp ./app.db` binds concrete resources; `litescope://schema/{source}`
  templates work for any source). `litescope_query` enforces token budgeting —
  `max_rows` cap + `columns` projection + truncation reporting** ✨ new

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

## Now — moats complete

All three moats have shipped: the lock doctor (static + live), MCP write safety
(Phase B), local/Turso backup & restore (Phase C), and autopilot (Phase D). Every
SQLite source answers "did you back up?", every agent write is guarded, and
routine tuning is one command — or one MCP call — away.

Open follow-ups before moving to depth & reach:

- **Scheduled snapshots** — a retention/cron policy for local/Turso so backups
  happen unattended, full parity with D1. (Phase C follow-up.)
- **EXPLAIN-driven autopilot** — `autopilot --queries` already feeds a query log
  through the advisor; next is turning recurring full-scans into proposed index
  actions automatically rather than guidance-only. (Phase D follow-up.)

---

## Next — depth & reach

Phase E (MCP protocol depth) and Phase F (exploration UI) have shipped.

### Phase G — Platform & distribution ← in progress

- ✅ **GitHub Action** — `uses: croc100/Litescope@v1` runs any Litescope command
  in CI (lint, diff, doctor), fails the check on findings, and posts a sticky
  blast-radius comment on the PR. See `action.yml` and
  `examples/github-actions/`.
- ✅ **Commercial licensing** — dual-license path documented (`COMMERCIAL.md`):
  AGPL-3.0 open core + commercial exception/support, plus the live hosted
  dashboard subscription.
- ✅ **npm wrapper** — `npx litescope …` for JS / `wrangler` users; a zero-dep
  installer (`npm/`) fetches the matching release binary on first run. Publish to
  npm is the only remaining step.
- **Cloudflare Workers Launchpad** — apply for CF's startup support program.

### Phase H — Fleet observability & alerting (hosted dashboard)

The differentiation thesis: generic Linux monitoring (Datadog, Grafana,
Prometheus, Netdata) watches *servers*. SQLite is a *file*, scattered across
apps, edge, devices, and per-tenant stores — invisible to those tools. No one
treats a fleet of thousands of SQLite files as first-class operational objects
**and acts on them**. That's the open ground, and it's only possible because of
SQLite's unique physics (WAL, pages, locks, single-file integrity).

- **Heartbeat / staleness detection** — a Cloudflare Cron Trigger scans for
  databases that stopped pushing (dead-man's-switch, like a ping-fail monitor).
  A DB silent past its expected interval flips to `unreachable`.
- **Threshold alerts → email** — on push, when a database crosses a severity
  threshold (corruption, WAL bloat, fragmentation, lock contention, schema
  drift), the dashboard emails the org owner automatically (Resend).
- **Fleet assertions (push-time verification)** — declare expectations in the
  fleet config (e.g. `expect: schema == canonical`, `expect: integrity == ok`);
  each scheduled push validates them pytest-style and fails/alerts on violation.
- **Scheduled push, packaged** — first-class systemd timer / cron recipes so an
  enterprise host self-reports every N minutes with one install step.
- **Lock doctor for the fleet** — surface "database is locked" contention
  across the fleet (build-order priority #1 of the SQLite moats).
- **Remote actions (the real moat)** — move from *observe* to *act*: trigger
  rewind / bisect / lock resolution from the dashboard. Monitoring tools alert;
  LiteScope fixes.

---

*Priority shifts based on ecosystem feedback. File issues or start a discussion
to influence the order.*
