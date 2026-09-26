---
name: safe-db-writes
description: Write to a production SQLite, Cloudflare D1 or Turso database safely — measure the blast radius on a dry run first, then apply with an automatic undo point. Use whenever you are about to run UPDATE, DELETE, INSERT or DDL against a real database, or the user asks to change data.
when_to_use: Triggered by "update the database", "delete these rows", "fix this record in prod", "run this SQL against D1", or any mutating statement on a database that is not a throwaway.
---

# Safe writes to a real database

An agent that runs `UPDATE ... WHERE` against production is one missing clause
away from rewriting a whole table. Litescope's write tools exist to make that
recoverable: measure first, then commit with an undo point already captured.

**Never run a mutating statement through a plain query tool on a database that
matters.** Use `litescope_query_write` / `litescope_migrate_apply`, which
require the server to be started with `--allow-writes`.

## The loop

### 1. Dry run — always first

```
litescope_query_write(source="./app.db", sql="UPDATE users SET plan='pro' WHERE org_id=42")
```

`apply` defaults to **false**: the statement runs inside a transaction that is
rolled back. What comes back is what matters:

- `rows_affected` — the blast radius. Compare it against what you expect
  *before* you go further.
- `preview[]` — per statement, its kind and row count.
- `blast_radius_diff` — schema and row-count changes the write would cause.

For D1 the dry run is measured on a pulled copy, so the numbers are exact
without touching production.

### 2. Judge the number

Stop and ask the user when the dry run disagrees with the intent:

- `rows_affected` is 0 → the `WHERE` matched nothing; the statement is wrong,
  not the data.
- `rows_affected` is much larger than expected → treat it as a bug in the
  predicate. Do not apply "to see what happens".
- The statement has no `WHERE` at all and is not deliberately a full-table
  operation → stop.

### 3. Apply

```
litescope_query_write(source="./app.db", sql="...", apply=true)
```

Before the write commits, an undo point is captured automatically — a file
snapshot locally, a Time Travel bookmark on D1 — and returned as
`rewind_token`. **Keep that token in your reply to the user.** It is the only
thing that makes the write reversible, and the user needs it after this
conversation ends.

### 4. Undo, if it was wrong

```
litescope_write_undo(source="./app.db", rewind_token="<token>")
```

## Rules that matter

- **A dry run is not a guarantee about the future.** It measures the database
  as it is now. If minutes pass, or the app is writing concurrently, re-run it.
- **Turso is execute-only**: it cannot dry-run, so `apply=true` is required
  there. Take a snapshot first (`litescope_snapshot`) and say so before you run.
- **A lock error is not a failure to retry blindly.** On `SQLITE_BUSY` the tool
  returns lock-doctor remediation instead of a raw error — follow it (see the
  `sqlite-lock-doctor` skill) rather than looping on the write.
- **Check for a backup first.** `litescope_health` reports `has_backup` and
  `snapshot_count`. If both are empty on a database that matters, take a
  snapshot before the write, not after.
- **A multi-statement change is a migration**, not a query: use
  `litescope_migrate_apply`, which rolls the whole thing back if any statement
  fails and restores the snapshot if the commit itself fails.
