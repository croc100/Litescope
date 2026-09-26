---
name: sqlite-corruption-recovery
description: Recover a corrupt or damaged SQLite database — "database disk image is malformed", a failed integrity check, or a file that will not open. Use when a database is corrupt, a backup is missing, or the user needs data salvaged out of a broken file.
when_to_use: Triggered by "database disk image is malformed", "database is corrupt", "integrity check failed", "recover my database", "the db file won't open", "I have no backup".
---

# Corruption recovery

Work on a **copy**. Every step below is reversible only because the original
file is untouched.

```bash
cp app.db app.db.broken     # before anything else
```

## 1. Confirm it, and see how bad it is

```
litescope_health(source="./app.db.broken", deep=true)
```

`integrity_ok: false` with the failing pages is real corruption. A database
that merely *fails to open* is often not corrupt at all — check first for a
missing `-wal`/`-shm` sidecar, a file copied while a writer was mid-transaction,
or a filesystem permission problem. Those are recoverable without salvage.

## 2. Prefer a backup over a salvage

Salvage recovers rows; it does not recover a database. If either of these
exists, use it and stop:

```
litescope_snapshot_list(source="./app.db")     # local snapshots
litescope_rewind(source="d1://<id>", to="2h ago")   # D1 Time Travel
```

A snapshot from an hour ago beats a perfect salvage, every time.

## 3. Salvage

When there is no healthy backup:

```
litescope salvage ./app.db.broken --output ./recovered.db
```

This is a pure-Go `.recover` pipeline: it replays the schema, then walks the
pages salvaging rows by rowid bisection around the damaged ones. It reports
what it could not read — **that report is the deliverable**, not an aside. The
user needs to know which tables came back partial.

## 4. Verify before you trust it

```
litescope_health(source="./recovered.db", deep=true)
litescope_diff(old="./app.db.broken", new="./recovered.db")
```

Then check row counts per table against whatever the application knows. A
salvage that returns 99% of rows is a success technically and a data-loss
incident operationally; say which one it is.

## 5. Find out why

Corruption is rarely spontaneous. The usual causes, in order:

- The database lives on a **network filesystem** (NFS, SMB, some Docker bind
  mounts). SQLite's locking cannot work correctly there. Move it to local disk.
- **Two processes with different locking assumptions**, or a file copied while
  a writer was active.
- `PRAGMA synchronous=OFF` plus a power loss or a hard container kill.
- Failing storage — check the host before blaming SQLite.

Close the incident by fixing the cause and putting a snapshot schedule in
place: `litescope snapshot schedule ./app.db --interval 6h --keep 28`.
