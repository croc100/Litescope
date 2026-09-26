---
name: migration-review
description: Review a SQLite or Cloudflare D1 migration before it runs — what it changes, what it would break, and whether it can be rolled back. Use when the user asks to review, apply or write a migration, adds a column or table, or changes a schema.
when_to_use: Triggered by "review this migration", "apply the migration", "add a column", "change the schema", "is this migration safe", "does this need a backfill".
---

# Migration review

SQLite's `ALTER TABLE` is narrow, and the workarounds (rebuild, copy, rename)
are where migrations lose data. Review a migration against the *live* schema,
never against what the repo says the schema should be.

## 1. See what the database actually has

```
litescope_schema(source="./app.db")
```

Then compare it to where it is supposed to be:

```
litescope_migrate_diff(old="./app.db", new="./schema.sql")
litescope_diff(old="./app.db", new="d1://<db-id>")   # environments drifted?
```

A migration written against a schema the database does not have is the most
common failure, and it is invisible until it runs.

## 2. Plan it

```
litescope_migrate_plan(old="./app.db", new="./schema.sql")
```

The plan names each operation and flags the destructive ones. Read it for:

- **Column drops and renames** — SQLite implements these by rebuilding the
  table. Every index, trigger and view on it must be recreated, and anything
  the rebuild forgets is silently gone.
- **`NOT NULL` without a default on a populated table** — fails outright.
  It needs a backfill in between: add nullable → backfill → add the constraint.
- **A new `UNIQUE` index** — fails if the existing data already violates it.
  Check with a `SELECT ... GROUP BY ... HAVING COUNT(*) > 1` before, not after.
- **Foreign keys** — SQLite only enforces them when `PRAGMA foreign_keys=ON`,
  which is off by default and per connection. A migration that relies on
  cascade behaviour may do nothing.

## 3. Dry-run it against the real data

```
litescope_migrate_apply(source="./app.db", sql="<migration>")
```

Dry run by default: every statement is executed in a transaction and rolled
back, returning per-statement `rows_affected`. This is where a backfill that
touches ten times more rows than expected shows itself.

## 4. Apply

```
litescope_migrate_apply(source="./app.db", sql="<migration>", apply=true)
```

An undo point is captured first and returned as `rewind_token` — surface it to
the user. If the commit fails, the snapshot is restored automatically.

## 5. Verify

```
litescope_diff(old="./app.db", new="./schema.sql")
litescope_health(source="./app.db")
```

CLI: `litescope validate ./app.db --expect ./schema.sql` asserts that a
migration produced only the changes it was supposed to.

The diff should now be empty, and health should be unchanged. A migration that
rebuilt a table and lost its indexes will show up here as a schema difference,
not as an error at apply time.

## For Cloudflare D1

- Batch the migration into as few statements as possible: each statement is a
  round trip, and a long migration can time out.
- The undo point is a Time Travel bookmark, and Time Travel has a retention
  window — don't rely on it to undo a migration next week.
- Dry runs are measured on a pulled copy, so the counts are exact.
