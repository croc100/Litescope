---
name: sqlite-lock-doctor
description: Diagnose and fix "database is locked" / SQLITE_BUSY in SQLite, Cloudflare D1 or Turso. Use when a database throws "database is locked", SQLITE_BUSY, a write times out, an app hangs on a transaction, or the user asks why writes are blocking readers.
when_to_use: Triggered by "database is locked", "SQLITE_BUSY", "writes are blocking", "my SQLite app hangs", "lock contention", "busy timeout".
---

# Lock doctor

`database is locked` is the most common way a SQLite deployment fails in
production. It is almost never a corrupt file — it is a configuration in which
contention was always possible, plus load that finally found it.

Answer two different questions, in this order.

## 1. Is something holding the lock right now?

Only worth asking while the incident is live, and only for a local file.

```
litescope_locks(source="./app.db", live=true)
```

`state` is `free`, `readable`, `locked` or `error`, and `holders` lists the
processes with the file open. A single long-running writer in `holders` is the
whole story: the fix is in that process, not in any PRAGMA.

CLI equivalent: `litescope locks ./app.db --live`

## 2. Why is contention possible at all?

This is the one that actually fixes the incident, and it works on a healthy
database too:

```
litescope_locks(source="./app.db")
```

Each finding carries the exact PRAGMA or DSN change in its `Fix` field. The
three that account for nearly every report:

| Finding | What it means | Fix |
| --- | --- | --- |
| `journal-not-wal` | In `delete`/`truncate`/`persist` journal mode a writer holds an exclusive lock for the whole transaction, so every reader gets `SQLITE_BUSY` | `PRAGMA journal_mode=WAL;` (persists in the file) |
| `busy-timeout-zero` | Every new connection starts at `busy_timeout=0`, so any contention fails instantly instead of retrying | Set it in the DSN (`file:app.db?_busy_timeout=5000`) or on open — it is **per connection**, so it must be in the pool's config, not run once |
| WAL bloat | The checkpoint is starved: readers never let the WAL truncate, so it grows without bound | `PRAGMA wal_checkpoint(TRUNCATE);` for the symptom, then find the long-lived read transaction causing it |

## Rules that matter

- **`busy_timeout` is per connection.** Running the PRAGMA once in a shell
  fixes nothing. It belongs in the connection string or the pool's on-connect
  hook, or every new connection reverts to 0.
- **`journal_mode=WAL` is persistent**, set once per database file — but it
  does not survive a copy that drops the `-wal` file, and it does not work on a
  network filesystem.
- **WAL is not a fix for a slow transaction.** It lets readers proceed during a
  write; it does not let two writers proceed. If the app has two concurrent
  writers, shorten the transactions.
- **D1 and Turso**: you cannot set these PRAGMAs. The tool returns
  provider-specific guidance instead — for D1 the answer is usually batching
  statements into one request, since each statement is its own round trip.

## After the fix

Confirm it, don't assume it:

```
litescope_locks(source="./app.db")     # verdict should be ok
litescope_health(source="./app.db")    # WAL should be back to a sane size
```

If contention keeps recurring, `litescope locks ./app.db --timeline` shows the
recorded contention windows, wait-time percentiles and top lock holders over
time, which is what tells you whether you fixed it or moved it.
