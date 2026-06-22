# Litescope Roadmap

> The operations toolchain for production SQLite — from a single database to a fleet.

This document defines the product tiers, what lives in each, and the sequencing.
It is the source of truth; README, website, and pricing must match it.

---

## The model: three tiers, one funnel

```
Free (OSS)  ──►  Pro (closed, paid)  ──►  Enterprise / "Ex" (closed, capped $49+/mo)
adoption          cash-flow validation     MRR + self-host + teams
```

Each tier is a validation gate for the next. We do not build the next tier
until the previous one shows the signal it is meant to prove.

| | **Free** | **Pro** | **Enterprise (Ex)** |
|---|---|---|---|
| Audience | Individual developer | Serious individual operator | Large company / team |
| Source | **Open source** | **Closed** (paid license key) | **Closed** |
| Price | $0 | $99 / year (Lemon Squeezy) | **from $49 / month**, capped (Lemon Squeezy) |
| Proves | Adoption / word of mouth | Willingness to pay | Real recurring revenue |
| Delivery | CLI + GUI binary | Same binary, key unlocks | Web SaaS + self-host app |

**One binary, license unlocks Pro.** There is no separate "Pro binary" — the same
public binary runs Free without a key and Pro with one (`RequirePro()` is a runtime
check). So downloads are never gated; the *key* is the gate. "Free + license = Pro."

**Why Free is open source but Pro is not:** open-sourcing the Free core maximizes
adoption and trust (the funnel) — this is the standard open-core split (GitLab CE
is open; paid features are not). Pro is *not* open-sourced: its buyer is the
individual developer, the person most able to recompile away a license gate. We
publish Free source for virality; Pro source stays private. (Accepted limit: the
public binary already contains Pro machine code gated at runtime, so a determined
user can binary-patch the gate — true protection for client-side Pro is impossible;
real lock-in lives server-side in Enterprise. The $99 tier relies on convenience +
goodwill, which is fine.)

**Why Ex is a capped monthly product (not contact-sales):** Lemon Squeezy, our
Merchant of Record, does not support custom/contact-sales arrangements, so
Enterprise ships as **fixed, capped monthly SKUs** — **$49/mo up to 25 databases**,
**Scale $149/mo up to 250**, self-host beyond that. The cap is deliberate: a
customer running hundreds of DBs self-selects a higher fixed SKU instead of
overrunning one flat price, which keeps hosting margins safe (we host on our own
infra) and stays Lemon-Squeezy-compatible. **Provisioned within ~1 week of
purchase** (the hosted/self-host setup is hands-on). The web SaaS code is the one
genuinely proprietary asset and stays closed.

---

## Tier → feature mapping

### Free — explore & inspect (single database)
- `diff` — schema & data comparison (all formats, HTML, CI/PR markdown)
- `schema` — inspect a single database (local / Turso / D1), Mermaid ERD output
- `import` — load a CSV / TSV / JSON file into a SQLite table with type inference (the data-in half: drag a spreadsheet, get a real database)
- `export` — stream a table or read-only query out to CSV / TSV / JSON (the data-out half: completes the spreadsheet round-trip)
- `dump` — export a local database as portable SQL (schema + data)
- `validate` — lock migrations to a spec, enforce in CI
- `check` — single-file backup integrity, incl. one `--against` reference comparison
- `migrate` (generate) — turn a diff into runnable SQL
- `monitor snapshot` / `monitor check` — baseline + drift check (cron/CI)
- `monitor watch` — continuous local watch of a single database (no webhook, no remote)
- `health` — single-DB fault check
- `fleet fingerprint` / `fleet health` — read-only shock diagnosis, up to 10 databases
- `serve` — **local, self-hosted web dashboard** (fleet topology + health + fingerprint); no cloud, no account, no telemetry; **drag-drop a CSV/TSV/JSON to add it as a database**
- **zero-config onboarding** — bare `litescope <file>` just works: a database opens `doctor`, a spreadsheet (`.csv`/`.tsv`/`.json`) runs `import`
- `metrics` — **Prometheus / OpenMetrics exporter** (fleet health + schema drift); one-shot or `--serve` /metrics scrape endpoint, drops into Grafana/Alertmanager. Read-only, up to 10 databases (full fleet with Pro)
- `advise` — index & query recommendations
- `mcp` — AI agent integration
- GUI: explorer, 1 named connection
- Local audit log (read) — trust/transparency hook

