# Litescope

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
| monitor snapshot / check | ✓ | ✓ | ✓ |
| monitor watch (continuous) | — | ✓ | ✓ |
| Webhook alerts (Slack, Discord) | — | ✓ | ✓ |
| Hosted monitoring dashboard | — | — | ✓ |
| Team access + audit trail | — | — | ✓ |

Set your license key:

```bash
export LITESCOPE_LICENSE=lsc_pro_xxxxxxxxxxxxxxxx
# or
echo "lsc_pro_xxxxxxxxxxxxxxxx" > ~/.litescope/license
```

Get a key at **[litescope.dev/pricing](https://litescope.dev/pricing)**.

---

## License

[Elastic License 2.0](LICENSE) — free for individuals and internal use.
Commercial distribution or embedding in a SaaS product requires a separate agreement.
