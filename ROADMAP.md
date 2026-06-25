# Litescope Roadmap

This is the public roadmap for Litescope — the operations toolchain for production SQLite.

**Positioning:** SQLite is the default database for AI apps, agents, and the edge — and it now runs real production on Turso and Cloudflare D1, one database per tenant, thousands of them. Litescope is the operations layer for those databases: the tool you reach for to see, trust, and fix your SQLite — from one local file to a fleet of thousands.

**Goal:** Become the indispensable operations layer for production SQLite. Everything passes through it, state accumulates inside it, and it's the tool you reach for when the pager goes off.

---

## The tiers

Litescope ships in three tiers, one continuous funnel:

| | **Free** | **Pro** | **Enterprise** |
|---|---|---|---|
| For | Individual developer | Serious individual operator | Teams & large companies |
| Source | **Open source** | Closed (license key) | Closed |
| Price | **$0** | **$99 / year** | **from $49 / month** |
| Delivery | CLI + GUI binary | Same binary, key unlocks Pro | Web dashboard + self-host |

**One binary — the license key is the gate.** The same public binary runs Free
without a key and unlocks Pro with one. Downloads are never gated. *Free + license = Pro.*

The Free core is open source to maximize trust and adoption — the standard
open-core split. Pro and Enterprise features are closed.

---

## What's in each tier

### Free — explore, inspect & validate a single database

- **`diff`** — schema + data comparison between any two SQLite sources (local, Turso, D1); all formats incl. HTML & CI/PR markdown
- **`schema`** — inspect tables, columns, indexes; Mermaid ERD output (`--erd`)
- **`dump`** — export a database as portable SQL (schema + data; `sqlite3 .dump` parity)
- **`import`** — turn a CSV/TSV/JSON file into a real, typed SQLite database (header-aware, type-inferred; `--append` / `--replace`)
- **`export`** — stream a whole table or any read-only query out to CSV/TSV/JSON
- **Zero-config onboarding** — bare `litescope <file>` routes a database to `doctor` and a spreadsheet to `import`; drag-and-drop a `.csv`/`.tsv`/`.json`/`.db` file onto the `serve` dashboard to add it to the fleet
- **`validate`** — snapshot-based migration locking for CI
- **`check`** — single-file backup integrity (PRAGMA + reference match)
- **`migrate` (generate)** — turn a diff into runnable SQL, with a blast-radius report
- **`monitor snapshot` / `monitor check`** — baseline + drift check for cron/CI
- **`health`** — single-DB fault check (corruption, WAL bloat, fragmentation)
- **`advise`** — index & query recommendations (catches un-indexed foreign keys)
- **`lint`** — SQLite schema design anti-pattern linter (no PK, untyped columns, not STRICT, AUTOINCREMENT, non-integer PK) — CI-native
- **`doctor`** — one-shot checkup (integrity + health + advise + lint) with a single verdict; HTML report via `--format html`
- **`mcp`** — Model Context Protocol server, so Claude and other agents can call Litescope as read-only tools
- **`serve`** — local, self-hosted web dashboard (fleet topology + health + fingerprint); no cloud, no account, no telemetry
- **`metrics`** — Prometheus / OpenMetrics exporter (fleet health + schema drift); one-shot or `--serve` /metrics scrape endpoint, drops into Grafana/Alertmanager
- **`monitor watch`** (local) — continuous watch of a single local database (webhook + remote targets are Pro)
- **`fleet fingerprint` / `fleet health`** — read-only shock diagnosis, up to 10 databases
- **`check --against`** — one reference comparison (batch + `--save-report` are Pro)
- **GUI** — desktop explorer, 1 named connection
- **Local audit log** (read)

*Principle: anything a developer needs to try and trust the tool on one database — or a peek at a small fleet — is free.*

### Pro — operate & automate ($99/year)