**Principle: nothing that drives adoption is paywalled.** Anything a developer
needs to *try and trust* the tool — on one database, or a peek at a small fleet —
is free.

### Pro — operate & automate (the single operator's power tools)
- `migrate apply` — `--dry-run`, automatic backup, transactional apply + rollback, `--verify`
- `check` (advanced) — batch (>1 file), `--save-report`
- `monitor watch` (advanced) — webhook alerts (Slack/Discord) and remote targets (Turso/D1)
- `fleet *` — full fleet (>10 DBs) and all actions: discover / snapshot / check / converge / recover / migrate (staged canary), plus `fingerprint`/`health` beyond the 10-DB Free preview and any watch/recover/alert
- `serve` — dashboard over the full fleet (Free shows the 10-DB preview; Pro lifts the cap)
- `metrics` — Prometheus exporter over the full fleet (Free shows the 10-DB preview; Pro lifts the cap)
- `policy` — write-protection / automated gates
- `team` — role-based access control (local)
- GUI: unlimited named connections, all Pro panels

**Principle: time-axis, automation, and many-DB-from-one-operator are Pro.**

### Enterprise (Ex) — teams, web & scale (closed, capped $49+/mo)
- **Hosted web dashboard** — the multi-user, cloud version of `serve`: fleet
  aggregation across machines, time-series history (health trend, schema drift
  over time), one screen for thousands of instances. (The single-operator local
  dashboard is free — see `serve` above.)
- **Alerting** — Slack / PagerDuty / on-call integration
- **SSO + org multi-user + org-level RBAC** (WorkOS/Clerk)
- **Self-host** — deploy the SaaS on the customer's own servers; detailed network
  configuration; bundles the OSS CLI features into one deployable application
- **Cloud-to-cloud** — runs in the customer's cloud environment as well as ours
- **SLA + priority support**
- CLI side: `litescope push` (agent → dashboard, API key) — **shipped**; the
  hosted backend (`litescope-cloud`, Cloudflare Workers + D1) is **live** at
  `litescope-cloud.croc100.workers.dev` (Lemon Squeezy webhook auto-provisioning,
  metadata-only ingestion)

**Principle: anything involving a team, a time-series backend, or a deployment
contract is Ex.** The data model already exists (audit log + fleet health events)
— the CLI becomes the agent with `litescope push`.

---

## Self-host & cloud-cloud (Ex delivery detail)

The enterprise tier must ship in two shapes:

1. **Self-host (on-prem):** a single deployable application the customer runs on
   their own servers. It bundles the OSS CLI capabilities + the dashboard +
   ingestion. Requires careful network configuration (ingress, agent → server
   auth, no outbound telemetry by default). This is non-negotiable: the same
   enterprises that want SSO are the ones that block outbound telemetry.
2. **Cloud-managed (our cloud):** we host it; customer points agents at us.
3. **Cloud-to-cloud:** the app must also run inside the customer's own cloud
   account (their AWS/GCP/CF), not only on-prem hardware or ours.

Infra fit: Cloudflare Workers + D1/Durable Objects + WorkOS/Clerk for SSO keeps
the managed version cheap; the self-host build is the same app packaged for the
customer environment.

---

## Sequencing (do not skip gates)

1. **Now — repackage:** unify code/README/website on the 3-tier model; fix the
   stale single-tier website; add web-SaaS messaging; Ex = capped monthly SKUs ($49/mo ≤25 DB, $149/mo ≤250; Lemon Squeezy; provisioned ~1 week).
