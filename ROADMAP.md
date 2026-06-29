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
  cleanup. With `--queries`, EXPLAIN-driven: infers the predicate columns behind
  a full table scan and proposes a runnable `CREATE INDEX`. Dry-run by default,
  auto-snapshot before applying, every action explained in plain language.
  `--fleet` runs it across a whole fleet; also the `litescope_autopilot` MCP
  tool** ✨ new
- `migrate` — generate + apply migrations; blast-radius analysis
- `validate` — snapshot-based migration locking for CI
- `monitor` — continuous drift detection; webhook alerts

**File superpowers**
- `rewind` — D1 Time Travel restore to any point in the last 30 days
- `bisect` — binary-search Time Travel snapshots to find the breaking migration
- **`snapshot` / `restore` — point-in-time backups for local (and Turso) SQLite:
  consistent VACUUM INTO copies in a sibling `.litescope-snapshots/`, with
  `list`, `--label`, `--keep N` retention, integrity-checked restore + automatic
  pre-restore safety snapshot, and `snapshot schedule` for unattended interval
  backups with retention (single DB or `--fleet`, `--once` for cron/systemd).
  `health` now flags databases with no backup. Also `litescope_snapshot` /
  `_restore` / `_snapshot_list` MCP tools** ✨ new

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
- **MCP spec 2025-06-18 — protocol upgraded from 2024-11-05, with machine-readable
  tool annotations (`readOnlyHint` / `destructiveHint` / `idempotentHint` /
  `openWorldHint` + titles) so clients can gate risky tools, structured output
  (`structuredContent` on every result + `outputSchema` on the core read tools)
  so agents consume results without re-parsing text, argument completion
  (`completion/complete` suggests known sources / D1 DSNs), resource subscriptions
  (`resources/subscribe` emits change notifications when a local DB file changes),
  and server logging (`notifications/message`, `logging/setLevel`)** ✨ new
- **MCP Streamable HTTP transport (MCP 2025-06-18) — the server is no longer
  stdio-only: `litescope mcp --http :7577 [--http-path /mcp]` exposes a single
  endpoint (POST = JSON-RPC, GET = SSE stream for server notifications, DELETE =
  session end) keyed by `Mcp-Session-Id`. Bearer-token auth (`--http-token` /
  `LITESCOPE_MCP_TOKEN`, constant-time) and an Origin allowlist (`--http-origin`,
  DNS-rebinding protection) make it publicly hostable. This is the foundation the
  hosted dashboard and remote fleet actions build on** ✨ new

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

Both Phase C/D follow-ups have now shipped:

- ✅ **Scheduled snapshots** — `snapshot schedule` takes unattended snapshots on
  an `--interval` with `--keep` retention (defaults on), for a single database or
  every local DB in a `--fleet` config. `--once` makes it a clean systemd-timer /
  cron unit. Local/Turso parity with D1 Time Travel's automatic backups.
