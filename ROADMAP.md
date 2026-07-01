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

All three moats (lock doctor, autopilot, file superpowers) are live, guarded by
MCP write safety (dry-run + auto-snapshot on every write), and surfaced end to
end — CLI, MCP tools, and the hosted/local dashboard.

- **Inspection & diagnosis** — `health`, `schema` (+ Mermaid ERD), `diff`,
  `advise`, `lint`, `doctor`, `check`, `locks` (static + `--live`/`--watch`
  holder detection)
- **Change management** — `autopilot` (ANALYZE/optimize/FK-index/VACUUM,
  `--queries` EXPLAIN-driven index proposals, `--fleet`), `migrate`, `validate`,
  `monitor`
- **File superpowers** — `rewind` (D1 Time Travel), `bisect`, `snapshot` /
  `restore` (local + Turso, `--label`/`--keep`/`schedule`), `salvage`
  (pure-Go `.recover`-style corruption recovery when no healthy backup exists)
- **Fleet & data** — `fleet`, `serve` (local dashboard: topology, ERD, data
  browser, diff, lock doctor, autopilot preview, snapshot/restore, D1 Time
  Travel panel, fleet-health-over-time timeline), `metrics`, `import`/`export`/`dump`
- **MCP protocol** — 24 tools spanning read/write/fleet/D1-lifecycle, 4 canned
  prompts (`diagnose_locked_database`, `review_migration`, `safe_optimize`,
  `health_checkup`), `schema`/`dictionary` resources, spec 2025-06-18
  (annotations, structured output, argument completion, resource subscriptions,
  Streamable HTTP transport with bearer auth + Origin allowlist)
- **D1 & Turso** — any-source DSN, env-var auth, D1 lifecycle tools, `d1 pull`/`push`
- **Platform** — GitHub Action, dual license (`COMMERCIAL.md`), npm wrapper
  (published as [`litescope`](https://www.npmjs.com/package/litescope)),
  hosted Enterprise dashboard (Cloudflare Workers + D1) live with org auth,
  billing, metadata-only push ingestion

---

## Next — depth & reach

### Phase H — SQLite depth ← starting now

Being *the* SQLite tool means going deeper than any generic DB client can.

1. **Deeper moats, in priority order:**
   - **Lock doctor time-series (top priority)** — per-database `SQLITE_BUSY`
     event history and WAL-checkpoint monitoring, feeding a multi-process
     contention timeline (fleet-level health timeline already ships; this is
     the per-DB drill-down).
   - **Reversible-write MCP contract** ✅ shipped — `litescope_query_write`
     (local) returns `{ rows_affected, blast_radius_diff, rewind_token }` in
     one response, undoable via `litescope_write_undo`. This was also Phase J's
     headline bet, pulled forward as the single highest-leverage MCP-server gap.
   - **Autopilot** — workload-driven index recommendations from a real query
     log, partial/expression-index proposals, cache & page-size tuning. ✅ shipped
   - **File superpowers** — automatic corruption recovery (pure-Go `.recover`
     pipeline: schema replay + rowid-bisection row salvage) ✅ shipped;
     page-level diff visualization and incremental backups still open.
2. **MCP resource/prompt depth** — live-state resources (`litescope://health/{source}`,
   `litescope://locks/{source}`) ✅ shipped, subscribable alongside
   schema/dictionary. Prompts currently cover single-DB diagnosis only; add
   fleet-scale workflow prompts (e.g. tenant health sweep) — still open.
3. **Single-database depth** — inline cell editing in the browser, FTS5
   full-text search tooling, trigger/view/virtual-table inspection, `ATTACH`
   multi-database queries, `EXPLAIN QUERY PLAN` visualization.
4. **Lint rule expansion** — grow the schema linter toward Bytebase-class
   coverage: naming conventions, index width, NULL handling, type-affinity,
   reserved words, migration-safety checks.
5. **Heartbeat staleness** — flag databases that stopped reporting ✅ shipped
   (`--stale-after` on `health`/`fleet health`; ties into Phase I's hosted
   dead-man's-switch).
6. **Cloudflare Workers Launchpad** — apply for CF's startup support program
   (external, non-code).

### Phase I — Fleet observability & alerting (hosted dashboard) ← queued

The differentiation thesis: generic Linux monitoring (Datadog, Grafana,
Prometheus, Netdata) watches *servers*. SQLite is a *file*, scattered across
apps, edge, devices, and per-tenant stores — invisible to those tools. Builds
on the live hosted backend's push ingestion.

- Heartbeat / staleness detection (Cloudflare Cron dead-man's-switch)
- Threshold alerts → email (Resend) on severity crossings
- Fleet assertions (push-time, pytest-style expectation checks)
- Scheduled push, packaged (systemd timer / cron recipes)
- Lock doctor for the fleet (surface contention fleet-wide)
- Remote actions — trigger rewind/bisect/lock resolution from the dashboard

### Phase J — Agent-native operations ← queued

The agent era spawns millions of small SQLite databases (per-agent,
per-session, per-edge-device memory). Headline bet (reversible writes) is
pulled forward into Phase H above; what remains here builds on it:

- Lock doctor for multi-agent concurrency (live contention subscription)
- Fleet ops for agent memory (health/fingerprint/drift/rewind reframed for
  swarms of agent DBs — demand still a hypothesis)
- Exploring, not committed: `sqlite-vec`/`vec0` ops for SQLite-RAG; provenance-
  stamped writes (agent/turn/tool — git blame for agent memory)

*Deliberately not on the roadmap: multi-engine support (Postgres/MySQL/DuckDB
in one MCP). Depth on SQLite's unique physics is the bet; breadth is not.*

### Phase K — Fleet operations at scale ← queued, revenue frontier

One company operating tens of thousands of SQLite files as a fleet — the
per-tenant/per-edge/per-agent shape Turso, D1, LiteFS, and PocketBase
deployments already live in.

- Tenant-fleet migration orchestration (supervised rollout: canary cohort,
  halt-on-error, per-tenant rollback via auto-snapshot)
- Online schema-change engine (create-new/backfill/swap/rename rebuild, the
  SQLite analog of `gh-ost`, guarded by the lock doctor)
- Replication & DR health (litestream/LiteFS awareness, failover integrity)

### Phase L — Cost & compliance (enterprise gates) ← queued

What unblocks big-tech procurement and recurring enterprise revenue.

- D1/Turso cost & capacity intelligence (attribute billed rows-read/storage to
  queries and tables, forecast plan-limit breaches)
- Compliance & governance posture (plaintext PII detection, encryption-at-rest
  verification, access audit trail)
- Signed integrity attestation (cryptographic proof a DB passed integrity +
  backup + canonical-schema checks at time T)

---

*Priority shifts based on ecosystem feedback. File issues or start a discussion
to influence the order.*