2. **Free adoption signal:** publish OSS, measure installs / stars / inbound.
   *Gate: is anyone using it?*
3. **Pro willingness-to-pay:** Lemon Squeezy live (KYC) → first sales.
   *Gate: will an individual pay $99?*
4. **Ex demand signal:** Enterprise ($49+/mo) orders + dashboard waitlist.
   *Gate: does a team want the web/SSO/self-host product?*
5. **Build Ex** only after step 4 shows real inbound. Web SaaS is a separate,
   closed codebase; CLI gets `litescope push`.

---

## Feature enhancement plan (2026-06-20)

Phase 1–4 + DB-tool parity are done. Further work is **not** open-ended
gold-plating — it must serve the next gate (**Free adoption**). Four axes,
**Free first**:

1. **Free "wow" polish** _(in progress — current focus)_
   - ✅ `litescope doctor` — one-shot checkup (integrity + health + advise + lint)
     with a single verdict; the "point it at a DB and go *oh*" command.
   - ✅ `litescope lint` — SQLite schema design anti-pattern linter (no PK,
     untyped columns, not STRICT, AUTOINCREMENT, non-integer PK). CI-native,
     a category Atlas/squawk own for PG/MySQL but SQLite lacked.
   - ✅ ERD output — `schema --erd` emitting Mermaid (shareable in GitHub READMEs).
   - ✅ `dump` / export — SQL dump (sqlite3 `.dump` parity: schema+data, blob/NULL/quote-safe, round-trip verified). `--schema-only`/`--data-only`/`--table`/`-o`.
   - ◐ HTML shareable reports — `doctor --format html` ✅ (standalone, self-contained,
     auto-escaped, brand-themed). `diff` HTML next.
   - ⬜ `advise` accuracy — fewer false positives, clearer rationale.
   - Goal: maximize virality & shareability of the single-DB experience.
2. **Pro depth** _(after Pro revenue is validated)_ — fleet ops, `migrate apply`,
   `monitor watch` completeness; proves the $99 value. Don't over-invest pre-revenue.
3. **Stability & trust hardening** _(before OSS launch)_ — edge cases, error
   messages, test coverage, docs. "It doesn't break" is table stakes for virality.
4. **`litescope push` (Ex groundwork)** _(✅ shipped + deployed)_ — turns the CLI
   into the dashboard agent; the hosted `litescope-cloud` backend is live.
5. **Prometheus / OpenMetrics exporter** _(✅ shipped)_ — `litescope metrics`
   exposes fleet health + schema drift to Grafana/Alertmanager. Zero hosting
   cost; fills the observability-integration gap competitors' ops stacks assume.

**Discipline:** build axis 1 now; axes 2–4 are gated behind their validation
signals above. Resist building Ex/Pro depth before the Free adoption gate proves out.

---

## Competitive reality (keep honest)

This is not an empty blue ocean. Individual features are occupied:
- Schema diff / migration: **Atlas**, Bytebase, Flyway, Liquibase
- Audit / policy / RBAC: **Bytebase** (direct overlap with Phase 4)
- SQLite → web + hosted SaaS: **Datasette + Datasette Cloud** (same business model)
- Managed SQLite + dashboard: **Turso, Cloudflare D1, SQLite Cloud** (their DBs only)
- Backup/replication: **Litestream / LiteFS**

The only genuinely empty intersection:
> **cross-provider (Turso + D1 + local) fleet monitoring _plus_ operational
> actions (migrate / converge / recover) for thousands of SQLite instances.**

Risks to respect:
- **Market may not exist yet** — depends on the DB-per-tenant / local-first wave;
  early in 2026.
- **Platform risk** — if it grows, Turso/Cloudflare can build the dashboard
  themselves (they own the data plane).
- **Solo + category creation is the hardest combination** — don't try to create a
  category; ride the demand Turso already created, and validate cheaply (a post +
  waitlist) before building the big SaaS.