- ✅ **EXPLAIN-driven autopilot** — full-scan findings from `autopilot --queries`
  are no longer guidance-only: the advisor infers the WHERE/JOIN predicate
  columns (equality-first composite ordering, skipping already-indexed leading
  columns) and emits a runnable `CREATE INDEX`. Autopilot applies it under
  `--aggressive`, snapshotting first like any other write.

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
  installer (`npm/`) fetches the matching release binary on first run.
  **Published to npm** as [`litescope`](https://www.npmjs.com/package/litescope).
- **Cloudflare Workers Launchpad** — apply for CF's startup support program.

### Phase H — SQLite depth ← next

Being *the* SQLite tool means going deeper than any generic DB client can. The
MCP transport foundation now ships (see Shipped); what remains is depth. Three
fronts, in priority order — each lands in the CLI first, then surfaces in the
hosted dashboard (Phase I) so the paying screen reflects every moat.

1. **Deeper moats** — push the three superpowers past parity:
   - *Lock doctor*: live WAL-checkpoint monitoring, a `SQLITE_BUSY` event
     time-series, and a multi-process contention timeline.
   - *Autopilot*: workload-driven index recommendations from an actual query
     log, partial / expression-index proposals, cache & page-size tuning.
   - *File superpowers*: page-level diff visualization, automatic corruption
     recovery (a `.recover` pipeline), and incremental backups.
2. **Single-database depth** — close the generic-client gap: inline cell editing
   in the browser, FTS5 full-text search tooling, trigger / view / virtual-table
   inspection, `ATTACH` multi-database queries, and `EXPLAIN QUERY PLAN`
   visualization.
3. **Lint rule expansion** — grow the schema linter from a handful of rules
   toward Bytebase-class coverage: naming conventions, index width, NULL
   handling, type-affinity, reserved words, and migration-safety checks.

**Dashboard surfacing (next concrete step).** The moats already shipped in the
CLI (lock doctor, autopilot, snapshot/restore, bisect/rewind) are not all
exposed in the hosted dashboard yet — only the diff panel is. Surfacing the
existing moats on the paying screen is near-zero new code for a large product
value jump, and the gaps it reveals (e.g. lock doctor needs a time-series view)
become the priority order for the depth work above.

**Dashboard redesign — a real monitoring console.** The dashboard is the paying
surface, so it has to read like a serious operations tool, not a hobby utility.
Shipped: an app shell (sidebar nav + routed views), dual light/dark theming, and
a dense at-a-glance fleet overview (KPI strip, topology heatmap, worst-first
worklist).

- ✅ **Panel restyle** — the lock doctor, diff, data browser, and ERD panels now
  share the shell's sharp, dense design tokens (status-only color), so the whole
  app is visually coherent in both themes.
- ✅ **Time dimension (fleet health)** — the dashboard is no longer point-in-time:
  a local SQLite history store (`~/.cache/litescope/history.db`) records a fleet
  health/size snapshot on each overview request, surfaced as per-KPI sparklines
  and a stacked ok/warning/critical "fleet health over time" timeline.

Remaining time-dimension depth (ties into Phase I heartbeat / alerting):

- **Per-database time-series** — `SQLITE_BUSY` and WAL-size history per DB (the
  fleet-level health timeline shipped; per-DB lock/WAL trends are next).
- **Heartbeat staleness** — flag databases that stopped reporting.

### Phase I — Fleet observability & alerting (hosted dashboard)

The differentiation thesis: generic Linux monitoring (Datadog, Grafana,
Prometheus, Netdata) watches *servers*. SQLite is a *file*, scattered across
apps, edge, devices, and per-tenant stores — invisible to those tools. Few tools
treat a fleet of thousands of SQLite files as first-class operational objects
**and act on them**. That's the open ground, and it leans on SQLite's unique
physics (WAL, pages, locks, single-file integrity).

**Live today (Enterprise hosted backend).** The hosted dashboard ships on
Cloudflare Workers + D1: org auth (OTP / magic-link), billing + plan caps, and
metadata-only push ingestion (`litescope push` posts health reports, schema
fingerprints, and drift — never customer data) rendered as a fleet overview.
What remains below turns that ingestion into active monitoring:

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
- **Remote actions** — move from *observe* to *act*: trigger
  rewind / bisect / lock resolution from the dashboard. Monitoring tools alert;
  LiteScope fixes.

### Phase J — Agent-native operations ← strategic frontier

The agent era spawns *millions* of small SQLite databases — per-agent,
per-session, and per-edge-device memory — and agent writes are the scariest thing
to put in production. Litescope's existing moats (snapshot, rewind, diff, fleet
fingerprint, lock doctor — all metadata-only and verifiable) are exactly the
primitives this needs. It's open ground no DB tool owns yet, and most of it is
repackaging parts we already ship.

**Headline — reversible writes.** The parts already exist
(`litescope_query_write` + auto-snapshot + `litescope_rewind` + diff). Package
them as one MCP contract: every agent write returns
`{ rows_affected, blast-radius diff preview, rewind_token }`, and any write is
undone in a single call — "give the agent write access without fear." This is the
near-term bet; the rest of this phase only makes sense once it lands.