- **`migrate apply`** — `--dry-run`, automatic backup, transactional apply + rollback, `--verify`
- **`check` (advanced)** — batch (>1 file), `--save-report`
- **`monitor watch` (advanced)** — webhook alerts (Slack/Discord) and remote targets (Turso/D1)
- **`fleet *`** — full fleet (>10 DBs) and all actions: discover / snapshot / check / converge / recover / migrate (staged canary) across hundreds of Turso & D1 databases
- **`serve` / `metrics`** — dashboard and exporter over the full fleet (Free shows the 10-DB preview; Pro lifts the cap)
- **`policy`** — write-protection / automated gates
- **`team`** — role-based access control (local)
- **GUI** — unlimited named connections, all Pro panels

*Principle: automation, the time axis, and many-databases-from-one-operator are Pro.*

### Enterprise — teams, web & scale (from $49/month, capped; provisioned ~1 week)

- **Web dashboard** — fleet aggregation, time-series history (health trend, schema drift over time), one screen for thousands of instances
- **Alerting** — Slack / PagerDuty / on-call integration
- **SSO + org multi-user + org-level RBAC**
- **Self-host** — deploy the dashboard on your own servers or your own cloud; no outbound telemetry by default
- **SLA + priority support**
- CLI side: `litescope push` — agent → dashboard ingestion (API key, offline buffering)

*Principle: anything involving a team, a time-series backend, or a deployment contract is Enterprise.*

---

## Where we still fall short (honest gaps)

We keep this public because trust matters more than looking finished. Litescope's
defensible ground is **cross-provider fleet operations** (Turso + D1 + local) — as
a single-database browser it is deliberately not trying to beat the incumbents.
These are the gaps we know about, by area.

### Free — as a general SQLite tool

Competitors: DBeaver, TablePlus, Beekeeper, DB Browser for SQLite, Datasette.

- **Interactive ER diagram** (drag / zoom / relationship lines) — we emit Mermaid text only (`schema --erd`); DBeaver/TablePlus have visual ERDs.
- **Visual query builder** — the web SQL console now has query history + saved queries (v0.3.6), but no point-and-click visual builder.
- **Multi-engine support** (Postgres/MySQL/…) — SQLite-only by design; a weakness for "one tool for everything" buyers.
- **In-browser data publishing / sharing** — Datasette's core free feature; our `serve` is diagnostics-only.
- **Plugin / extension ecosystem** — single closed binary vs. DBeaver/Datasette ecosystems.

As a migration tool (vs. Atlas / Flyway / Liquibase) our free scope is actually
generous (diff → SQL, blast-radius, versioned `new/status/up`, declarative `plan`,
`lint`). Remaining gaps: maturity of a declarative schema language, and CI
integration depth (Atlas Cloud-style migration linting bots).

### Pro — as an operations / governance tool

Competitors: Bytebase (team & governance), Atlas Cloud (migration CI).

