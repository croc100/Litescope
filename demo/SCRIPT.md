# Litescope — demo recording script

A ~2.5 minute walkthrough for the Lemon Squeezy identity-verification submission
(attach as an `.mp4`, not a YouTube link). Shows what the product is and that it
does real work on real databases.

**Positioning (the one line):** *Litescope is the database operations layer for AI apps.*
Your AI app writes to SQLite/Turso; Litescope keeps that storage honest — schema,
migrations, and fleet health.

---

## Before you record

```bash
cd demo
python3 setup.py                                   # (re)generate the demo data
go build -o /tmp/litescope ../cmd/litescope        # build the binary
```

- Terminal: full-screen, large font (≥18pt), dark theme. The tool's palette is
  GitHub-dark + teal, so a dark terminal matches.
- The runner pauses for **ENTER** between steps — read the narration, then press
  ENTER to reveal the next command. Nothing is timed; take your time.
- Pro features are unlocked locally via `LITESCOPE_SKIP_VERIFY=1` +
  `LITESCOPE_LICENSE=lsc_pro_test` (already set inside `demo.sh`).

```bash
./demo.sh        # drives the whole walkthrough
```

---

## The data (what's on screen and why)

- **`app.db`** — a single AI-app DB. Healthy, but has a classic bug: a foreign key
  (`messages.user_id`) with **no index**. `schema.sql` is the *desired* schema
  (adds `users.verified_at` + the missing index).
- **8 tenant databases** in `litescope.fleet.yaml`, all tagged `group:prod`:
  - 5 **canonical** (the intended schema)
  - 2 **drifted** — `tenant-0006/0007`, missing the `audit_logs` table (a migration
    that never reached them)
  - 1 **corrupt** — `tenant-0008`, with a verified healthy backup sitting next to it.

---

## Scene-by-scene narration

### 1 · Triage a single AI-app database
> "Here's a database behind an AI app. One command tells me it's structurally
> healthy — integrity, WAL, fragmentation. But `advise` goes deeper: it caught a
> foreign key with no index. SQLite doesn't auto-index those, so every join scans
> the whole table. It hands me the exact `CREATE INDEX` to fix it."

`health app.db` → HEALTHY · `advise app.db` → 1 finding + the fix SQL.

### 2 · Plan a migration — schema-as-code + blast radius
> "I keep my schema declaratively in `schema.sql`. Litescope diffs the live database
> against it and generates the migration — but first it tells me the *blast radius*
> of each change. Adding a column is instant and lock-free; building the index takes
> a brief read-lock. No surprises in production."

`migrate plan app.db --schema schema.sql` → blast-radius table + generated SQL.

### 3 · The fleet — diagnose, then treat
> "Now scale it up. One app is easy; a fleet of tenant databases is where things rot."

**3a Fingerprint** — "I *think* every tenant runs one schema. Fingerprint proves it:
two distinct schemas, plus one unreachable. Two tenants are missing `audit_logs`."

**3b Health** — "Across the fleet, worst-first: tenant-0008 is corrupt."

**3c Blast radius** — "That corrupt tenant shares `group:prod` with seven others.
One bad file often means shared infrastructure is suspect — here's who to check
*before* the next page."

**3d Converge (dry-run)** — "Fix the drift: Litescope generates the SQL to bring the
two drifted tenants back to canonical, and dry-runs it first."

**3e Recover (dry-run)** — "And the corrupt one: restore it from its most recent
backup — verified healthy before use. The incident loop, closed."

### 4 · The same engine over MCP
> "Everything you just saw is also an MCP server. Point an AI agent at it and these
> become tools the model can call directly — health, diff, migrate-plan, advise,
> fingerprint, fleet-health. The agent operates the database the same way I just did."

`echo tools/list | litescope mcp` → the 8 read-only tools.

### Close
> "Litescope — the database operations layer for AI apps."

---

## Optional Scene 5 — the GUI (if recording the desktop app)

```bash
cd ../gui && wails dev
```
Show the **Fleet** panel topology map (fingerprint histogram + inline converge,
health triage + recover). Then Diff / Migrate panels. Keep it brief — the CLI is
the proof; the GUI is the polish.

---

## Reset between takes

```bash
python3 setup.py     # restores the corrupt tenant + drift to their starting state
```