**Then, building on the same primitives:**

- **Lock doctor for multi-agent concurrency.** Multiple agents hammering one
  SQLite file (`SQLITE_BUSY`) is the #1 multi-agent failure; let an agent
  subscribe to a live contention signal ("you're contended — back off / use
  WAL"). The existing lock-doctor moat, aimed at concurrency.
- **Fleet ops for agent memory.** Reframe the fleet engine (health, fingerprint,
  drift, lock contention) as the observability + recovery layer for swarms of
  agent SQLite memories: detect corruption / drift across many agent DBs, rewind
  a single agent's memory, fingerprint which agents diverged. Reuses shipped tech;
  the demand is still a hypothesis, so it stays a bet, not a commitment.

**Exploring (operational depth the target buyers may expect — not committed):**
vector-aware ops for `sqlite-vec`/`vec0` tables (SQLite-RAG ops, for AI platforms);
and provenance-stamped writes (agent/turn/tool, *git blame for agent memory* — an
audit primitive for enterprise). Both deepen the operations story; each waits on
real demand.

*Deliberately **not** on the roadmap: multi-engine support (Postgres / MySQL /
DuckDB in one MCP). Depth on SQLite's unique physics is the bet; breadth is not.*

### Phase K — Fleet operations at scale ← the revenue frontier

The depth phases above serve *one developer operating one database*. The money is
in *one company operating tens of thousands of SQLite files as a fleet* — the
per-tenant / per-edge / per-agent shape that Turso, Cloudflare D1, LiteFS, and
PocketBase deployments already live in. These are the unsolved operational jobs
those teams pay for, and each builds on the fleet engine and file-superpower moats
we already ship.

- **Tenant-fleet migration orchestration** — roll a schema change across N tenant
  databases as a supervised rollout, not a for-loop: automatic canary cohort,
  halt-on-error, progress + per-tenant status, and per-tenant rollback (restore
  from the auto-snapshot taken before each apply). The #1 nightmare of running a
  SaaS on per-tenant SQLite, and the highest-conviction commercial feature here.
- **Online schema-change engine** — safe, low-lock execution of the changes
  SQLite's thin `ALTER TABLE` can't do directly: generate and run the
  create-new / backfill / swap / rename rebuild (the SQLite analog of `gh-ost` /
  `pt-online-schema-change`), with blast-radius preview and the lock doctor
  guarding contention. Hard to build = a real moat; ties directly into `migrate`.
- **Replication & DR health** — make Litescope aware of the replication layer real
  SQLite shops run: is litestream caught up, is the LiteFS primary healthy, did a
  failover lose writes? An operations tool that ignores replication has a hole;
  this closes it (metadata-only, like the rest of the fleet story).

### Phase L — Cost & compliance (enterprise gates)

What unblocks big-tech procurement and recurring enterprise revenue — distinct
from developer-facing depth.

- **D1 / Turso cost & capacity intelligence** — usage is billed by rows-read and
  storage, and no tool shows where the money leaks. Attribute cost to the queries
  and tables driving it, surface the full-scan reading 10M rows, and forecast when
  a database hits plan limits. Autopilot watches *performance*; this watches the
  *bill*.
- **Compliance & governance posture** — the angle that fits the "DB verification
  tool" identity and clears the buyer's security review: detect plaintext PII in
  columns across the fleet, verify encryption-at-rest (SQLCipher / SEE), and keep
  an access audit trail. "Which of my 10k tenant DBs hold unencrypted PII?"
- **Signed integrity attestation** — a cryptographically signed certificate that a
  database passed integrity + backup + canonical-schema checks at time T, for
  contractual SLAs, supply-chain, and audit. No one issues verifiable proofs for
  SQLite state; this makes Litescope the system of record for "it was healthy."

---

*Priority shifts based on ecosystem feedback. File issues or start a discussion
to influence the order.*