- **Change-request / approval workflow** — we have `policy` / `team` (RBAC) but no PR-style "request → review → merge" flow.
- **Configurable SQL-review policy engine** — Bytebase ships dozens of rules; we have `lint` (schema anti-patterns) only.
- **First-class migration CI bot** — the GitHub Action exists but isn't at Atlas Cloud level (auto PR comments, lint gates, approval gates).
- **Time-series history / trends** — drift and health are point-in-time; no trend view.
- **Alert channel breadth** — Slack/Discord webhooks only; no PagerDuty/Opsgenie/email digest.
- **Backup scheduling & retention** — `recover` exists, but no automated backup orchestration (Litestream's territory).

### Pricing — where we're cheaper vs. more expensive

- **Cheaper than:** Bytebase (their Team/Enterprise is ~$100+/seat/month; we're $99/year, seats unlimited & local), DBeaver PRO ($25/mo), Atlas Cloud when seat count is high, and Bytebase Enterprise (our hosted tier is $49–149/mo capped).
- **More expensive than:** **TablePlus** — it sells a one-time perpetual license (~$89); our **$99/year subscription** looks expensive to anyone who only wants a DB browser. We must win on operations (fleet / migrate / monitor), not on browsing.

### Web dashboard (`litescope serve`)

Competitors: Datasette, Bytebase UI, Turso/D1 consoles, TablePlus.

- **Time-series charts** — snapshots only, no trend graphs.
- **Auth / multi-user** — single local user; SSO and sessions are unbuilt Enterprise items.
- **Event feed / alert inbox** — no in-dashboard drift/fault timeline.
- **Per-DB drilldown depth** — a detail drawer exists, but no query/index-usage/slow-query insight.
- **Shareable links / embeds** — Datasette's strength (publish data by URL); absent.
- **Responsive / mobile** — unverified.
- **Custom dashboards / widgets** — Grafana via `metrics` is the workaround; no native widgets.

### Cross-cutting / maturity

- **Platform risk** — Turso/Cloudflare could ship the same dashboard themselves (they own the data plane).
- **Single defensible niche** — "cross-provider fleet monitoring + operational actions (migrate / converge / recover) for thousands of SQLite instances." As a single-DB tool the differentiation is thin.
- **No ecosystem / community yet** — vs. the large DBeaver/Datasette communities.
- **Weak trust signals** — closed main repo, small star count, no reference customers.
- **Thin docs / onboarding** — no tutorial videos or guided walkthroughs.

**Bottom line:** competing as a single-DB browser loses to TablePlus (perpetual
license), Datasette (free web exploration), and DBeaver (multi-engine). The one
square we can win is *diagnose + treat thousands of per-tenant SQLite databases* —
and there our pricing (Pro $99/yr, Enterprise $49/mo) is the real weapon.

---

## 🎯 Flagship — invincible *for SQLite*

Not a Swiss-army knife. The bar is simple: **for anyone running SQLite — on
Turso, D1, the edge, or an AI app — Litescope is the undisputed tool, and there's
a reason to reach for it over everything else.**

The strategy is to own **what is true only for SQLite**. Generic database tools
(Atlas, Bytebase, DBeaver) are built on the *server-database* mental model. The
three axes below are either impossible in that model or things those tools would
never prioritize — so they can't follow. Each is a promotion of assets we already
have (`health`, `advise`, VACUUM INTO snapshots, `audit`), not a from-scratch build.

### Moat 1 — the single-file superpower: exact time-travel & bisect

A SQLite database is **one file**. That makes things a Postgres tool physically
cannot do:

- **Byte-exact branch / snapshot / rewind** — a file copy (VACUUM INTO), not a
  logical `pg_dump`/restore. Time-travel that is *physically exact*, not reconstructed.
- **Page-level diff** — show exactly which pages/rows changed; compare against a
  known-good copy to pinpoint *corrupt pages*.
- **`litescope bisect`** — binary-search across snapshots to find the exact
  migration or write that broke a database. (git bisect for your DB — impossible
  on a server database.)

```
litescope rewind tenant_4821 --to "before last migration"
litescope bisect --good monday.db --bad now.db
```

### Moat 2 — there is no DBA: be the autopilot

People running SQLite at scale are **app developers and AI agents, not DBAs**.
They don't know WAL, checkpointing, VACUUM, ANALYZE, `page_size`, or
`busy_timeout`. Generic tools *expose* these knobs for a DBA operator; Litescope
should *manage* them and explain in plain language.

- Auto-resolve WAL bloat / checkpoint starvation, schedule defrag, run
  `PRAGMA optimize` / ANALYZE, flag STRICT / rowid anti-patterns — with one-key
  apply and a plain-language "why."
- Promotes today's `health` / `advise` diagnosis into an **autopilot action**.

```
litescope autopilot          # keep SQLite physics maintained, hands-off
```

### Moat 3 — `database is locked`: own SQLite's defining pain

SQLite's one fatal characteristic is the **single writer** (no row locks — a
whole-database write lock). Everyone running SQLite in production eventually cries
over `SQLITE_BUSY` / `database is locked` — and **no tool in the world helps.**

- Diagnose lock contention: long-held write transactions, writer starvation,
  misconfigured connection pools.
- Prescribe fixes in plain language: WAL mode, `busy_timeout`, transaction scope,
  batched writes. Across a fleet: rank the tenants that are lock hotspots.

```
litescope locks tenant.db    # who is starving the writer, and the fix
```

*This is the emptiest, most universal, most immediately "yes, this" space — and
it is SQLite-only. The concept does not even exist in a Postgres/MySQL tool.*

### The operations layer (on top of the three moats)

Once the moats exist, fleet operations orchestrate them across thousands of
per-tenant databases — **health-gated canary migration** (apply in 1% → 10% →
50% → 100% waves, auto-rollback from snapshots on the first fault) and **Shield**
(self-healing: drift auto-converges, new tenants inherit canonical, corrupt
databases auto-quarantine). Supporting: `branch` (COW rehearsal), `audit --fleet`
(one-button blast-radius report), query replay across N tenants, GitOps loop.

```
litescope fleet migrate --schema v2.sql --canary 1% --watch --auto-rollback
litescope shield --converge-on-drift --adopt-new --quarantine-corrupt
```

**Why defensible.** Bytebase (batch/tenant change) and Turso (multi-DB schema
propagation) have *pieces* of the operations layer, but no one owns the three
SQLite-only moats — and a server-DB tool can't, by construction. The standing
risk is the platform owners (Turso/CF) shipping similar; see *Cross-cutting /
maturity* above.

**Build order:** Moat 3 (lock doctor — emptiest, most universal) → Moat 1
(bisect/rewind — biggest demo shock) → Moat 2 (autopilot — ties them together),
with the fleet operations layer riding on top.

---

## 📋 Planned

Sequenced by leverage, and **funded by revenue**: each stage is unlocked by the
one before it earning its keep. Win individual developers first (free, zero
hosting cost), prove Pro demand, then invest that into the hosted web tier last —
the most expensive thing to run, built only once it's paid for.

### Sequencing principle

The order below is deliberately "cheapest-to-run and fastest-to-revenue first."
CLI/local features carry **no hosting cost**, so they ship first; anything that
needs a server we run is sequenced last and gated on Pro revenue.

1. **Monitor & alerting depth (CLI, no hosting cost)** — the Prometheus/OpenMetrics
   exporter is **shipped** (`litescope metrics`, see below); remaining work is
   richer alert channels on top of `monitor watch` / `fleet health --watch`.
2. **Ecosystem surface (thin adapters)** — broaden the
   [GitHub Action](https://github.com/croc100/litescope-action), add CI/CD recipes
   and a Terraform-friendly check mode. Integration via thin adapters, not
   reinventing each platform.
3. **Org-level collaboration** — extend the shipped local `audit` / `policy` /
   `team` into approval workflows and org-scoped RBAC. Local-first first, hosted
   later — this is the bridge into Enterprise.
4. **Multi-engine reach (selective)** — evaluate Postgres/MySQL *fleet* operation
   by building on existing OSS drivers/engines rather than rewriting them. The
   differentiation stays "fleet operations," not raw schema diffing.
5. **Enterprise web dashboard (revenue-gated, edge-hosted)** — fleet aggregation
   + time-series history over a hosted/self-hostable backend. The agent side,
   `litescope push`, is **shipped**, and the metadata-only edge backend
   (Cloudflare Workers + D1) is **live in early access**. Built on a cost-minimal
   edge stack: heavy analysis runs on the user's machine via the CLI/agent and
   only uploads results, so the hosted tier carries almost no fixed cost.
   Remaining: SSO, time-series history UI, self-host packaging.

**What stays free vs. hosted.** The open-source CLI is always free, including
self-hosting. Hosting we run (web dashboard, team backend) is the paid surface —
hosting has a real marginal cost, so it's never given away.

---

## 💡 Backlog

- `fleet vacuum` — reclaim space on the bloat candidates `fleet health` flags
- `migrate --online` — chunk-based backfill for large rebuilds (avoids long lock)
- Remote deep health (integrity_check over Turso/D1, beyond reachability)
- Browse/query UI — explore data in the browser (likely via Datasette
  interop rather than a from-scratch build)

### Under consideration

- **AI analyst (BYOK)** — an optional built-in agent (bring your own Claude / GPT /
  Gemini key) that reviews a database or fleet and *recommends* fixes for a human
  to approve. Diagnosis and recommendation only; never autonomous execution of
  destructive changes.

---

*Roadmap reflects current direction. Priority order may shift based on user feedback.*
