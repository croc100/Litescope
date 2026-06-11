# Litescope

[![Contributors](https://gcv-five.vercel.app/api/badge/croc100/litescope)](https://gcv-five.vercel.app/croc100/litescope)

**SQLite production operations — diff, validate, check, monitor.**

SQLite is everywhere: Turso, Cloudflare D1, mobile, edge. The tooling hasn't caught up. Litescope fills the gap.

```
litescope diff before.db after.db
litescope validate run before.db after.db --expect migration.yaml
litescope monitor watch turso://TOKEN@ORG/prod --baseline baseline.json --interval 1h
```

---

## Commands

### `diff` — Human-readable schema and data diff

```bash
litescope diff old.db new.db
litescope diff old.db new.db --format json
litescope diff old.db new.db --format markdown   # for CI / PR comments
litescope diff old.db new.db --html report.html

# Remote sources (schema diff)
litescope diff local.db turso://TOKEN@ORG/production
litescope diff local.db d1://TOKEN@ACCOUNT_ID/DATABASE_ID
```

Output:

```
Schema diff
  ~ users       + verified_at TEXT, - legacy_id INTEGER
  + audit_logs  new table (4 columns)
  - sessions    table removed

Data diff
  users         +12 rows  -3 rows
  audit_logs    +248 rows
```

---

### `schema` — Inspect a single database

```bash
litescope schema app.db
litescope schema turso://TOKEN@ORG/prod
litescope schema d1://TOKEN@ACCOUNT_ID/DB_ID
```

---

### `validate` — Lock migrations to a spec (free)

Snapshot your expected migration once, then enforce it in CI. Fails loudly if something unexpected changes.

```bash
# Capture expected diff as a spec file
litescope validate init before.db after.db --output migration.yaml

# Verify in CI — exits 1 if unexpected changes
litescope validate run before.db after.db --expect migration.yaml
litescope validate run before.db after.db --expect migration.yaml --format json
```

`migration.yaml` example:

```yaml
version: 1
description: "add verified_at to users"
schema:
  changed:
    - table: users
      added_columns:
        - name: verified_at
          type: TEXT
```

---

### `check` — Backup integrity verification (free)

```bash
# PRAGMA integrity check + schema comparison
litescope check backup.db --reference production.db

# Also compare row counts
litescope check backup.db --reference production.db --data
```

---

### `migrate` — Generate and apply schema migrations

Turn a schema diff into runnable SQL, then apply it safely.

```bash
# Generate migration SQL (free) — destructive changes report rows affected
litescope migrate before.db after.db --output migration.sql

# Preview: run everything in a transaction, then roll back (Pro)
litescope migrate apply prod.db migration.sql --dry-run

# Apply with automatic backup and verification (Pro)
litescope migrate apply prod.db migration.sql --verify after.db
```

`migrate apply` safety sequence:

1. Pre-flight integrity check — corrupt databases are refused
2. Automatic backup via `VACUUM INTO` (point-in-time consistent)
3. All statements run inside a single transaction
4. Foreign key + integrity verification before commit
5. Any failure rolls back; a failed commit restores the backup

SQLite does not support `DROP COLUMN` or type changes directly — Litescope generates the standard rebuild pattern (`CREATE` → `INSERT` → `DROP` → `RENAME`) automatically.

---

### `monitor` — Schema drift detection

Catch unplanned schema changes before they cause incidents.

```bash
# 1. Save baseline after a confirmed-good deploy
litescope monitor snapshot production.db --output baseline.json

# 2. Check for drift (free — use in cron or CI)
litescope monitor check production.db --baseline baseline.json
litescope monitor check turso://TOKEN@ORG/prod --baseline baseline.json --format json

# 3. Continuous watch with alerts (Pro)
litescope monitor watch turso://TOKEN@ORG/prod \
  --baseline baseline.json \
  --interval 1h \
  --webhook https://hooks.slack.com/...
```

`monitor check` exits **0** (no drift) or **1** (drift detected) — drop it directly into CI.

---

### `fleet` — Manage many databases at once (Cloud)

One database is `monitor`. A hundred databases is `fleet`. Built for teams running
SQLite at scale on Turso groups and Cloudflare D1.

```bash
# 1. Discover every database in a Turso org (or D1 account) → writes a fleet config
litescope fleet discover turso --org my-org --token $TURSO_API_TOKEN --db-token $TURSO_GROUP_TOKEN
litescope fleet discover d1 --account $CF_ACCOUNT_ID --token $CF_API_TOKEN

# 2. Capture baselines for the whole fleet in parallel
litescope fleet snapshot

# 3. Detect drift across the whole fleet in parallel — exits 1 if any DB drifted
litescope fleet check
litescope fleet check --tag group:prod      # filter by tag
litescope fleet check --format json         # for dashboards / CI
```

```
Fleet: production · 312 database(s)

●  tenant-0001   ok       18ms
●  tenant-0002   ok       21ms
▲  tenant-0148   drift    +1 table, ~1 table
○  tenant-0149   no baseline
✗  tenant-0203   error    connection refused

312 databases · 309 ok · 1 drift · 1 no baseline · 1 error
```

The fleet config (`litescope.fleet.yaml`) is a plain checked-in file:

```yaml
version: 1
name: production
databases:
  - name: tenant-0001
    dsn: turso://TOKEN@my-org/tenant-0001
    tags: [group:prod]
```

`fleet check` exits **1** when any database drifted or errored — drop it straight into CI to gate deploys across your entire fleet.

---

## GitHub Integration

### Automatic PR schema diff comments

```yaml
- uses: croc100/litescope-action@v1
  with:
    command: diff
    source: before.db
    target: after.db
    format: markdown
    comment-on-pr: "true"
```

### Validate migration in CI

```yaml
- uses: croc100/litescope-action@v1
  with:
    command: validate
    source: before.db
    target: after.db
    expect: .litescope/migration.yaml
```

### CI drift check

```yaml
- uses: croc100/litescope-action@v1
  with:
    command: monitor-check
    source: turso://TOKEN@ORG/prod
    baseline: .litescope/baseline.json
```

Exits 1 on drift → blocks the pipeline. See [croc100/litescope-action](https://github.com/croc100/litescope-action) for full options.

---

## Install

**Go install**

```bash
go install github.com/croc100/litescope/cmd/litescope@latest
```

**Homebrew** _(coming soon)_

```bash
brew install croc100/tap/litescope
```

**Binary download**

Download for macOS, Linux, or Windows from [Releases](https://github.com/croc100/Litescope/releases).

---

## Remote sources

| DSN format | Provider |
|---|---|
| `path/to/file.db` | Local SQLite file |
| `turso://TOKEN@ORG/DBNAME` | [Turso](https://turso.tech) |
| `d1://TOKEN@ACCOUNT_ID/DATABASE_ID` | [Cloudflare D1](https://developers.cloudflare.com/d1/) |

---

## Pricing

| | Free | Pro ($9/mo) | Cloud ($49/mo) |
|---|---|---|---|
| diff, schema, validate, check | ✓ | ✓ | ✓ |
| migrate (generate SQL) | ✓ | ✓ | ✓ |
| monitor snapshot / check | ✓ | ✓ | ✓ |
| migrate apply (backup + rollback) | — | ✓ | ✓ |
| monitor watch (continuous) | — | ✓ | ✓ |
| Webhook alerts (Slack, Discord) | — | ✓ | ✓ |
| **fleet** — manage 100s of DBs at once | — | — | ✓ |
| fleet discover / snapshot / check | — | — | ✓ |
| monitor history (drift timeline) | — | — | ✓ |
| Team access + audit trail | — | — | ✓ |

Set your license key:

```bash
export LITESCOPE_LICENSE=lsc_pro_xxxxxxxxxxxxxxxx
# or
echo "lsc_pro_xxxxxxxxxxxxxxxx" > ~/.litescope/license
```

Get a key at **[litescope.dev/pricing](https://github.com/croc100/Litescope#pricing)**.

---

## License

[Elastic License 2.0](LICENSE) — free for individuals and internal use.
Commercial distribution or embedding in a SaaS product requires a separate agreement.
